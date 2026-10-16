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
// decided before the asker is built: a push that changed a README settles
// "nothing to witness" with no request, no helper exec and no socket.
package fleetwitness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It asks narcissus as the run's SVID, so it needs one; its
// deadline grows with the sources it asks about.
var Def = atomkit.Def{
	ID: "fleet:witness", Stage: checks.StagePrepush, Lane: checks.LaneAny,
	Group:      checks.GroupBasic,
	Desc:       "Every changed .py/.go source file is shown to the code witness (narcissus), never a test: a canonical-class or Standard match is a finding, a Convention is advisory, novel is clean.",
	Surface:    "any:**",
	Toolchain:  "none",
	Needs:      []string{"svid"},
	NotOnForks: checks.ForkFleetStyle,
	Deadline:   deadline,
	Check:      check,
}

const id = "fleet:witness"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneAny}

// probe is the witness's /ready reader; nil is checks.GetWitnessReady.
func probe(w atoms.Witness) checks.WitnessProbe {
	if w.Probe == nil {
		return checks.GetWitnessReady
	}
	return w.Probe
}

// url is the port the witness's /ready is read on; "" is checks.WitnessURL.
// It is only ever probed, never asked: an ask goes to checks.WitnessMTLSURL.
func url(w atoms.Witness) string {
	if w.URL == "" {
		return checks.WitnessURL
	}
	return w.URL
}

// sleep is the pause between attempts; nil is checks.SleepContext.
func sleep(w atoms.Witness) func(context.Context, time.Duration) error {
	if w.Sleep == nil {
		return checks.SleepContext
	}
	return w.Sleep
}

// asking is how one run asks narcissus: ask posts one request, and say is the
// line the atom's output carries about the identity it asked as.
type asking struct {
	ask func(context.Context, string) (int, string, string, error)
	say func() string
}

// asker answers how this run asks narcissus, or why it cannot.
//
// THE IDENTITY IS THE LANE POD'S SPIRE SVID, through the witnesscall helper, at
// narcissus's mTLS door, and there is no other way to ask. A run with no forwarded
// agent socket (In.Spire), or whose helper cannot say who it is, has no way to
// ask: the reason comes back and the atom settles could-not-run. NOTHING HERE
// EVER POSTS TO THE PLAINTEXT PORT. The clear port filed every such ask as an
// `unidentified` caller; the fallback is gone (foundry-tools#109), so a lane that
// lost its socket is loud instead of anonymous. An ask that was made and failed
// is that file's could-not-consult row, retried over the same door.
func asker(ctx context.Context, in atomkit.In) (asking, string) {
	if in.Spire == "" {
		return asking{}, "the lane forwarded no --spire socket, and the witness is asked only as the lane's SVID over mTLS, never in the clear"
	}
	env := []string{"WITNESSCALL_SOCKET=unix://" + in.Spire}
	who, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "witnesscall", Args: []string{"whoami"}, Env: env})
	if code < 0 {
		return asking{}, "witnesscall never ran: " + who
	}
	if code != 0 {
		return asking{}, "the lane has no identity to ask as: " + who
	}
	ask := func(ctx context.Context, body string) (int, string, string, error) {
		return checks.AskWitnessRetried(ctx, identifiedPost(in, env), body,
			checks.WitnessAttempts, checks.WitnessRetryPause, sleep(in.Witness))
	}
	return asking{ask: ask, say: func() string { return checks.WitnessAskedAs(who) }}, ""
}

// identifiedPost posts one request through witnesscall at narcissus's mTLS door.
// Anything but an answer is an error, which the caller retries. A request that
// was sent and drew no answer in time is ErrWitnessNoAnswer and is not retried:
// the witness is slow, and a repeat only doubles its load. The request rides in
// a file of its own, because the helper reads a file and the asks run at once.
func identifiedPost(in atomkit.In, env []string) checks.WitnessAsk {
	return func(ctx context.Context, body string) (int, string, string, error) {
		file := atoms.TempPath("witness-request", ".json")
		defer func() { _ = os.Remove(file) }()
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			return 0, "", "", fmt.Errorf("the request could not be written for the helper: %w", err)
		}
		out, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "witnesscall", Args: []string{"post", checks.WitnessMTLSURL, file}, Env: env})
		if code == checks.WitnessNoAnswerExit {
			return 0, "", "", fmt.Errorf("%w: %s", checks.ErrWitnessNoAnswer, out)
		}
		if code != 0 {
			return 0, "", "", errors.New(out)
		}
		return checks.ParseWitnessCall(out)
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
	// NO IDENTITY, NO ASK, AND NO NETWORK: this is settled before the witness's
	// /ready is read, so a lane without a socket touches nothing.
	via, why := asker(ctx, in)
	if why != "" {
		return settle(int(checks.StateCannotRun), "CANNOT RUN - "+why+". No file was asked.", nil)
	}

	// THE TARGET MAY BE THE THING THE CHANGE REPAIRS. A witness that does not say
	// ready is a could-not-run about narcissus, named so, with no ask sent
	// (checks/witness_ready.go says why).
	if ok, detail := checks.WitnessReadiness(ctx, probe(in.Witness), url(in.Witness),
		checks.WitnessReadyAttempts, checks.WitnessReadyPause, sleep(in.Witness)); !ok {
		return settle(int(checks.StateCannotRun), checks.WitnessNotReadyReason(detail), nil)
	}
	star := checks.StarName(in.Origin)

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
			body := checks.WitnessRequest(i+1, query, checks.WitnessLanguage(p), "ci:gate:"+star+"@HEAD", p)
			status, contentType, answer, err := via.ask(ctx, body)
			if err != nil {
				rows[i] = checks.WitnessRow{Path: p, Class: "could-not-consult", Reason: "could not ask the witness: " + err.Error()}
				return
			}
			status, result := checks.WitnessEnvelope(status, contentType, answer)
			rows[i] = checks.ClassifyWitness(p, status, result)
		})
	}
	wg.Wait()
	state, reason := checks.AggregateWitness(rows, skipped, vendored, tests)
	return settle(state, reason+"\n"+via.say(), rows)
}
