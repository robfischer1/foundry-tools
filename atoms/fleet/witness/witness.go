// Package fleetwitness is the atom fleet:witness: every changed .py/.go source file the star
// authored is shown to the code witness (narcissus); a test file is not (checks.WitnessTest).
// One file holds the atom, and both runners execute it (lld.md FN-085).
//
// WHAT CHANGED IS WHERE THE CHANGE SET COMES FROM. The atom reads the ONE change
// set the tree facts carry (In.Changed), so the base is fetched once per run,
// by whoever hands the atom its tree, and never here. Paths come back
// NUL-separated, so a name with a space is one path.
//
// A CHANGE SET WITH NO SOURCE NEVER TOUCHES THE NETWORK. The classification is
// decided before anything is asked: a push that changed a README settles
// "nothing to witness" with no request.
package fleetwitness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It asks narcissus's REST door as the lane pod's
// ServiceAccount (forge/ci), so it needs no SVID; its deadline grows with the
// sources it asks about.
var Def = atomkit.Def{
	ID: "fleet:witness", Stage: checks.StagePrepush, Lane: checks.LaneAny,
	Group:      checks.GroupBasic,
	Desc:       "Every changed .py/.go source file is shown to the code witness (narcissus), never a test: a canonical-class or Standard match is a finding, a Convention is advisory, novel is clean.",
	Surface:    "any:**",
	Toolchain:  "none",
	NotOnForks: checks.ForkFleetStyle,
	Deadline:   deadline,
	Check:      check,
}

const id = "fleet:witness"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneAny}

// sleep is the pause between attempts; nil is checks.SleepContext.
func sleep(w atoms.Witness) func(context.Context, time.Duration) error {
	if w.Sleep == nil {
		return checks.SleepContext
	}
	return w.Sleep
}

// do sends one request: the seam when a test set one, else the plaintext client.
func do(w atoms.Witness) func(*http.Request) (*http.Response, error) {
	if w.Do != nil {
		return w.Do
	}
	return checks.WitnessClient().Do
}

// THE DOOR IS NARCISSUS'S REST FACE (rest-mesh P4, wonka68-rm2). The ask is
// POST /v1/witness with the verb's arguments, plaintext from the lane pod, and
// Cilium L7 by ServiceAccount is the authorization: grants-narcissus admits
// forge/ci there and stamps it forge-ci (flux#7207), and forge's
// executor-egress lets the lane out to :8203 (flux#7224). It replaced the
// per-run SVID's MCP tools/call on the mTLS door (:8201); the run is still named,
// by the request's own caller argument.

// readyProbe reads the door's GET /openapi.json, the readiness the REST door
// serves. Whatever kept it from an answer (narcissus down, refusing, or silent
// past the probe's timeout) is the probe's error, which
// checks.WitnessReadiness words as "unreachable".
func readyProbe(w atoms.Witness) checks.WitnessProbe {
	return func(ctx context.Context, u string) (int, string, error) {
		ctx, cancel := context.WithTimeout(ctx, checks.WitnessReadyTimeout)
		defer cancel()
		status, _, body, err := send(ctx, w, http.MethodGet, u, nil)
		return status, body, err
	}
}

// send makes one request and reads its answer whole. A request that went out
// and drew no answer in time, or an answer cut short, is ErrWitnessNoAnswer;
// one that never reached the witness is the transport's error, which the ask
// retries.
func send(ctx context.Context, w atoms.Witness, method, u string, body []byte) (int, http.Header, string, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return 0, nil, "", err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := do(w)(req)
	if err != nil {
		if timedOut(err) {
			return 0, nil, "", fmt.Errorf("%w: %v", checks.ErrWitnessNoAnswer, err)
		}
		return 0, nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, "", fmt.Errorf("%w: the answer was cut short: %v", checks.ErrWitnessNoAnswer, err)
	}
	return resp.StatusCode, resp.Header, string(raw), nil
}

// timedOut is a deadline failure, which context.DeadlineExceeded and the
// client timeout both report through net.Error.Timeout.
func timedOut(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// ask posts one file's arguments and answers the status and tool result
// ClassifyWitness reads (checks.WitnessResult), retried while nothing answered.
func ask(w atoms.Witness) func(context.Context, string) (int, string, string, error) {
	post := func(ctx context.Context, body string) (int, string, string, error) {
		status, h, answer, err := send(ctx, w, http.MethodPost, checks.WitnessAskURL, []byte(body))
		if err != nil {
			return 0, "", "", err
		}
		status, result := checks.WitnessResult(status, h.Get("X-Star"), h.Get("Content-Type"), answer)
		return status, "application/json", result, nil
	}
	return func(ctx context.Context, body string) (int, string, string, error) {
		return checks.AskWitnessRetried(ctx, post, body, checks.WitnessAttempts, checks.WitnessRetryPause, sleep(w))
	}
}

// deadline is the atom's own deadline: its worst case over the sources this
// change set would ask about (checks.WitnessWorstCase). A change set that would
// not compute has nothing to ask, so it does not raise the run's per-atom
// timeout.
func deadline(in atomkit.In) time.Duration {
	if in.ChangedErr != nil {
		return 0
	}
	sources, _, _, _ := checks.WitnessChangeSet(in.Changed)
	return checks.WitnessWorstCase(len(sources))
}

// check shows every changed source the star authored to the witness. A source
// snapshot has no change set to read and the verdict says so; any other change
// set that would not compute is a 2.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	settle := func(state int, reason string, rows []checks.WitnessRow) checks.Verdict {
		return checks.VerdictOf(row, state, "narcissus — fleet:witness: "+reason+"\n\n"+checks.WitnessSummary(rows))
	}
	if in.ChangedErr != nil {
		var snap atoms.SnapshotError
		if errors.As(in.ChangedErr, &snap) {
			return settle(0, "no change set to witness at pre-push: the source is a "+snap.Kind+
				" snapshot with no commits; the landing's Job witnesses the real change set.", nil)
		}
		return settle(2, "CANNOT RUN - could not read the change set: "+in.ChangedErr.Error(), nil)
	}
	sources, skipped, vendored, tests := checks.WitnessChangeSet(in.Changed)
	if len(sources) == 0 {
		state, reason := checks.AggregateWitness(nil, skipped, vendored, tests)
		return settle(state, reason, nil)
	}
	// THE TARGET MAY BE THE THING THE CHANGE REPAIRS. A witness that does not say
	// ready is a could-not-run about narcissus, named so, with no ask sent
	// (checks/witness_ready.go says why).
	if ok, detail := checks.WitnessReadiness(ctx, readyProbe(in.Witness), checks.WitnessRESTURL,
		checks.WitnessReadyAttempts, checks.WitnessReadyPause, sleep(in.Witness)); !ok {
		return settle(int(checks.StateCannotRun), checks.WitnessNotReadyReason(detail), nil)
	}
	star := checks.StarName(in.Origin)
	asking := ask(in.Witness)

	// A FEW AT ONCE. The rows keep git's order, whatever order the answers
	// arrive in, and one file's failure is that file's row, not the run's.
	rows := make([]checks.WitnessRow, len(sources))
	t := in.Tree()
	slots := make(chan struct{}, checks.WitnessWorkers)
	var wg sync.WaitGroup
	for i, p := range sources {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			query, err := t.Read(p)
			if err != nil {
				rows[i] = checks.WitnessRow{Path: p, Class: "could-not-consult", Reason: "could not ask: " + err.Error()}
				return
			}
			body := checks.WitnessArgs(query, checks.WitnessLanguage(p), "ci:gate:"+star+"@HEAD", p)
			status, _, result, err := asking(ctx, body)
			if err != nil {
				rows[i] = checks.WitnessRow{Path: p, Class: "could-not-consult", Reason: "could not ask the witness: " + err.Error()}
				return
			}
			rows[i] = checks.ClassifyWitness(p, status, result)
		})
	}
	wg.Wait()
	state, reason := checks.AggregateWitness(rows, skipped, vendored, tests)
	return settle(state, reason+"\n"+checks.WitnessAskedAs(), rows)
}
