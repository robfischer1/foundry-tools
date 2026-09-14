package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

const gatePin = "git.notusmi.com/rob/foundry-tools@deadbeef"

const cleanVector = `[{"atom":"go:vet","stage":"precommit","lane":"go","state":0,"result":"pass"}]`

const redVector = `[{"atom":"go:vet","stage":"precommit","lane":"go","state":0,"result":"pass"},` +
	`{"atom":"go:test-race","stage":"prepush","lane":"go","state":1,"result":"findings","reason":"FAIL TestX"}]`

// gateOn is the module constructed on a fetched commit whose tree is fakeTree,
// grading to the vector a test hands it, with the attestation's back-off
// flattened.
func gateOn(t *testing.T, vector string) *FoundryTools {
	t.Helper()
	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module x\n"})
	engine.stdout(`"rev-parse","HEAD^{tree}"`, fakeTree+"\n")
	graded, backoff, budget := gateVector, attestBackoff, attestBudget
	gateVector = func(context.Context, *FoundryTools, string, string) (string, error) { return vector, nil }
	attestBackoff = 0
	t.Cleanup(func() { gateVector, attestBackoff, attestBudget = graded, backoff, budget })
	return &FoundryTools{Source: dag.Directory(), Repo: "http://door:8215/rob/ares.git", Sha: buildSha}
}

// runGate runs the gate lane as the door does: with the pod's socket.
func runGate(t *testing.T, m *FoundryTools, stage string, withSocket bool) {
	t.Helper()
	gateOnTree(t, m, fakeTree, stage, withSocket)
}

func gateOnTree(t *testing.T, m *FoundryTools, tree, stage string, withSocket bool) {
	t.Helper()
	spire := dag.LoadSocketFromID("spire-agent-socket")
	if !withSocket {
		spire = nil
	}
	if err := m.Gate(context.Background(), tree, gatePin, "base-sha", stage, spire,
		"https://hades:8102", "spiffe://notusmi.com/star/hades"); err != nil {
		t.Fatalf("gate: %v", err)
	}
}

// attempted answers whether the attestation's nth attempt reached the engine.
func attempted(n string) bool {
	return engine.chain(`"GATE_ATTEST_ATTEMPT"`, `value:"`+n+`"`) != ""
}

// A clean vector is attested as the pod — hadescall on the socket, told to
// stamp its own SVID into the receipt — and the lane settles clean.
func TestACleanGateIsAttestedAsThePodAndSettlesClean(t *testing.T) {
	m := gateOn(t, cleanVector)
	engine.stdout(`"tartarus_attest_emit"`, "HTTP 200\n{\"ok\":true}")
	runGate(t, m, "", true)
	wantCalls(t, engine.chain(`"tartarus_attest_emit"`),
		[]string{"withUnixSocket", `"/run/spire/agent.sock"`, `owner:"65532:65532"`},
		[]string{"withEnvVariable", `"HADESCALL_SVID_FIELD"`, `"svid"`},
		[]string{"withExec", `"/usr/local/bin/hadescall"`, `"tartarus_attest_emit"`, "module_pin", "foundry-tools@deadbeef", "ca-gate/ares:" + fakeTree},
	)
	if attempted("2") {
		t.Fatal("a receipt that landed was sent again")
	}
	settledOn(t, "0", "1 atom(s): 1 pass, 0 findings, 0 cannot-run")
}

// Findings are attested too, and settle as findings naming the red atom.
func TestAGateWithFindingsIsAttestedAndSettlesFindings(t *testing.T) {
	m := gateOn(t, redVector)
	engine.stdout(`"tartarus_attest_emit"`, "HTTP 200\n{}")
	runGate(t, m, "", true)
	if engine.chain(`"tartarus_attest_emit"`) == "" {
		t.Fatal("a red vector was not attested")
	}
	settledOn(t, "1", "go:test-race state=1 FAIL TestX")
}

// A fetched commit whose tree is not the one the door named is never graded;
// a cannot-run vector saying so is attested in its place.
func TestAGateOnTheWrongTreeAttestsACannotRunVector(t *testing.T) {
	m := gateOn(t, cleanVector)
	engine.stdout(`"rev-parse","HEAD^{tree}"`, "0000000000000000000000000000000000000000\n")
	gateVector = func(context.Context, *FoundryTools, string, string) (string, error) {
		t.Fatal("a tree the door did not name was graded")
		return "", nil
	}
	engine.stdout(`"tartarus_attest_emit"`, "HTTP 200\n{}")
	runGate(t, m, "", true)
	wantCalls(t, engine.chain(`"tartarus_attest_emit"`), []string{"withExec", "cannot-run", "refusing to grade"})
	settledOn(t, "2", "refusing to grade a tree the receipt would not describe")
}

// A tree the engine could not read is never graded.
func TestATreeTheEngineCannotReadIsCannotRun(t *testing.T) {
	m := gateOn(t, cleanVector)
	engine.exitCode(`"rev-parse","HEAD^{tree}"`, 128)
	engine.stdout(`"rev-parse","HEAD^{tree}"`, "fatal: bad revision\n")
	engine.stdout(`"tartarus_attest_emit"`, "HTTP 200\n{}")
	runGate(t, m, "", true)
	settledOn(t, "2", "could not fetch ares")
}

// A module that could not produce a vector is a cannot-run.
func TestAModuleThatCannotGradeIsCannotRun(t *testing.T) {
	m := gateOn(t, cleanVector)
	gateVector = func(context.Context, *FoundryTools, string, string) (string, error) {
		return "", errors.New("the engine went away")
	}
	engine.stdout(`"tartarus_attest_emit"`, "HTTP 200\n{}")
	runGate(t, m, "", true)
	settledOn(t, "2", "could not produce a vector: the engine went away")
}

// Output that is not a vector is a cannot-run, and nothing is not a pass.
func TestAVectorThatIsNotAVectorOrIsEmptyIsCannotRun(t *testing.T) {
	m := gateOn(t, "garbage")
	engine.stdout(`"tartarus_attest_emit"`, "HTTP 200\n{}")
	runGate(t, m, "", true)
	settledOn(t, "2", "not a verdict vector")

	m = gateOn(t, "[]")
	engine.stdout(`"tartarus_attest_emit"`, "HTTP 200\n{}")
	runGate(t, m, "", true)
	settledOn(t, "2", "EMPTY vector")
}

// With no tree to key a receipt on, nothing is attested and the lane could
// not run.
func TestNoTreeMeansNoReceipt(t *testing.T) {
	m := gateOn(t, cleanVector)
	gateOnTree(t, m, "", "", true)
	if engine.chain(`"tartarus_attest_emit"`) != "" {
		t.Fatal("a receipt with no tree was attested")
	}
	settledOn(t, "2", "no tree to key a receipt on")
}

// A receipt hades refuses never lands, so the lane could not run — whatever
// the vector said — and it is not asked again.
func TestAReceiptThatDoesNotLandIsCouldNotRun(t *testing.T) {
	m := gateOn(t, cleanVector)
	engine.stdout(`"tartarus_attest_emit"`, "HTTP 403\n{\"detail\":\"not granted\"}")
	runGate(t, m, "", true)
	if attempted("2") {
		t.Fatal("a refused receipt was sent again")
	}
	settledOn(t, "2", "NOT attested")
	settledOn(t, "2", "POLICY refused")
}

// A hold-down is waited out: the receipt is asked again and lands.
func TestAHeldDownReceiptIsWaitedOut(t *testing.T) {
	m := gateOn(t, cleanVector)
	engine.stdout(`"tartarus_attest_emit"`, "HTTP 503\nproduce hold-down: retry")
	engine.stdout(`value:"2"`, "HTTP 200\n{}")
	runGate(t, m, "", true)
	if !attempted("2") || attempted("3") {
		t.Fatalf("want exactly two attempts:\n%v", engine.chains())
	}
	settledOn(t, "0", "attested for tree")
}

// A hold-down that never lifts runs out of attempts and is could-not-run.
func TestAHoldDownThatNeverLiftsIsCouldNotRun(t *testing.T) {
	m := gateOn(t, cleanVector)
	engine.stdout(`"tartarus_attest_emit"`, "HTTP 503\nproduce hold-down: retry")
	runGate(t, m, "", true)
	if !attempted("6") || attempted("7") {
		t.Fatal("want exactly six attempts")
	}
	settledOn(t, "2", "held down")
}

// The back-off spends a budget: once it is gone no attempt is made, however
// many remain.
func TestASpentBudgetStopsTheHoldDownRetries(t *testing.T) {
	m := gateOn(t, cleanVector)
	attestBackoff, attestBudget = time.Millisecond, 2*time.Millisecond
	engine.stdout(`"tartarus_attest_emit"`, "HTTP 503\nproduce hold-down: retry")
	runGate(t, m, "", true)
	// 1ms then the last 1ms of the budget: the third attempt is the last.
	if !attempted("3") || attempted("4") {
		t.Fatalf("want exactly three attempts inside a 2ms budget:\n%v", engine.chains())
	}
	settledOn(t, "2", "held down")
}

// hadescall that could not ask is retried like a hold-down, and never lands.
func TestAHadescallThatCannotAskIsRetriedThenCouldNotRun(t *testing.T) {
	m := gateOn(t, cleanVector)
	engine.exitCode(`"tartarus_attest_emit"`, 2)
	engine.stdout(`"tartarus_attest_emit"`, "")
	engine.stderr(`"tartarus_attest_emit"`, "hadescall tartarus_attest_emit: no identity from unix:///run/spire/agent.sock within 2m0s")
	runGate(t, m, "", true)
	if !attempted("6") {
		t.Fatal("an ask that could not be made was not retried")
	}
	settledOn(t, "2", "could not ask hades")
}

// hadescall that did not run at all is a could-not-run at once.
func TestAHadescallThatDidNotRunIsCouldNotRun(t *testing.T) {
	m := gateOn(t, cleanVector)
	engine.failLeaf(`"tartarus_attest_emit"`, "exitCode", "the engine went away")
	runGate(t, m, "", true)
	if attempted("2") {
		t.Fatal("an engine error was retried")
	}
	settledOn(t, "2", "hadescall did not run")
}

// An answer that is not hadescall's shape is a could-not-run.
func TestAnAttestAnswerWithNoStatusLineIsCouldNotRun(t *testing.T) {
	m := gateOn(t, cleanVector)
	engine.stdout(`"tartarus_attest_emit"`, "garbage")
	runGate(t, m, "", true)
	settledOn(t, "2", "no status line")
}

// The mutation lane settles from its exit and attests nothing.
func TestTheMutationLaneSettlesFromItsExitAndAttestsNothing(t *testing.T) {
	m := gateOn(t, redVector)
	var stage string
	gateVector = func(_ context.Context, _ *FoundryTools, s, _ string) (string, error) {
		stage = s
		return redVector, nil
	}
	runGate(t, m, "mutation", false)
	if stage != "mutation" {
		t.Fatalf("the vector was graded at stage %q", stage)
	}
	if engine.chain(`"tartarus_attest_emit"`) != "" {
		t.Fatal("the mutation lane attested")
	}
	settledOn(t, "1", "mutation: 2 atom(s)")
	settledOn(t, "1", "no receipt by design")
}

// A tree the engine did not fetch is a cannot-run, attested like any other.
func TestAGateOnAnUnfetchedTreeIsCannotRun(t *testing.T) {
	gateOn(t, cleanVector)
	engine.stdout(`"tartarus_attest_emit"`, "HTTP 200\n{}")
	runGate(t, &FoundryTools{Source: dag.Directory()}, "", true)
	settledOn(t, "2", "construct the module with --repo and --sha")
}
