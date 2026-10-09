package atoms

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// FLEET:WITNESS IN THE BINARY. Every changed .py/.go source file the star
// authored is shown to the code witness (narcissus); a test file is not
// (checks.WitnessTest). The chain is atoms_fleet.go's fleetWitness and the
// judgements are internal/checks/witness.go's, so the two readers cannot word a
// row differently.
//
// WHAT CHANGED IS WHERE THE CHANGE SET COMES FROM. The chain fetched the base
// into its own container and ran three git execs for it; the binary reads the ONE
// change set Collect computed (Input.Changed), so the base is fetched once per
// run, by the module, and never here. Paths come back NUL-separated, so a name
// with a space is one path (the chain split on whitespace).
//
// A CHANGE SET WITH NO SOURCE NEVER TOUCHES THE NETWORK. The classification is
// decided before the asker is built: a push that changed a README settles
// "nothing to witness" with no request, no helper exec and no socket.
//
// WHAT IT CANNOT REPRODUCE EXACTLY: the identity. See witnessAsker.

// Witness is how fleet:witness reaches narcissus. The zero value is the
// production port and the real clock.
type Witness struct {
	// URL is the plaintext MCP port; "" is checks.WitnessURL.
	URL string
	// Sleep is the pause between attempts; nil is checks.SleepContext.
	Sleep func(context.Context, time.Duration) error
}

func (w Witness) url() string {
	if w.URL == "" {
		return checks.WitnessURL
	}
	return w.URL
}

func (w Witness) sleep() func(context.Context, time.Duration) error {
	if w.Sleep == nil {
		return checks.SleepContext
	}
	return w.Sleep
}

// askInClear posts one request to the plaintext port, retried as the chain
// retried it: a transport failure or a gateway's 502/503/504 is asked again
// (checks.WitnessAttempts in all, checks.WitnessRetryPause apart), any other
// answer, a 4xx and a 500 included, is the witness's own and comes back as it
// came. THE ENGINE CACHE CANNOT SERVE IT: this is a process in a container whose
// exec is keyed afresh on every shadow call (CA_REASK), so no earlier answer
// stands in for a later ask.
func (w Witness) askInClear(ctx context.Context, body string) (int, string, string, error) {
	post := func(ctx context.Context, body string) (int, string, string, error) {
		return checks.PostWitness(ctx, w.url(), body)
	}
	return checks.AskWitnessRetried(ctx, post, body, checks.WitnessAttempts, checks.WitnessRetryPause, w.sleep())
}

// witnessAsk is how one run asks narcissus: ask posts one request, and say is
// the line the atom's output carries about the identity it asked as.
type witnessAsk struct {
	ask func(context.Context, string) (int, string, string, error)
	say func() string
}

// witnessAsker answers how this run asks narcissus.
//
// THE IDENTITY IS WHAT THE SHADOW MAY NOT HAVE. The chain asked as the lane
// pod's SPIRE SVID through the witnesscall helper, with the pod's agent socket
// mounted in the helper's container. The binary reproduces that when the module
// forwarded the same socket into ITS container (-spire, Input.Spire): it execs
// the same helper (witnesscall, a layer of the tools container) at the same mTLS
// door, with the same fallback (an ask that could not be made falls back to the
// clear port; one that was sent and drew no answer in time does not).
// What it cannot reproduce: a run with no socket, where the shadow asks in the
// clear and narcissus records `unidentified`, and a shadow whose container the
// agent attests differently from the helper's. The line the atom's output
// carries says which way each ask went, so a difference in that line is the
// identity difference and not a verdict difference.
func (in Input) witnessAsker(ctx context.Context) witnessAsk {
	inClear := func(why string) witnessAsk {
		return witnessAsk{ask: in.Witness.askInClear, say: func() string { return checks.WitnessInTheClear(why) }}
	}
	if in.Spire == "" {
		return inClear("the lane forwarded no --spire socket")
	}
	env := []string{"WITNESSCALL_SOCKET=unix://" + in.Spire}
	id, code := in.run(ctx, Cmd{Dir: in.Root, Name: "witnesscall", Args: []string{"whoami"}, Env: env})
	if code < 0 {
		return inClear("witnesscall never ran: " + id)
	}
	if code != 0 {
		return inClear(id)
	}
	var fell, asked atomic.Int64
	ask := func(ctx context.Context, body string) (int, string, string, error) {
		asked.Add(1)
		status, ctype, answer, err := checks.AskWitnessRetried(ctx, in.identifiedPost(env), body,
			checks.WitnessAttempts, checks.WitnessRetryPause, in.Witness.sleep())
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
		return in.Witness.askInClear(ctx, body)
	}
	return witnessAsk{ask: ask, say: func() string { return checks.WitnessAskedAs(id, fell.Load(), asked.Load()) }}
}

// identifiedPost posts one request through witnesscall at narcissus's mTLS door.
// Anything but an answer is an error, which the caller retries and then asks in
// the clear. The request rides in a file of its own, because the helper reads a
// file and the asks run at once.
func (in Input) identifiedPost(env []string) checks.WitnessAsk {
	return func(ctx context.Context, body string) (int, string, string, error) {
		file := tempPath("witness-request", ".json")
		defer func() { _ = os.Remove(file) }()
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			return 0, "", "", fmt.Errorf("the request could not be written for the helper: %w", err)
		}
		out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "witnesscall", Args: []string{"post", checks.WitnessMTLSURL, file}, Env: env})
		if code == checks.WitnessNoAnswerExit {
			return 0, "", "", fmt.Errorf("%w: %s", checks.ErrWitnessNoAnswer, out)
		}
		if code != 0 {
			return 0, "", "", errors.New(out)
		}
		return checks.ParseWitnessCall(out)
	}
}

// witnessDeadline is fleet:witness's own deadline: its worst case over the
// sources this change set would ask about (checks.WitnessWorstCase). A dry run
// asks nothing and a change set that would not compute has nothing to ask, so
// neither raises the run's per-atom timeout.
func witnessDeadline(in Input) time.Duration {
	if in.WitnessDry || in.ChangedErr != nil {
		return 0
	}
	sources, _, _, _ := checks.WitnessChangeSet(in.Changed)
	return checks.WitnessWorstCase(len(sources))
}

// fleetWitness: see the header. A DRY run (Input.WitnessDry, the shadow's) asks
// nothing: it classifies the change set, lists what it would have asked and
// settles 2, never 0, so that no reader takes it for a verdict. It exists
// because the shadow doubles every ask on a pull that touches .go/.py, and
// narcissus saturated on 2026-10-07. A source snapshot has no change set to read and
// the verdict says so; any other change set that would not compute is a 2.
func fleetWitness(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	settle := func(state int, reason string, rows []checks.WitnessRow) checks.Verdict {
		return checks.VerdictOf(a, state, "narcissus — fleet:witness: "+reason+"\n\n"+checks.WitnessSummary(rows))
	}
	if in.ChangedErr != nil {
		if in.WitnessDry {
			return settle(int(checks.StateCannotRun), checks.WitnessDryMark+" no change set to read: "+in.ChangedErr.Error(), nil)
		}
		var snap SnapshotError
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
	star := checks.StarName(in.Origin)

	// A FEW AT ONCE. The rows keep git's order, whatever order the answers
	// arrive in, and one file's failure is that file's row, not the run's.
	via := in.witnessAsker(ctx)
	rows := make([]checks.WitnessRow, len(sources))
	t := in.tree()
	slots := make(chan struct{}, checks.WitnessWorkers)
	var wg sync.WaitGroup
	for i, p := range sources {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			query, err := t.read(p)
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
