package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// chainMark opens the reason of every verdict a stubbed chain answers.
const chainMark = "answered by the chain: "

// chainCalls counts, by atom, how often a stubbed chain was asked.
type chainCalls struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *chainCalls) of(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[id]
}

// stubChains replaces the chain of every atom the binary registers with one that
// counts its asks and answers a pass marked as the chain's; restored after.
func stubChains(t *testing.T) *chainCalls {
	t.Helper()
	calls := &chainCalls{n: map[string]int{}}
	for _, a := range atoms.Builtin() {
		orig := registry[a.ID]
		registry[a.ID] = func(_ context.Context, _ *run) checks.Verdict {
			calls.mu.Lock()
			calls.n[a.ID]++
			calls.mu.Unlock()
			v := checks.VerdictOf(checks.AtomByID(a.ID), 0, "")
			v.Reason = chainMark + a.ID
			return v
		}
		t.Cleanup(func() { registry[a.ID] = orig })
	}
	return calls
}

// votingBox gives the module the ballot box a gate lane would, and answers it.
func votingBox(m *FoundryTools) *ballotBox {
	m.box = newBallotBox(nil, false)
	return m.box
}

// binarySays stubs the binary's answer for a stage: the given states by atom id,
// a pass for the rest.
func binarySays(t *testing.T, states map[string]int) {
	t.Helper()
	castBinary = func(_ context.Context, _ *FoundryTools, stage, _ string) (string, error) {
		var out []checks.Verdict
		for _, id := range atoms.StageIDs(stage) {
			v := checks.VerdictOf(checks.AtomByID(id), states[id], "binary: "+id)
			v.Reason = votedMark + id
			if states[id] == 2 {
				v.Reason = votedMark + id + " CANNOT RUN - a tool layer did not build"
			}
			out = append(out, v)
		}
		raw, _ := json.Marshal(out)
		return string(raw), nil
	}
}

// A BINARY-WIDE FAILURE DOES NOT TURN THE LANE RED: every atom is asked once of
// its chain, the chain's answer is the vote, and each fallback is counted with
// the binary's reason.
func TestABinaryWideFailureIsAnsweredByTheChainsAndCounted(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		err  error
		want string
	}{
		{"the run failed", "", errors.New("the engine went away"), "the atoms binary did not answer: the engine went away"},
		{"the binary was OOM killed", "", errors.New("the atoms binary exited 137: Killed"), "exited 137"},
		{"the output is not a vector", "panic: oops", nil, "the module's output is not a verdict vector"},
		{"every atom is left out", "[]", nil, "returned no verdict for this atom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			casting(t, voterBinary)
			castBinary = func(context.Context, *FoundryTools, string, string) (string, error) { return tc.out, tc.err }
			calls := stubChains(t)
			engine.reset()
			engine.withTree(bareTree)
			m := bareModule()
			box := votingBox(m)
			vs, err := m.vector(soon(t), checks.StageOrbit, "", "")
			ids := atoms.StageIDs(checks.StageOrbit)
			if err != nil || len(vs) != len(ids) {
				t.Fatalf("vector %v, err %v", vs, err)
			}
			for _, v := range vs {
				if v.State != 0 || !strings.HasPrefix(v.Reason, chainMark) {
					t.Errorf("%s was not answered by its chain: %+v", v.Atom, v)
				}
				if n := calls.of(v.Atom); n != 1 {
					t.Errorf("%s: the chain was asked %d times, want once", v.Atom, n)
				}
				if reason := box.fell[v.Atom]; !strings.Contains(reason, tc.want) {
					t.Errorf("%s: fallback noted as %q, want the binary's %q", v.Atom, reason, tc.want)
				}
			}
			if len(box.fell) != len(ids) {
				t.Errorf("%d fallbacks counted, want %d", len(box.fell), len(ids))
			}
		})
	}
}

// ONE ATOM AT STATE 2 GETS EXACTLY ONE CHAIN ASK; the rest are the binary's.
func TestOneCouldNotRunAtomIsAskedOfItsChainOnce(t *testing.T) {
	casting(t, voterBinary)
	ids := atoms.StageIDs(checks.StageOrbit)
	binarySays(t, map[string]int{ids[1]: 2})
	calls := stubChains(t)
	engine.reset()
	engine.withTree(bareTree)
	m := bareModule()
	box := votingBox(m)
	vs, err := m.vector(soon(t), checks.StageOrbit, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		wantChain := v.Atom == ids[1]
		if got := strings.HasPrefix(v.Reason, chainMark); got != wantChain {
			t.Errorf("%s: chain-answered %v, want %v (%q)", v.Atom, got, wantChain, v.Reason)
		}
		if n := calls.of(v.Atom); n != map[bool]int{true: 1, false: 0}[wantChain] {
			t.Errorf("%s: the chain was asked %d times", v.Atom, n)
		}
	}
	if len(box.fell) != 1 || !strings.Contains(box.fell[ids[1]], "a tool layer did not build") {
		t.Errorf("fallbacks %v", box.fell)
	}
}

// A FINDING IS A VERDICT: state 1 is never asked again.
func TestAFindingIsNeverReAsked(t *testing.T) {
	casting(t, voterBinary)
	ids := atoms.StageIDs(checks.StageOrbit)
	binarySays(t, map[string]int{ids[0]: 1, ids[2]: 1})
	calls := stubChains(t)
	engine.reset()
	engine.withTree(bareTree)
	m := bareModule()
	box := votingBox(m)
	vs, err := m.vector(soon(t), checks.StageOrbit, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		if calls.of(v.Atom) != 0 || !strings.HasPrefix(v.Reason, votedMark) {
			t.Errorf("%s was re-asked or not the binary's: %+v", v.Atom, v)
		}
	}
	if vs[0].State != 1 || vs[2].State != 1 || len(box.fell) != 0 {
		t.Errorf("states %d %d, fallbacks %v", vs[0].State, vs[2].State, box.fell)
	}
}

// A chain that cannot answer either leaves the binary's 2 standing and notes
// nothing; a lane that is over still gets the chain's ask, and the chain's answer.
func TestAFallbackThatCannotBeAskedLeavesTheBinarysTwo(t *testing.T) {
	casting(t, voterBinary)
	ids := atoms.StageIDs(checks.StageOrbit)
	binarySays(t, map[string]int{ids[0]: 2})
	stubChains(t)
	orig := registry[ids[0]]
	delete(registry, ids[0])
	t.Cleanup(func() { registry[ids[0]] = orig })
	engine.reset()
	engine.withTree(bareTree)
	m := bareModule()
	box := votingBox(m)
	vs, err := m.vector(soon(t), checks.StageOrbit, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if vs[0].State != 2 || !strings.HasPrefix(vs[0].Reason, votedMark) || len(box.fell) != 0 {
		t.Errorf("verdict %+v, fallbacks %v", vs[0], box.fell)
	}
}

func TestALaneThatEndsBeforeTheBinaryFallsBackToTheChain(t *testing.T) {
	casting(t, voterBinary)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	castBinary = func(context.Context, *FoundryTools, string, string) (string, error) { <-release; return "[]", nil }
	var asked []string
	var chainCtxErr error
	chain := func(c context.Context, id string) (checks.Verdict, error) {
		asked = append(asked, id)
		chainCtxErr = c.Err()
		return checks.VerdictOf(checks.AtomByID(id), 0, ""), nil
	}
	m := bareModule()
	box := votingBox(m)
	p := m.pollFor(voterBinary, checks.StagePrecommit, "", checks.AtomsForStage(checks.StagePrecommit), chain)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	v, ok := p.vote(ctx, "fleet:check-yaml")
	if chainCtxErr != nil {
		t.Errorf("the chain was handed a context that had ended: %v", chainCtxErr)
	}
	if !ok || v.State != 0 || len(asked) != 1 || !strings.Contains(box.fell["fleet:check-yaml"], "the lane ended before the atoms binary answered") {
		t.Errorf("verdict %+v (%v), asked %v, fallbacks %v", v, ok, asked, box.fell)
	}
}

// WITNESS IS INCLUDED: if its real ask could not be made, the chain's witness answers.
func TestAWitnessThatCouldNotAskFallsBackToTheChainsWitness(t *testing.T) {
	casting(t, voterBinary)
	binarySays(t, map[string]int{"fleet:witness": 2})
	calls := stubChains(t)
	engine.reset()
	engine.withTree(bareTree)
	m := bareModule()
	box := votingBox(m)
	vs, err := m.vector(soon(t), checks.StagePrepush, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		if v.Atom == "fleet:witness" && (!strings.HasPrefix(v.Reason, chainMark) || calls.of(v.Atom) != 1) {
			t.Errorf("witness %+v, asked %d times", v, calls.of(v.Atom))
		}
	}
	if _, ok := box.fell["fleet:witness"]; !ok || len(box.fell) != 1 {
		t.Errorf("fallbacks %v", box.fell)
	}
}

// The push sequence falls back the same way, and stops on what the chain finds.
func TestThePushSequenceFallsBackToTheChain(t *testing.T) {
	casting(t, voterBinary)
	castBinary = func(context.Context, *FoundryTools, string, string) (string, error) { return "", errors.New("boom") }
	calls := stubChains(t)
	engine.reset()
	engine.withTree(bareTree)
	vs, unreached, err := bareModule().sequence(soon(t), checks.StagePrepush, "")
	if err != nil || len(unreached) != 0 {
		t.Fatalf("unreached %v, err %v", unreached, err)
	}
	for _, id := range atoms.StageIDs(checks.StagePrepush) {
		if calls.of(id) != 1 {
			t.Errorf("%s: asked %d times", id, calls.of(id))
		}
	}
	for _, v := range vs {
		if strings.Contains(v.Reason, "atoms binary") {
			t.Errorf("a binary failure survived into the vector: %+v", v)
		}
	}
}

// THE REPORT COUNTS THE FALLBACKS WITH THE BINARY'S REASON, and an atom that fell
// back is not "compared": its vote is its chain.
func TestTheReverseShadowReportCountsFallbacks(t *testing.T) {
	ids := atoms.StageIDs(checks.StageOrbit)
	var chains []checks.Verdict
	voted := map[string]checks.Verdict{}
	for _, id := range ids {
		v := checks.VerdictOf(checks.AtomByID(id), 0, "")
		chains, voted[id] = append(chains, v), v
	}
	fell := map[string]string{ids[1]: "orbit:surface: CANNOT RUN - narc did not build\nsecond line"}
	got := renderReverse(ids, false, chains, nil, voted, fell, nil, 0)
	for _, want := range []string{
		"shadow atoms (binary voted): 3 compared, 3 identical, 0 same state, 0 state differs, 0 missing from the binary, 0 missing from the chains",
		"1 of 4 fell back to the chain",
		"  " + ids[1] + ": " + ids[1] + ": CANNOT RUN - narc did not build | second line",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
	if none := renderReverse(ids, false, chains, nil, voted, nil, nil, 0); strings.Contains(none, "fell back") {
		t.Errorf("a clean run reported fallbacks:\n%s", none)
	}
}

// End to end through the gate: a binary that answers nothing leaves the record
// the chains' record, and the line on stderr counts the fallbacks.
func TestAGateWhoseBinaryFailsRecordsTheChainsAnswerAndReportsTheFallbacks(t *testing.T) {
	origRun, origToday, origGrace, origOut := shadowRun, shadowToday, shadowGrace, shadowOut
	t.Cleanup(func() { shadowRun, shadowToday, shadowGrace, shadowOut = origRun, origToday, origGrace, origOut })
	shadowRun, shadowGrace = defaultShadow, time.Minute
	ids := atoms.StageIDs(checks.StageOrbit)
	shadowToday = func(context.Context, *FoundryTools, string, string, string) ([]checks.Verdict, error) {
		var vs []checks.Verdict
		for _, id := range ids {
			vs = append(vs, checks.VerdictOf(checks.AtomByID(id), 0, ""))
		}
		return vs, nil
	}
	var report bytes.Buffer
	shadowOut = func() io.Writer { return &report }
	casting(t, voterBinary)
	castBinary = func(context.Context, *FoundryTools, string, string) (string, error) { return "", errors.New("OOM") }
	stubChains(t)
	engine.reset()
	engine.withTree(bareTree)
	engine.stdout(`"rev-parse","HEAD^{tree}"`, fakeTree+"\n")
	m := &FoundryTools{Source: dag.Directory(), Repo: "http://door:8215/rob/ares.git", Sha: buildSha}
	rec, err := m.Gate(context.Background(), fakeTree, gatePin, "base-sha", checks.StageOrbit)
	if err != nil {
		t.Fatal(err)
	}
	recordedOn(t, rec, "0", chainMark+"orbit:contracts")
	if want := "4 of 4 fell back to the chain"; !strings.Contains(report.String(), want) || !strings.Contains(report.String(), "OOM") {
		t.Errorf("report %q lacks %q and the binary's reason", report.String(), want)
	}
}

// A PANIC IN THE BINARY'S RUN, or in reading what it printed, is an error for
// every atom and each goes to its chain: the lane is not killed.
func TestAPanickingBinaryRunFallsEveryAtomBackToItsChain(t *testing.T) {
	casting(t, voterBinary)
	castBinary = func(context.Context, *FoundryTools, string, string) (string, error) { panic("index out of range") }
	calls := stubChains(t)
	engine.reset()
	engine.withTree(bareTree)
	m := bareModule()
	box := votingBox(m)
	vs, err := m.vector(soon(t), checks.StageOrbit, "", "")
	ids := atoms.StageIDs(checks.StageOrbit)
	if err != nil || len(vs) != len(ids) {
		t.Fatalf("vector %v, err %v", vs, err)
	}
	for _, v := range vs {
		if !strings.HasPrefix(v.Reason, chainMark) || calls.of(v.Atom) != 1 || !strings.Contains(box.fell[v.Atom], "the atoms binary's run panicked: index out of range") {
			t.Errorf("%s: %+v, chain asked %d times, fallback %q", v.Atom, v, calls.of(v.Atom), box.fell[v.Atom])
		}
	}
}
