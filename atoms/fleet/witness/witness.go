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
	"sync/atomic"
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

// url is the plaintext MCP port; "" is checks.WitnessURL.
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

// askInClear posts one request to the plaintext port, retried as the chain
// retried it: a transport failure or a gateway's 502/503/504 is asked again
// (checks.WitnessAttempts in all, checks.WitnessRetryPause apart), any other
// answer, a 4xx and a 500 included, is the witness's own and comes back as it
// came. THE ENGINE CACHE CANNOT SERVE IT: the Dagger adapter's environment keys
// the exec afresh on every run, so no earlier answer stands in for a later ask.
func askInClear(ctx context.Context, w atoms.Witness, body string) (int, string, string, error) {
	post := func(ctx context.Context, body string) (int, string, string, error) {
		return checks.PostWitness(ctx, url(w), body)
	}
	return checks.AskWitnessRetried(ctx, post, body, checks.WitnessAttempts, checks.WitnessRetryPause, sleep(w))
}

// asking is how one run asks narcissus: ask posts one request, and say is the
// line the atom's output carries about the identity it asked as.
type asking struct {
	ask func(context.Context, string) (int, string, string, error)
	say func() string
}

// asker answers how this run asks narcissus.
//
// THE IDENTITY IS THE LANE POD'S SPIRE SVID, through the witnesscall helper,
// when the run forwarded the agent socket (In.Spire): it execs the helper at
// narcissus's mTLS door, with the same fallback (an ask that could not be made
// falls back to the clear port; one that was sent and drew no answer in time
// does not). A run with no socket asks in the clear and narcissus records
// `unidentified`. The line the atom's output carries says which way each ask
// went, so a difference in that line is the identity difference and not a
// verdict difference.
func asker(ctx context.Context, in atomkit.In) asking {
	inClear := func(why string) asking {
		checks.WarnWitnessInTheClear(why)
		return asking{
			ask: func(ctx context.Context, body string) (int, string, string, error) {
				return askInClear(ctx, in.Witness, body)
			},
			say: func() string { return checks.WitnessInTheClear(why) },
		}
	}
	if in.Spire == "" {
		return inClear("the lane forwarded no --spire socket")
	}
	env := []string{"WITNESSCALL_SOCKET=unix://" + in.Spire}
	who, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "witnesscall", Args: []string{"whoami"}, Env: env})
	if code < 0 {
		return inClear("witnesscall never ran: " + who)
	}
	if code != 0 {
		return inClear(who)
	}
	var fell, asked atomic.Int64
	ask := func(ctx context.Context, body string) (int, string, string, error) {
		asked.Add(1)
		status, ctype, answer, err := checks.AskWitnessRetried(ctx, identifiedPost(in, env), body,
			checks.WitnessAttempts, checks.WitnessRetryPause, sleep(in.Witness))
		if err == nil {
			return status, ctype, answer, nil
		}
		if errors.Is(err, checks.ErrWitnessNoAnswer) {
			// The identified request went out and the witness is slow. The
			// clear port would repeat it, double the load and file an
			// `unidentified` caller; the atom settles could-not-consult.
			return 0, "", "", err
		}
		fell.Add(1)
		checks.WarnWitnessInTheClear("the identified ask failed: " + err.Error())
		return askInClear(ctx, in.Witness, body)
	}
	return asking{ask: ask, say: func() string { return checks.WitnessAskedAs(who, fell.Load(), asked.Load()) }}
}

// identifiedPost posts one request through witnesscall at narcissus's mTLS door.
// Anything but an answer is an error, which the caller retries and then asks in
// the clear. The request rides in a file of its own, because the helper reads a
// file and the asks run at once.
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
// change set would ask about (checks.WitnessWorstCase). A dry run asks nothing
// and a change set that would not compute has nothing to ask, so neither raises
// the run's per-atom timeout.
func deadline(in atomkit.In) time.Duration {
	if in.WitnessDry || in.ChangedErr != nil {
		return 0
	}
	sources, _, _, _ := checks.WitnessChangeSet(in.Changed)
	return checks.WitnessWorstCase(len(sources))
}

// check shows every changed source the star authored to the witness. A DRY run
// (In.WitnessDry, the shadow's) asks nothing: it classifies the change set,
// lists what it would have asked and settles 2, never 0, so that no reader takes
// it for a verdict. A source snapshot has no change set to read and the verdict
// says so; any other change set that would not compute is a 2.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	settle := func(state int, reason string, rows []checks.WitnessRow) checks.Verdict {
		return checks.VerdictOf(row, state, "narcissus — fleet:witness: "+reason+"\n\n"+checks.WitnessSummary(rows))
	}
	if in.ChangedErr != nil {
		if in.WitnessDry {
			return settle(int(checks.StateCannotRun), checks.WitnessDryMark+" no change set to read: "+in.ChangedErr.Error(), nil)
		}
		var snap atoms.SnapshotError
		if errors.As(in.ChangedErr, &snap) {
			return settle(0, "no change set to witness at pre-push: the source is a "+snap.Kind+
				" snapshot with no commits; the landing's Job witnesses the real change set.", nil)
		}
		return settle(2, "CANNOT RUN - could not read the change set: "+in.ChangedErr.Error(), nil)
	}
	sources, skipped, vendored, tests := checks.WitnessChangeSet(in.Changed)
	if in.WitnessDry {
		return settle(int(checks.StateCannotRun), checks.WitnessDryText(sources, "ci:gate:"+checks.StarName(in.Origin)+"@HEAD", skipped, vendored, tests), nil)
	}
	if len(sources) == 0 {
		state, reason := checks.AggregateWitness(nil, skipped, vendored, tests)
		return settle(state, reason, nil)
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
	via := asker(ctx, in)
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
