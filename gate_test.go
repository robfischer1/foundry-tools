package main

import (
	"context"
	"errors"
	"testing"
)

const gatePin = "git.notusmi.com/rob/foundry-tools@deadbeef"

const cleanVector = `[{"atom":"go:vet","stage":"precommit","lane":"go","state":0,"result":"pass"}]`

const redVector = `[{"atom":"go:vet","stage":"precommit","lane":"go","state":0,"result":"pass"},` +
	`{"atom":"go:test-race","stage":"prepush","lane":"go","state":1,"result":"findings","reason":"FAIL TestX"}]`

// gateOn is the module constructed on a fetched commit whose tree is fakeTree,
// grading to the vector a test hands it.
func gateOn(t *testing.T, vector string) *FoundryTools {
	t.Helper()
	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module x\n"})
	engine.stdout(`"rev-parse","HEAD^{tree}"`, fakeTree+"\n")
	graded := gateVector
	gateVector = func(context.Context, *FoundryTools, string, string) (string, error) { return vector, nil }
	t.Cleanup(func() { gateVector = graded })
	return &FoundryTools{Source: dag.Directory(), Repo: "http://door:8215/rob/ares.git", Sha: buildSha}
}

// runGate runs the gate lane as the door does.
func runGate(t *testing.T, m *FoundryTools, stage string) {
	t.Helper()
	gateOnTree(t, m, fakeTree, stage)
}

func gateOnTree(t *testing.T, m *FoundryTools, tree, stage string) {
	t.Helper()
	if err := m.Gate(context.Background(), tree, gatePin, "base-sha", stage); err != nil {
		t.Fatalf("gate: %v", err)
	}
}

// A clean vector settles clean, under the pin the door named — and nothing
// is attested: the gate's exit is its whole answer (CA F16).
func TestACleanGateSettlesCleanAndAttestsNothing(t *testing.T) {
	m := gateOn(t, cleanVector)
	runGate(t, m, "")
	if engine.chain(`"tartarus_attest_emit"`) != "" || engine.chain(`"/usr/local/bin/hadescall"`) != "" {
		t.Fatal("the gate attested")
	}
	settledOn(t, "0", "1 atom(s): 1 pass, 0 findings, 0 cannot-run")
	settledOn(t, "0", "settled from its exit under "+gatePin)
}

// Findings settle as findings naming the red atom.
func TestAGateWithFindingsSettlesFindings(t *testing.T) {
	m := gateOn(t, redVector)
	runGate(t, m, "")
	settledOn(t, "1", "go:test-race state=1 FAIL TestX")
}

// A fetched commit whose tree is not the one the door named is never graded;
// the lane settles cannot-run saying so.
func TestAGateOnTheWrongTreeIsCannotRun(t *testing.T) {
	m := gateOn(t, cleanVector)
	engine.stdout(`"rev-parse","HEAD^{tree}"`, "0000000000000000000000000000000000000000\n")
	gateVector = func(context.Context, *FoundryTools, string, string) (string, error) {
		t.Fatal("a tree the door did not name was graded")
		return "", nil
	}
	runGate(t, m, "")
	settledOn(t, "2", "refusing to grade a tree the settle would not describe")
}

// A tree the engine could not read is never graded.
func TestATreeTheEngineCannotReadIsCannotRun(t *testing.T) {
	m := gateOn(t, cleanVector)
	engine.exitCode(`"rev-parse","HEAD^{tree}"`, 128)
	engine.stdout(`"rev-parse","HEAD^{tree}"`, "fatal: bad revision\n")
	runGate(t, m, "")
	settledOn(t, "2", "could not fetch ares")
}

// A module that could not produce a vector is a cannot-run.
func TestAModuleThatCannotGradeIsCannotRun(t *testing.T) {
	m := gateOn(t, cleanVector)
	gateVector = func(context.Context, *FoundryTools, string, string) (string, error) {
		return "", errors.New("the engine went away")
	}
	runGate(t, m, "")
	settledOn(t, "2", "could not produce a vector: the engine went away")
}

// Output that is not a vector is a cannot-run, and nothing is not a pass.
func TestAVectorThatIsNotAVectorOrIsEmptyIsCannotRun(t *testing.T) {
	m := gateOn(t, "garbage")
	runGate(t, m, "")
	settledOn(t, "2", "not a verdict vector")

	m = gateOn(t, "[]")
	runGate(t, m, "")
	settledOn(t, "2", "EMPTY vector")
}

// The mutation lane settles from its exit and attests nothing.
func TestTheMutationLaneSettlesFromItsExitAndAttestsNothing(t *testing.T) {
	m := gateOn(t, redVector)
	var stage string
	gateVector = func(_ context.Context, _ *FoundryTools, s, _ string) (string, error) {
		stage = s
		return redVector, nil
	}
	runGate(t, m, "mutation")
	if stage != "mutation" {
		t.Fatalf("the vector was graded at stage %q", stage)
	}
	settledOn(t, "1", "mutation: 2 atom(s)")
	settledOn(t, "1", "settled from its exit under "+gatePin)
}

// A tree the engine did not fetch is a cannot-run.
func TestAGateOnAnUnfetchedTreeIsCannotRun(t *testing.T) {
	gateOn(t, cleanVector)
	runGate(t, &FoundryTools{Source: dag.Directory()}, "")
	settledOn(t, "2", "construct the module with --repo and --sha")
}
