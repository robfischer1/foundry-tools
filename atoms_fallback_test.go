package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/catalogue"
	"dagger/foundry-tools/internal/checks"
)

// chainMark opens the reason of every verdict a stubbed chain answers.
const chainMark = "answered by the chain: "

// reaskCalls counts, by atom, how often a stubbed chain was asked.
type reaskCalls struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *reaskCalls) of(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[id]
}

// stubReasks replaces the chain every atom is re-asked through with one that
// counts its asks and answers a pass marked as the chain's; restored after.
func stubReasks(t *testing.T) *reaskCalls {
	t.Helper()
	calls := &reaskCalls{n: map[string]int{}}
	// the atoms the binary carries, and the ported ones it does not (a python
	// atom runs through the adapter alone: it is its own chain)
	var ids []string
	for _, a := range atoms.Builtin() {
		ids = append(ids, a.ID)
	}
	for _, e := range catalogue.Entries() {
		if e.Def != nil && !slices.Contains(ids, e.ID) {
			ids = append(ids, e.ID)
		}
	}
	for _, id := range ids {
		orig := registry[id]
		registry[id] = func(_ context.Context, _ *run) checks.Verdict {
			calls.mu.Lock()
			calls.n[id]++
			calls.mu.Unlock()
			v := checks.VerdictOf(checks.AtomByID(id), 0, "")
			v.Reason = chainMark + id
			return v
		}
		t.Cleanup(func() { registry[id] = orig })
	}
	return calls
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
// its chain, and the chain's answer is the vote.
func TestABinaryWideFailureIsAnsweredByTheChainsAndCounted(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		err  error
	}{
		{"the run failed", "", errors.New("the engine went away")},
		{"the binary was OOM killed", "", errors.New("the atoms binary exited 137: Killed")},
		{"the output is not a vector", "panic: oops", nil},
		{"every atom is left out", "[]", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			casting(t, voterBinary)
			castBinary = func(context.Context, *FoundryTools, string, string) (string, error) { return tc.out, tc.err }
			calls := stubReasks(t)
			engine.reset()
			engine.withTree(bareTree)
			m := bareModule()
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
			}
		})
	}
}

// ONE ATOM AT STATE 2 GETS EXACTLY ONE CHAIN ASK; the rest are the binary's.
func TestOneCouldNotRunAtomIsAskedOfItsChainOnce(t *testing.T) {
	casting(t, voterBinary)
	ids := atoms.StageIDs(checks.StageOrbit)
	binarySays(t, map[string]int{ids[1]: 2})
	calls := stubReasks(t)
	engine.reset()
	engine.withTree(bareTree)
	m := bareModule()
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
}

// A FINDING IS A VERDICT: state 1 is never asked again.
func TestAFindingIsNeverReAsked(t *testing.T) {
	casting(t, voterBinary)
	ids := atoms.StageIDs(checks.StageOrbit)
	binarySays(t, map[string]int{ids[0]: 1, ids[2]: 1})
	calls := stubReasks(t)
	engine.reset()
	engine.withTree(bareTree)
	m := bareModule()
	vs, err := m.vector(soon(t), checks.StageOrbit, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		if calls.of(v.Atom) != 0 || !strings.HasPrefix(v.Reason, votedMark) {
			t.Errorf("%s was re-asked or not the binary's: %+v", v.Atom, v)
		}
	}
	if vs[0].State != 1 || vs[2].State != 1 {
		t.Errorf("states %d %d", vs[0].State, vs[2].State)
	}
}

// A chain that cannot answer either leaves the binary's 2 standing and notes
// nothing; a lane that is over still gets the chain's ask, and the chain's answer.
func TestAFallbackThatCannotBeAskedLeavesTheBinarysTwo(t *testing.T) {
	casting(t, voterBinary)
	ids := atoms.StageIDs(checks.StageOrbit)
	binarySays(t, map[string]int{ids[0]: 2})
	stubReasks(t)
	orig := registry[ids[0]]
	delete(registry, ids[0])
	t.Cleanup(func() { registry[ids[0]] = orig })
	engine.reset()
	engine.withTree(bareTree)
	m := bareModule()
	vs, err := m.vector(soon(t), checks.StageOrbit, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if vs[0].State != 2 || !strings.HasPrefix(vs[0].Reason, votedMark) {
		t.Errorf("verdict %+v", vs[0])
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
	p := m.pollFor(voterBinary, checks.StagePrecommit, "", checks.AtomsForStage(checks.StagePrecommit), chain)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	v, ok := p.vote(ctx, "fleet:check-yaml")
	if chainCtxErr != nil {
		t.Errorf("the chain was handed a context that had ended: %v", chainCtxErr)
	}
	if !ok || v.State != 0 || len(asked) != 1 {
		t.Errorf("verdict %+v (%v), asked %v", v, ok, asked)
	}
}

// WITNESS IS INCLUDED: if its real ask could not be made, the chain's witness answers.
func TestAWitnessThatCouldNotAskFallsBackToTheChainsWitness(t *testing.T) {
	casting(t, voterBinary)
	binarySays(t, map[string]int{"fleet:witness": 2})
	calls := stubReasks(t)
	engine.reset()
	engine.withTree(bareTree)
	m := bareModule()
	vs, err := m.vector(soon(t), checks.StagePrepush, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		if v.Atom == "fleet:witness" && (!strings.HasPrefix(v.Reason, chainMark) || calls.of(v.Atom) != 1) {
			t.Errorf("witness %+v, asked %d times", v, calls.of(v.Atom))
		}
	}
}

// The push sequence falls back the same way, and stops on what the chain finds.
func TestThePushSequenceFallsBackToTheChain(t *testing.T) {
	casting(t, voterBinary)
	castBinary = func(context.Context, *FoundryTools, string, string) (string, error) { return "", errors.New("boom") }
	calls := stubReasks(t)
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

// End to end through the gate: a binary that answers nothing leaves the record
// the chains' record.
func TestAGateWhoseBinaryFailsRecordsTheChainsAnswer(t *testing.T) {
	casting(t, voterBinary)
	castBinary = func(context.Context, *FoundryTools, string, string) (string, error) { return "", errors.New("OOM") }
	stubReasks(t)
	engine.reset()
	engine.withTree(bareTree)
	engine.stdout(`"rev-parse","HEAD^{tree}"`, fakeTree+"\n")
	m := &FoundryTools{Source: dag.Directory(), Repo: "http://door:8215/rob/ares.git", Sha: buildSha}
	rec, err := m.Gate(context.Background(), fakeTree, gatePin, "base-sha", "")
	if err != nil {
		t.Fatal(err)
	}
	recordedOn(t, rec, "0", chainMark+"fleet:check-yaml")
}

// A PANIC IN THE BINARY'S RUN, or in reading what it printed, is an error for
// every atom and each goes to its chain: the lane is not killed.
func TestAPanickingBinaryRunFallsEveryAtomBackToItsChain(t *testing.T) {
	casting(t, voterBinary)
	castBinary = func(context.Context, *FoundryTools, string, string) (string, error) { panic("index out of range") }
	calls := stubReasks(t)
	engine.reset()
	engine.withTree(bareTree)
	m := bareModule()
	vs, err := m.vector(soon(t), checks.StageOrbit, "", "")
	ids := atoms.StageIDs(checks.StageOrbit)
	if err != nil || len(vs) != len(ids) {
		t.Fatalf("vector %v, err %v", vs, err)
	}
	for _, v := range vs {
		if !strings.HasPrefix(v.Reason, chainMark) || calls.of(v.Atom) != 1 {
			t.Errorf("%s: %+v, chain asked %d times", v.Atom, v, calls.of(v.Atom))
		}
	}
}
