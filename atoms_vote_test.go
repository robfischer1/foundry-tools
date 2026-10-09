package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// votedMark opens the reason of every verdict the stubbed binary answers, so a
// test reads which side a verdict came from.
const votedMark = "voted by the binary: "

// binaryVector is the vector the stubbed binary prints for a stage: one passing
// verdict per atom it registers there, in the registry's order.
func binaryVector(t *testing.T, stage string) string {
	t.Helper()
	var vs []checks.Verdict
	for _, id := range atoms.StageIDs(stage) {
		v := checks.VerdictOf(checks.AtomByID(id), 0, "")
		v.Reason, v.Logs = votedMark+id, []string{"a line"}
		vs = append(vs, v)
	}
	raw, err := json.Marshal(vs)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// casting sets who votes and stubs the binary's run, restoring both. It answers
// the stages the binary was run for, in order.
func casting(t *testing.T, side voter) *[]string {
	t.Helper()
	origSide, origCast := atomsVoter, castBinary
	t.Cleanup(func() { atomsVoter, castBinary = origSide, origCast })
	atomsVoter = side
	var ran []string
	castBinary = func(_ context.Context, _ *FoundryTools, stage, _ string) (string, error) {
		ran = append(ran, stage)
		return binaryVector(t, stage), nil
	}
	return &ran
}

// soon is a context that ends: a lane whose binary never answers fails a test
// in seconds instead of hanging it.
func soon(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// chainsAreOff makes the chain of every atom the binary registers a test
// failure: a binary-voted lane must not run them, whatever it does with the
// answers.
func chainsAreOff(t *testing.T) {
	t.Helper()
	for _, a := range atoms.Builtin() {
		orig := registry[a.ID]
		registry[a.ID] = func(ctx context.Context, r *run) checks.Verdict {
			t.Errorf("the chain of %s ran though the binary votes", a.ID)
			return orig(ctx, r)
		}
		t.Cleanup(func() { registry[a.ID] = orig })
	}
}

// A tree with no language in it: the lane atoms stand down and the run-anywhere
// atoms are the whole of the vector.
var bareTree = map[string]string{"README.md": "hello\n"}

func bareModule() *FoundryTools { return &FoundryTools{Source: dag.Directory()} }

// THE CUTOVER SHIPS ON, and the switch is one constant.
func TestTheBinaryVotesByDefault(t *testing.T) {
	if defaultVoter != voterBinary {
		t.Errorf("defaultVoter is %q: the cutover is not on", defaultVoter)
	}
}

// (a) FOR EVERY ATOM THE BINARY REGISTERS, THE VECTOR'S VERDICT IS THE BINARY'S;
// the rest keep their chains. The binary runs once for the lane, whatever the
// number of atoms, and its verdicts sit where the chains' would have, in
// catalogue order.
func TestEveryAtomTheBinaryRegistersVotesFromTheBinary(t *testing.T) {
	ran := casting(t, voterBinary)
	registered := map[string]bool{}
	for _, a := range atoms.Builtin() {
		registered[a.ID] = true
	}
	chainsAreOff(t)
	seen := map[string]bool{}
	for _, stage := range []string{"", checks.StagePrecommit, checks.StagePrepush, checks.StageOrbit} {
		t.Run("stage "+stage, func(t *testing.T) {
			*ran = nil
			engine.reset()
			engine.withTree(bareTree)
			vs, err := bareModule().vector(soon(t), stage, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if len(*ran) != 1 || (*ran)[0] != stage {
				t.Errorf("the binary ran for stages %q, want exactly [%q]", *ran, stage)
			}
			var order []string
			for _, v := range vs {
				if v.Atom == "" {
					t.Errorf("an atom of the vector was never answered: %+v", v)
				}
				fromBinary := strings.HasPrefix(v.Reason, votedMark)
				if fromBinary != registered[v.Atom] {
					t.Errorf("%s: voted by the binary is %v, registered is %v (%q)", v.Atom, fromBinary, registered[v.Atom], v.Reason)
				}
				if fromBinary {
					seen[v.Atom] = true
					order = append(order, v.Atom)
				}
			}
			var want []string
			for _, a := range checks.AtomsForStage(stage) {
				if registered[a.ID] {
					want = append(want, a.ID)
				}
			}
			if !slices.Equal(order, want) {
				t.Errorf("the binary's verdicts sit in the order %v, want the catalogue's %v", order, want)
			}
		})
	}
	if len(seen) != len(registered) {
		t.Errorf("%d of the %d registered atoms were seen voting", len(seen), len(registered))
	}
}

// A binary that would not answer, or answered garbage, or left an atom out, is
// that atom's could-not-run - and never its chain's pass.
func TestABinaryThatDoesNotAnswerIsAFailureForEachOfItsAtoms(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		err  error
		want string
	}{
		{"the run failed", "", errors.New("the engine went away"), "the atoms binary did not answer: the engine went away"},
		{"the output is not a vector", "panic: oops", nil, "the atoms binary did not answer: the module's output is not a verdict vector"},
		{"an atom is left out", "[]", nil, "the atoms binary returned no verdict for this atom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			casting(t, voterBinary)
			castBinary = func(context.Context, *FoundryTools, string, string) (string, error) { return tc.out, tc.err }
			engine.reset()
			engine.withTree(bareTree)
			vs, err := bareModule().vector(soon(t), checks.StageOrbit, "", "")
			if err != nil || len(vs) != len(atoms.StageIDs(checks.StageOrbit)) {
				t.Fatalf("vector %v, err %v", vs, err)
			}
			for _, v := range vs {
				if v.State != 2 || v.Result != "cannot-run" || !strings.Contains(v.Reason, tc.want) {
					t.Errorf("%s: %+v", v.Atom, v)
				}
			}
		})
	}
}

// (c) THE ROLLBACK: with the chains voting the binary is not run for a vote, no
// verdict is the binary's, and the shadow is the dry one (no ballot box).
func TestTheRollbackSwitchRestoresTheChainsAsVoters(t *testing.T) {
	ran := casting(t, voterChains)
	engine.reset()
	engine.withTree(bareTree)
	vs, err := bareModule().vector(soon(t), checks.StagePrecommit, "", "")
	if err != nil || len(vs) == 0 {
		t.Fatalf("vector %v, err %v", vs, err)
	}
	if len(*ran) != 0 {
		t.Errorf("the binary ran for a vote though the chains vote: %q", *ran)
	}
	for _, v := range vs {
		if strings.HasPrefix(v.Reason, votedMark) {
			t.Errorf("%s was voted by the binary after the rollback", v.Atom)
		}
	}
	m := &FoundryTools{}
	if h := m.startShadow(soon(t), "", "b"); h == nil || h.box != nil || m.box != nil {
		t.Errorf("a chain-voted lane was given a ballot box: %+v", h)
	}
	casting(t, voterBinary)
	m = &FoundryTools{}
	if h := m.startShadow(soon(t), "", "b"); h == nil || h.box == nil || m.box != h.box {
		t.Errorf("a binary-voted lane has no ballot box: %+v", h)
	}
}

// The push sequence asks the binary once, when it reaches the first of its
// atoms, and a vote that finds something stops it like any other atom.
func TestThePushSequenceVotesFromTheBinaryOnce(t *testing.T) {
	ran := casting(t, voterBinary)
	// The sequence runs in the catalogue's order, which is not the registry's.
	var ids []string
	for _, a := range checks.AtomsForStage(checks.StagePrepush) {
		if slices.Contains(atoms.StageIDs(checks.StagePrepush), a.ID) {
			ids = append(ids, a.ID)
		}
	}
	engine.reset()
	engine.withTree(bareTree)
	vs, unreached, err := bareModule().sequence(soon(t), checks.StagePrepush, "")
	if err != nil || len(unreached) != 0 {
		t.Fatalf("unreached %v, err %v", unreached, err)
	}
	if len(*ran) != 1 {
		t.Errorf("the binary ran %d times for one sequence", len(*ran))
	}
	var voted []string
	for _, v := range vs {
		if strings.HasPrefix(v.Reason, votedMark) {
			voted = append(voted, v.Atom)
		}
	}
	if !slices.Equal(voted, ids) {
		t.Errorf("the sequence took %v from the binary, want %v", voted, ids)
	}

	// The third atom finds something: the sequence stops there and names the rest.
	castBinary = func(context.Context, *FoundryTools, string, string) (string, error) {
		var out []checks.Verdict
		for _, id := range ids {
			out = append(out, checks.VerdictOf(checks.AtomByID(id), map[bool]int{true: 1, false: 0}[id == ids[2]], "x"))
		}
		raw, _ := json.Marshal(out)
		return string(raw), nil
	}
	vs, unreached, err = bareModule().sequence(soon(t), checks.StagePrepush, "")
	stopped := slices.IndexFunc(vs, func(v checks.Verdict) bool { return v.Atom == ids[2] })
	reached := slices.IndexFunc(vs, func(v checks.Verdict) bool { return v.Atom == ids[3] })
	if err != nil || !slices.Equal(unreached, ids[3:]) || stopped < 0 || vs[stopped].State != 1 || reached >= 0 {
		t.Errorf("unreached %v, stopped at %d, reached %d, err %v; want the tail %v", unreached, stopped, reached, err, ids[3:])
	}
}

// poll answers nothing for what it does not carry, and nothing at all when nil.
func TestAPollAnswersOnlyForTheAtomsItCarries(t *testing.T) {
	var none *poll
	if _, ok := none.vote(soon(t), "fleet:check-yaml"); ok || none.carries("fleet:check-yaml") {
		t.Error("a nil poll answered")
	}
	none.start(soon(t))
	ran := casting(t, voterBinary)
	p := bareModule().pollFor(voterBinary, checks.StagePrecommit, "", checks.AtomsForStage(checks.StagePrecommit))
	if _, ok := p.vote(soon(t), "go:vet"); ok {
		t.Error("the poll answered for a toolchain atom")
	}
	if len(*ran) != 0 {
		t.Error("asking after an atom the binary lacks ran the binary")
	}
	for _, id := range []string{"fleet:check-yaml", "fleet:check-merge-conflict"} {
		if v, ok := p.vote(soon(t), id); !ok || v.Atom != id {
			t.Errorf("%s: %+v %v", id, v, ok)
		}
	}
	if len(*ran) != 1 {
		t.Errorf("two atoms ran the binary %d times", len(*ran))
	}
	if p := bareModule().pollFor(voterBinary, checks.StageMutation, "", checks.AtomsForStage(checks.StageMutation)); p != nil {
		t.Error("the mutation lane has a poll")
	}
	if p := bareModule().pollFor(voterChains, "", "", checks.AtomsForStage("")); p != nil {
		t.Error("the chains' vote has a poll")
	}
}

// (d) THE WITNESS VOTES FOR REAL: no dry flag in any stage, and the lane's
// socket is mounted and named for the stage that carries fleet:witness.
func TestTheVoterAsksTheWitnessForRealAndMountsTheSocket(t *testing.T) {
	for _, tc := range []struct {
		stage  string
		spire  bool
		socket bool
	}{
		{"prepush", true, true},
		{"", true, true},
		{"prepush", false, false},
		{"precommit", true, false},
		{"orbit", true, false},
	} {
		t.Run(tc.stage+map[bool]string{true: " with a socket", false: " without"}[tc.spire], func(t *testing.T) {
			m := &FoundryTools{Source: dag.Directory(), Repo: "http://door/rob/x.git", Sha: buildSha}
			if tc.spire {
				m.spire = dag.LoadSocketFromID("spire-agent-socket")
			}
			engine.reset()
			engine.withTree(laneDies(map[string]string{"go.mod": "module x\n"}))
			engine.stdout(`"/usr/local/bin/atoms"`, "[]")
			if _, err := m.atomsBallot(soon(t), tc.stage, "abc"); err != nil {
				t.Fatal(err)
			}
			c := engine.chain(`"/usr/local/bin/atoms"`)
			if strings.Contains(c, "-witness-dry") {
				t.Errorf("the voter was told to ask nothing:\n%s", c)
			}
			if got := strings.Contains(c, `"-spire","`+atomsSpirePath+`"`) && strings.Contains(c, "withUnixSocket"); got != tc.socket {
				t.Errorf("socket mounted and named: %v, want %v\n%s", got, tc.socket, c)
			}
			if !strings.Contains(c, `from(address:"`+checks.ImageTools+`")`) || strings.Contains(c, checks.ImageFleet) {
				t.Errorf("the voter ran off the tools container:\n%s", c)
			}
		})
	}
}

// The default run of the binary for a vote IS the voting run: end to end, a lane
// that forwarded a socket gets it mounted through vector.
func TestTheLanesVoteRunsTheVotingBinary(t *testing.T) {
	casting(t, voterBinary)
	castBinary = func(ctx context.Context, m *FoundryTools, stage, base string) (string, error) {
		return m.atomsBallot(ctx, stage, base)
	}
	m := &FoundryTools{Source: dag.Directory(), Repo: "http://door/rob/x.git", Sha: buildSha, spire: dag.LoadSocketFromID("spire-agent-socket")}
	engine.reset()
	engine.withTree(laneDies(bareTree))
	engine.stdout(`"/usr/local/bin/atoms"`, binaryVector(t, checks.StagePrepush))
	vs, err := m.vector(soon(t), checks.StagePrepush, "fleet:witness", "abc")
	if err != nil || len(vs) != 1 || !strings.HasPrefix(vs[0].Reason, votedMark) {
		t.Fatalf("vector %+v, err %v", vs, err)
	}
	if c := engine.chain(`"/usr/local/bin/atoms"`); !strings.Contains(c, "withUnixSocket") || strings.Contains(c, "-witness-dry") {
		t.Errorf("the voter's chain:\n%s", c)
	}
}

// (e) THE RECORD'S SHAPE IS UNCHANGED: verdicts that cross the binary's JSON
// and the vote make the same record, byte for byte, as the same verdicts
// built in process the way a chain builds them - state, reason, lines,
// findings, truncation and times included.
func TestTheRecordFromTheBinaryIsTheRecordFromTheChain(t *testing.T) {
	ids := atoms.StageIDs(checks.StagePrecommit)
	var chain []checks.Verdict
	for i, id := range ids {
		a := checks.AtomByID(id)
		var v checks.Verdict
		switch i % 4 {
		case 0:
			v = checks.VerdictOf(a, 0, "line one\nline two")
		case 1:
			v = checks.VerdictOf(a, 1, "bad.yml: broken\nsecond")
			v.Findings = []checks.Finding{{Verdict: "fail", Subject: "bad.yml", Cause: "broken", Detail: "d", Probe: "p"}}
		case 2:
			v = checks.VerdictOf(a, 2, id+": CANNOT RUN - no tool")
		default:
			v = checks.AbsentVerdict(a)
			v.Logs, v.Truncated, v.OriginalBytes = []string{}, true, 12345
		}
		if v.Logs == nil {
			v.Logs = []string{}
		}
		started := time.Date(2026, 10, 8, 1, 2, 3, 4e6, time.UTC)
		chain = append(chain, checks.Timed(v, started, started.Add(1500*time.Millisecond)))
	}
	raw, err := json.MarshalIndent(chain, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	casting(t, voterBinary)
	castBinary = func(context.Context, *FoundryTools, string, string) (string, error) { return string(raw), nil }
	engine.reset()
	engine.withTree(bareTree)
	voted, err := bareModule().vector(soon(t), checks.StagePrecommit, strings.Join(ids, ","), "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := stageResult(checks.SettleStage("gate", voted)).Record()
	if err != nil {
		t.Fatal(err)
	}
	want, err := stageResult(checks.SettleStage("gate", chain)).Record()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("the record moved:\n got %.600s\nwant %.600s", got, want)
	}
	if !strings.Contains(got, `"started_at":"2026-10-08T01:02:03.004Z"`) || !strings.Contains(got, `"Findings":[{"Verdict":"fail"`) {
		t.Errorf("the record lost the duration or the findings: %.400s", got)
	}
}
