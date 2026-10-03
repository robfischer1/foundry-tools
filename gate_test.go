package main

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
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

// runGate runs the gate lane as the door does, and answers its run record.
func runGate(t *testing.T, m *FoundryTools, stage string) string {
	t.Helper()
	return gateOnTree(t, m, fakeTree, stage)
}

func gateOnTree(t *testing.T, m *FoundryTools, tree, stage string) string {
	t.Helper()
	rec, err := m.Gate(context.Background(), tree, gatePin, "base-sha", stage)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	return rec
}

// recordedOn is settledOn's replacement for the lanes that now RETURN their
// verdict instead of exiting with it: the state the record states, and a
// phrase the run's own evidence must contain.
//
// It reads the record the way the door does — one sentinel-marked line — so a
// change that broke the wire shape fails here rather than in production.
func recordedOn(t *testing.T, record, state, phrase string) {
	t.Helper()
	if strings.Contains(record, "\n") {
		t.Fatalf("the record must be ONE line; the door scans line by line: %q", record[:min(len(record), 120)])
	}
	rest, ok := strings.CutPrefix(record, "ourea-run-record/1 ")
	if !ok {
		t.Fatalf("the record must carry the sentinel the door reads: %q", record[:min(len(record), 80)])
	}
	var got StageResult
	if err := json.Unmarshal([]byte(rest), &got); err != nil {
		t.Fatalf("the record is not a StageResult: %v", err)
	}
	if want := state; strconv.Itoa(got.State) != want {
		t.Fatalf("record state = %d, want %s (log: %s)", got.State, want, got.Log)
	}
	if phrase != "" && !strings.Contains(evidence(got), phrase) {
		t.Fatalf("the record's evidence must contain %q; got: %s", phrase, evidence(got))
	}
}

// evidence is everything a reader could look at for a phrase: the rendered
// log, plus each atom's reason and its own lines.
func evidence(s StageResult) string {
	var b strings.Builder
	b.WriteString(s.Log)
	for _, a := range append(append([]AtomResult{}, s.Atoms...), s.Omitted...) {
		b.WriteString("\n" + a.Reason + "\n" + strings.Join(a.Logs, "\n"))
	}
	return b.String()
}

// A clean vector records clean, and nothing is attested: the gate's RECORD is
// its whole answer now (CA F16, then F3).
func TestACleanGateRecordsCleanAndAttestsNothing(t *testing.T) {
	m := gateOn(t, cleanVector)
	rec := runGate(t, m, "")
	if engine.chain(`"tartarus_attest_emit"`) != "" || engine.chain(`"/usr/local/bin/hadescall"`) != "" {
		t.Fatal("the gate attested")
	}
	recordedOn(t, rec, "0", "go:vet")
}

// THE GATE NO LONGER SETTLES WITH ITS EXIT. This is the flip, and it is the
// reason the door's guard had to land first: from here the container exits 0
// whatever the tree contained, so a door still reading the number would call
// every finding a pass.
func TestTheGateNoLongerExitsWithItsVerdict(t *testing.T) {
	m := gateOn(t, redVector)
	rec := runGate(t, m, "")
	if chain := engine.chain(`"/usr/local/bin/verdict"`); chain != "" {
		t.Fatalf("the gate still settled through an exec: %s", chain)
	}
	recordedOn(t, rec, "1", "FAIL TestX")
}

// Findings are recorded as findings, naming the red atom.
func TestAGateWithFindingsRecordsFindings(t *testing.T) {
	m := gateOn(t, redVector)
	recordedOn(t, runGate(t, m, ""), "1", "FAIL TestX")
}

// A fetched commit whose tree is not the one the door named is never graded;
// the lane records cannot-run saying so.
func TestAGateOnTheWrongTreeIsCannotRun(t *testing.T) {
	m := gateOn(t, cleanVector)
	engine.stdout(`"rev-parse","HEAD^{tree}"`, "0000000000000000000000000000000000000000\n")
	gateVector = func(context.Context, *FoundryTools, string, string) (string, error) {
		t.Fatal("a tree the door did not name was graded")
		return "", nil
	}
	recordedOn(t, runGate(t, m, ""), "2", "refusing to grade a tree the settle would not describe")
}

// A tree the engine could not read is never graded.
func TestATreeTheEngineCannotReadIsCannotRun(t *testing.T) {
	m := gateOn(t, cleanVector)
	engine.exitCode(`"rev-parse","HEAD^{tree}"`, 128)
	engine.stdout(`"rev-parse","HEAD^{tree}"`, "fatal: bad revision\n")
	recordedOn(t, runGate(t, m, ""), "2", "could not fetch ares")
}

// A module that could not produce a vector is a cannot-run.
func TestAModuleThatCannotGradeIsCannotRun(t *testing.T) {
	m := gateOn(t, cleanVector)
	gateVector = func(context.Context, *FoundryTools, string, string) (string, error) {
		return "", errors.New("the engine went away")
	}
	recordedOn(t, runGate(t, m, ""), "2", "could not produce a vector: the engine went away")
}

// Output that is not a vector is a cannot-run, and nothing is not a pass.
func TestAVectorThatIsNotAVectorOrIsEmptyIsCannotRun(t *testing.T) {
	m := gateOn(t, "garbage")
	recordedOn(t, runGate(t, m, ""), "2", "not a verdict vector")

	m = gateOn(t, "[]")
	recordedOn(t, runGate(t, m, ""), "2", "EMPTY vector")
}

// The mutation lane records from the same function at its own stage.
func TestTheMutationLaneRecordsAndAttestsNothing(t *testing.T) {
	m := gateOn(t, redVector)
	var stage string
	gateVector = func(_ context.Context, _ *FoundryTools, s, _ string) (string, error) {
		stage = s
		return redVector, nil
	}
	rec := runGate(t, m, "mutation")
	if stage != "mutation" {
		t.Fatalf("the vector was graded at stage %q", stage)
	}
	recordedOn(t, rec, "1", "FAIL TestX")
	var got StageResult
	_ = json.Unmarshal([]byte(strings.TrimPrefix(rec, "ourea-run-record/1 ")), &got)
	if got.Stage != "mutation" {
		t.Fatalf("the record must name its own lane, got %q", got.Stage)
	}
}

// A tree the engine did not fetch is a cannot-run.
func TestAGateOnAnUnfetchedTreeIsCannotRun(t *testing.T) {
	gateOn(t, cleanVector)
	recordedOn(t, runGate(t, &FoundryTools{Source: dag.Directory()}, ""), "2", "construct the module with --repo and --sha")
}

// EXACTLY ONE RECORD PER RUN. Two would make the door refuse both rather than
// choose between them, which is correct of the door and useless to everyone.
func TestExactlyOneRecordPerRun(t *testing.T) {
	m := gateOn(t, redVector)
	rec := runGate(t, m, "")
	if n := strings.Count(rec, "ourea-run-record/1"); n != 1 {
		t.Fatalf("the run emitted %d records, want exactly 1", n)
	}
}

// THE TWO TRANSPORTS GRADE THE SAME TREE THE SAME WAY. A lane that said one
// thing on stdout and another on a volume would be worse than the interleaving
// bug the volume replaces, so both entry points go through gateStage and this
// asserts they still do — including for a red vector, where a disagreement
// would actually cost somebody a verdict.
func TestTheFileAndTheLineAreTheSameRecord(t *testing.T) {
	for _, vector := range []string{cleanVector, redVector} {
		m := gateOn(t, vector)
		line := runGate(t, m, "")

		engine.reset()
		engine.withTree(map[string]string{"go.mod": "module x\n"})
		engine.stdout(`"rev-parse","HEAD^{tree}"`, fakeTree+"\n")
		// No record token: this case proves the FILE, and a nil token is also
		// the shape every lane has until its Call declares one.
		f, err := m.GateFile(context.Background(), fakeTree, gatePin, "base-sha", "", nil)
		if err != nil {
			t.Fatalf("gate-file: %v", err)
		}
		// The SDK is lazy — building a File issues no query — so the chain only
		// exists once something asks for the contents.
		_, _ = f.Contents(context.Background())
		chain := engine.chain("withNewFile", RecordFileName)
		if chain == "" {
			t.Fatalf("gate-file wrote no %s; the engine saw:\n%s",
				RecordFileName, strings.Join(engine.chains(), "\n"))
		}
		// The WHOLE record, not a prefix of it: the door parses one JSON object
		// off the sentinel, so a file holding less is a file that grades
		// differently. Compared through the chain, because that is where the
		// bytes actually go.
		if !strings.Contains(chain, quotedInto(line)) {
			t.Errorf("the file is not the line the door reads.\nline:  %s\nchain: %s", line, chain)
		}
	}
}

// quotedInto answers the line as it appears inside the recorded GraphQL chain,
// where it travels as a JSON string argument.
func quotedInto(line string) string {
	b, err := json.Marshal(line)
	if err != nil {
		panic(err)
	}
	return string(b[1 : len(b)-1])
}

// AND THE MUTATION LANE GOES THROUGH THE SAME DOOR. --stage=mutation is how the
// lane is selected on both transports; a GateFile that dropped it would hand the
// door a record labelled `gate` out of the mutation Job, and the coordinate's
// fold would attribute the verdict to the wrong lane.
func TestGateFileCarriesTheStageItWasAsked(t *testing.T) {
	m := gateOn(t, cleanVector)
	f, err := m.GateFile(context.Background(), fakeTree, gatePin, "base-sha", "mutation", nil)
	if err != nil {
		t.Fatalf("gate-file: %v", err)
	}
	_, _ = f.Contents(context.Background())
	chain := engine.chain("withNewFile", RecordFileName)
	if !strings.Contains(chain, `\"Stage\":\"mutation\"`) {
		t.Errorf("the mutation lane's file is not labelled mutation:\n%s", chain)
	}
}

// THE COMMIT HOOK'S CHECK IS ITS OWN LANE. A precommit run is labelled
// `check`, so the door never folds a check's verdict in as a gate's; every
// other stage keeps the label it had.
func TestLaneOfLabelsEachStage(t *testing.T) {
	for stage, want := range map[string]string{
		"": "gate", "prepush": "gate", "sweep": "gate",
		"mutation": "mutation", "precommit": "check", "mutation-bg": "mutation-bg",
	} {
		if got := laneOf(stage); got != want {
			t.Errorf("laneOf(%q) = %q, want %q", stage, got, want)
		}
	}
}

// A gate-file at --stage=precommit grades the precommit atoms only and hands
// the door a record labelled `check` — and a run that could not grade says
// so under `check` too (CannotRunVector names the lane as its atom).
func TestGateFileAtPrecommitIsTheCheckLane(t *testing.T) {
	m := gateOn(t, cleanVector)
	var stage string
	gateVector = func(_ context.Context, _ *FoundryTools, s, _ string) (string, error) {
		stage = s
		return cleanVector, nil
	}
	f, err := m.GateFile(context.Background(), fakeTree, gatePin, "", "precommit", nil)
	if err != nil {
		t.Fatalf("gate-file: %v", err)
	}
	_, _ = f.Contents(context.Background())
	if stage != "precommit" {
		t.Fatalf("the vector was graded at stage %q, want precommit", stage)
	}
	if chain := engine.chain("withNewFile", RecordFileName); !strings.Contains(chain, `\"Stage\":\"check\"`) {
		t.Errorf("the precommit file is not labelled check:\n%s", chain)
	}
	rec := gateOnTree(t, m, "another-tree", "precommit")
	recordedOn(t, rec, "2", "refusing to grade")
	var got StageResult
	_ = json.Unmarshal([]byte(strings.TrimPrefix(rec, "ourea-run-record/1 ")), &got)
	if got.Stage != "check" || len(got.Atoms) != 1 || got.Atoms[0].Atom != "check" {
		t.Fatalf("a check that could not grade must say so as check: %+v", got)
	}
}

// THE BACKGROUND MUTATION LANE GRADES THE MUTATION ATOMS UNDER ITS OWN LABEL:
// the vector is asked at stage mutation, the record says mutation-bg, and a
// run that could not grade says so as mutation-bg at the mutation stage.
func TestTheBackgroundMutationLaneGradesMutationUnderItsOwnName(t *testing.T) {
	m := gateOn(t, redVector)
	var stage string
	gateVector = func(_ context.Context, _ *FoundryTools, s, _ string) (string, error) {
		stage = s
		return redVector, nil
	}
	rec := runGate(t, m, "mutation-bg")
	if stage != "mutation" {
		t.Fatalf("the vector was graded at stage %q, want mutation", stage)
	}
	recordedOn(t, rec, "1", "FAIL TestX")
	var got StageResult
	_ = json.Unmarshal([]byte(strings.TrimPrefix(rec, "ourea-run-record/1 ")), &got)
	if got.Stage != "mutation-bg" {
		t.Fatalf("the record must name the background lane, got %q", got.Stage)
	}
	bad := gateOnTree(t, m, "another-tree", "mutation-bg")
	_ = json.Unmarshal([]byte(strings.TrimPrefix(bad, "ourea-run-record/1 ")), &got)
	if len(got.Atoms) != 1 || got.Atoms[0].Atom != "mutation-bg" || got.Stage != "mutation-bg" {
		t.Fatalf("a background run that could not grade = %+v", got)
	}
	for in, want := range map[string]string{"mutation-bg": "mutation", "mutation": "mutation", "precommit": "precommit", "": ""} {
		if g := gradedStage(in); g != want {
			t.Errorf("gradedStage(%q) = %q, want %q", in, g, want)
		}
	}
}
