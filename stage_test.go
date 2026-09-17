package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// Check runs the commit stage's atoms and nothing else, settles on the worst,
// and names what it found no surface for.
func TestCheckRunsTheCommitStageAndSettlesItsWorst(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"go","vet"`, 1)
	m := &FoundryTools{Source: dag.Directory()}
	res, err := m.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	worst := 0
	for _, a := range res.Atoms {
		worst = max(worst, a.State)
	}
	if res.Stage != "check" || res.State != worst || res.State == 0 {
		t.Errorf("stage %q state %d, worst atom %d: the stage settles on its worst atom", res.Stage, res.State, worst)
	}
	commit := map[string]bool{}
	for _, a := range checks.AtomsForStage(checks.StagePrecommit) {
		commit[a.ID] = true
	}
	seen := 0
	for _, a := range append(append([]AtomResult{}, res.Atoms...), res.Omitted...) {
		if !commit[a.Atom] {
			t.Errorf("%s is not a commit-stage atom", a.Atom)
		}
		seen++
	}
	if seen != len(commit) {
		t.Errorf("%d atoms answered, the commit stage has %d", seen, len(commit))
	}
	languageStarted := false
	for _, a := range res.Atoms {
		if a.Group == checks.GroupLanguage {
			languageStarted = true
		} else if languageStarted {
			t.Errorf("%s: the basic fanout comes before the language fanout", a.Atom)
		}
		if a.Atom == "go:vet" && (a.State != 1 || a.Result != "findings") {
			t.Errorf("go:vet carries its finding: %+v", a)
		}
	}
	if !strings.Contains(res.Log, "── go:vet · findings ──") {
		t.Errorf("the log names go:vet's finding:\n%s", res.Log)
	}
	if !strings.Contains(","+strings.Join(res.Lanes, ",")+",", ",go,") {
		t.Errorf("lanes %v: the tree carries go", res.Lanes)
	}
}

// Exit ends on the stage's state, carrying the log to the verdict exec.
func TestStageResultExitEndsOnItsState(t *testing.T) {
	engine.reset()
	s := &StageResult{Stage: "check", State: 1, Log: "go:vet findings"}
	_ = s.Exit(context.Background())
	if engine.chain(`"/usr/local/bin/verdict","1","go:vet findings"`) == "" {
		t.Errorf("no verdict exec for state 1 with the log:\n%s", strings.Join(engine.chains(), "\n"))
	}
}

// Push runs the push stage's atoms IN SEQUENCE, stops at the first that found
// something, names what it never reached — and mutation, running beside the
// sequence, still answers.
func TestPushStopsTheSequenceAtTheFirstRedAndStillRunsMutation(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"staticcheck"`, 1)
	m := &FoundryTools{Source: dag.Directory()}
	res, err := m.Push(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Stage != "push" || res.State != 1 {
		t.Errorf("stage %q state %d: the stage settles on the atom that stopped it", res.Stage, res.State)
	}
	ran := map[string]bool{}
	for _, a := range res.Atoms {
		ran[a.Atom] = true
	}
	if !ran["go:staticcheck"] {
		t.Errorf("the sequence ran up to its red: %+v", res.Atoms)
	}
	for _, after := range []string{"go:govulncheck", "go:build", "go:test-race"} {
		if ran[after] {
			t.Errorf("%s ran after the sequence stopped", after)
		}
	}
	if !strings.Contains(","+strings.Join(res.Unreached, ",")+",", ",go:test-race,") {
		t.Errorf("unreached %v: it names the atoms it never got to", res.Unreached)
	}
	if !strings.Contains(res.Log, "── not reached: the stage stopped before them ──") {
		t.Errorf("the log says what it did not look at:\n%s", res.Log)
	}
	// Mutation is its own lane and is not part of the sequence: it answers
	// even though the sequence stopped.
	if !ran["go:mutation"] {
		t.Errorf("mutation runs beside the sequence: %+v", res.Atoms)
	}
}

// Every push-stage atom is accounted for: the ones the sequence reached, the
// ones it never got to, and the mutation lane beside it. Nothing falls out.
func TestPushAccountsForEveryAtomItDidNotRun(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	m := &FoundryTools{Source: dag.Directory()}
	res, err := m.Push(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	answered := map[string]bool{}
	for _, a := range append(append([]AtomResult{}, res.Atoms...), res.Omitted...) {
		answered[a.Atom] = true
	}
	for _, id := range res.Unreached {
		answered[id] = true
	}
	sequence, _ := checks.Subsumed(checks.AtomsForStage(checks.StagePrepush))
	for _, a := range append(sequence, checks.AtomsForStage(checks.StageMutation)...) {
		if !answered[a.ID] {
			t.Errorf("%s is neither answered nor named unreached", a.ID)
		}
	}
	// The unreached are exactly the tail of the sequence after the atom that
	// stopped it — never an arbitrary set. (The stage's log orders basic
	// before language; the SEQUENCE's order is the catalogue's, so the atom
	// that stopped it is the last catalogue row that answered.)
	var stopped string
	var want []string
	for i, a := range sequence {
		if !answered[a.ID] || slices.Contains(res.Unreached, a.ID) {
			continue
		}
		stopped = a.ID
		want = nil
		for _, rest := range sequence[i+1:] {
			want = append(want, rest.ID)
		}
	}
	if strings.Join(res.Unreached, ",") != strings.Join(want, ",") {
		t.Errorf("unreached %v, want the tail after %s: %v", res.Unreached, stopped, want)
	}
}

// An atom with no runner is an authoring error, not a verdict — and a stage
// that hits one answers the error rather than a stage result that quietly
// omits it. Both halves of Push carry it: the sequence's and the errgroup's.
func TestPushAnswersTheErrorWhenAnAtomCannotBeDispatched(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	fn, ok := registry["go:staticcheck"]
	if !ok {
		t.Fatal("go:staticcheck has no runner to remove")
	}
	delete(registry, "go:staticcheck")
	defer func() { registry["go:staticcheck"] = fn }()

	res, err := (&FoundryTools{Source: dag.Directory()}).Push(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "go:staticcheck") {
		t.Errorf("err %v, result %+v: an undispatchable atom is the stage's error", err, res)
	}
	if res != nil {
		t.Errorf("a stage that errored answers no result: %+v", res)
	}

	// The commit stage answers the same way, through its own fanout.
	vet := registry["go:vet"]
	delete(registry, "go:vet")
	defer func() { registry["go:vet"] = vet }()
	res, err = (&FoundryTools{Source: dag.Directory()}).Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "go:vet") || res != nil {
		t.Errorf("check: err %v, result %+v", err, res)
	}
}
