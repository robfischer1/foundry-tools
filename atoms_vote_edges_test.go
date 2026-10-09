package main

import (
	"context"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// THE BINARY STARTS BEFORE THE CHAINS ARE DONE, not after: the lane's wall time
// is the longer of the two, not their sum. The one chain atom left in this
// vector waits until the binary has been started, and says so if it never is.
func TestTheBinaryIsStartedBesideTheChainsNotAfterThem(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	casting(t, voterBinary)
	castBinary = func(_ context.Context, _ *FoundryTools, stage, _ string) (string, error) {
		once.Do(func() { close(started) })
		return binaryVector(t, stage), nil
	}
	// EVERY CHAIN ATOM WAITS for the binary to have started, and there are more of
	// them than the pool holds: a lane that started the binary only when its first
	// vote was read would have the pool full of waiting chains and the binary
	// behind them.
	var chainAtoms []string
	for _, a := range checks.AtomsForStage(checks.StagePrecommit) {
		if !slices.Contains(atoms.StageIDs(checks.StagePrecommit), a.ID) {
			chainAtoms = append(chainAtoms, a.ID)
		}
	}
	waiting := 0
	for _, id := range chainAtoms {
		orig := registry[id]
		t.Cleanup(func() { registry[id] = orig })
		registry[id] = func(context.Context, *run) checks.Verdict {
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Error("the chains filled the pool before the binary was started")
			}
			return checks.VerdictOf(checks.AtomByID(id), 0, "")
		}
		waiting++
	}
	engine.reset()
	engine.withTree(everyLaneTree)
	vs, err := bareModule().vector(soon(t), checks.StagePrecommit, "", "")
	if err != nil || waiting <= atomsInFlight {
		t.Fatalf("%d chain atoms against a pool of %d, err %v", waiting, atomsInFlight, err)
	}
	if len(vs) < len(atoms.StageIDs(checks.StagePrecommit)) {
		t.Fatalf("vector %d long", len(vs))
	}
}

// A lane that ends before the binary answers does not wait for it: each atom is
// a could-not-run that says why.
func TestAVoteTheLaneCannotWaitForIsACouldNotRun(t *testing.T) {
	casting(t, voterBinary)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	castBinary = func(context.Context, *FoundryTools, string, string) (string, error) {
		<-release
		return "[]", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	p := bareModule().pollFor(voterBinary, checks.StagePrecommit, "", checks.AtomsForStage(checks.StagePrecommit), nil)
	v, ok := p.vote(ctx, "fleet:check-yaml")
	if !ok || v.State != 2 || v.Atom != "fleet:check-yaml" || !strings.Contains(v.Reason, "the lane ended before the atoms binary answered") {
		t.Errorf("verdict %+v (%v)", v, ok)
	}
}

// The record is settled: a shadow still waiting for a vote is told none is
// coming, so it does not sit out the grace.
func TestFinishTellsTheShadowNoVoteIsComing(t *testing.T) {
	origRun, origGrace, origOut := shadowRun, shadowGrace, shadowOut
	t.Cleanup(func() { shadowRun, shadowGrace, shadowOut = origRun, origGrace, origOut })
	casting(t, voterBinary)
	shadowRun = func(ctx context.Context, m *FoundryTools, _, _ string) string {
		_, _, err := m.box.wait(ctx)
		if err == nil {
			return "a vote came"
		}
		return "waited: " + err.Error()
	}
	shadowGrace = 8 * time.Second
	var buf strings.Builder
	shadowOut = func() io.Writer { return &buf }
	h := (&FoundryTools{}).startShadow(soon(t), "", "b")
	start := time.Now()
	h.finish()
	if got := buf.String(); !strings.Contains(got, "waited: the lane settled without casting a vote") || time.Since(start) > 4*time.Second {
		t.Errorf("report %q after %v", got, time.Since(start))
	}
}

// With the chains voting there is no ballot box, and the shadow is the dry one,
// which says the chains voted.
func TestTheShadowOfAChainVotedLaneIsTheDryOne(t *testing.T) {
	origToday, origBinary := shadowToday, shadowBinary
	t.Cleanup(func() { shadowToday, shadowBinary = origToday, origBinary })
	yaml := checks.AtomByID("fleet:check-yaml")
	vec := []checks.Verdict{checks.VerdictOf(yaml, 0, "")}
	shadowToday = func(context.Context, *FoundryTools, string, string, string) ([]checks.Verdict, error) {
		return vec, nil
	}
	shadowBinary = func(context.Context, *FoundryTools, string, string) (string, error) {
		return `[{"atom":"fleet:check-yaml","stage":"precommit","lane":"any","state":0,"result":"pass","logs":null}]`, nil
	}
	got := defaultShadow(soon(t), &FoundryTools{}, "", "b")
	if !strings.Contains(got, "shadow atoms (chains voted): 1 compared") {
		t.Errorf("report %q", got)
	}
}
