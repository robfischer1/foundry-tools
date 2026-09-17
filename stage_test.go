package main

import (
	"context"
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
