package main

import (
	"context"
	"slices"
	"strings"
	"sync"
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

// A TREE THAT CANNOT BE READ IS A COULD-NOT-RUN ABOUT THE REPOSITORY, one per
// atom whose lane the planner could not decide — never a silent absence, which
// would read as "this repo has no go.mod" when the truth is "nobody could
// look". The atoms that run everywhere are unaffected: they need no lane.
func TestATreeThePlannerCannotReadIsACouldNotRunPerLaneAtom(t *testing.T) {
	// EITHER READ CAN BE THE ONE THAT FAILS, and the atom is told which: the
	// root listing, or the module walk that decides the go lane. A reason
	// that named neither would be a could-not-run nobody can act on.
	for _, c := range []struct{ match, why string }{
		{"entries", "the tree went away"},
		{"glob", "the module walk went away"},
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		engine.fail(c.match, c.why)
		vs, err := (&FoundryTools{Source: dag.Directory()}).vector(context.Background(), checks.StagePrecommit, "", "")
		if err != nil {
			t.Fatal(err)
		}
		var lanes, anywhere int
		for _, v := range vs {
			if checks.AtomByID(v.Atom).Lane == checks.LaneAny {
				anywhere++
				continue
			}
			lanes++
			if v.State != 2 || !strings.Contains(v.Reason, "could not read the repository root") || !strings.Contains(v.Reason, c.why) {
				t.Errorf("%s (%s failed): want a could-not-run naming it, got %+v", v.Atom, c.match, v)
			}
		}
		if lanes == 0 || anywhere == 0 {
			t.Errorf("%s: %d lane atoms and %d run-anywhere atoms answered: the stage is neither", c.match, lanes, anywhere)
		}
	}
}

// Release hands F14 the binaries the push compiled — and answers an ERROR,
// never an empty directory, when there is nothing to hand over.
func TestReleaseAnswersTheBuiltBinariesOrSaysWhyNot(t *testing.T) {
	tree := map[string]string{
		"Dockerfile":          "FROM x\n",
		".copier-answers.yml": "service_name: hades\n",
		"go.mod":              "module x\n",
	}
	engine.reset()
	engine.withTree(tree)
	dir, err := (&FoundryTools{Source: dag.Directory()}).Release(context.Background())
	if err != nil || dir == nil {
		t.Fatalf("dir %v err %v", dir, err)
	}
	if engine.chain(`"-o","/out/hades"`) == "" {
		t.Errorf("the release build ran:\n%v", engine.chains())
	}

	// A build that failed has no directory to give.
	engine.reset()
	engine.withTree(tree)
	engine.exitCode(`"go","build","-trimpath"`, 1)
	engine.stderr(`"go","build","-trimpath"`, "main.go:1: undefined: x")
	if dir, err := (&FoundryTools{Source: dag.Directory()}).Release(context.Background()); err == nil || dir != nil {
		t.Errorf("a failed release build is an error, got dir %v err %v", dir, err)
	} else if !strings.Contains(err.Error(), "undefined: x") {
		t.Errorf("the error carries the compiler's own words: %v", err)
	}

	// A repo that names no star cannot name a binary either.
	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module x\n"})
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(context.Background()); err == nil || !strings.Contains(err.Error(), "names no star") {
		t.Errorf("err %v", err)
	}

	// A repo that names a star but carries no root module has no star binary:
	// Release is called directly by F14's Build, so it cannot lean on the
	// planner's lane check the way the atom does.
	engine.reset()
	engine.withTree(map[string]string{
		"Dockerfile":          "FROM x\n",
		".copier-answers.yml": "service_name: hades\n",
		"tools/go.mod":        "module x\n",
	})
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(context.Background()); err == nil || !strings.Contains(err.Error(), "no go.mod at the repository root") {
		t.Errorf("err %v", err)
	}

	// An engine that goes away mid-build is an error about the run.
	engine.reset()
	engine.withTree(tree)
	engine.fail(`"go","build","-trimpath"`, "the engine went away")
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(context.Background()); err == nil || !strings.Contains(err.Error(), "the engine went away") {
		t.Errorf("err %v", err)
	}
}

// THE LANE IS THE TREE'S (F17, the Rust half). A root Cargo.toml is the Rust
// lane's release: the rust:release atom's own exec, and the directory is the
// binaries read back from where cargo left them, each under its own name —
// never the whole target/ tree. Both toolchains at the root is refused, the
// way the cast lane refuses it.
func TestReleaseBuildsARustStarWithCargoAndHandsOverItsBinaries(t *testing.T) {
	tree := map[string]string{
		"Dockerfile":          "FROM x\nCOPY release/tron /tron\n",
		".copier-answers.yml": "service_name: tron\n",
		"Cargo.toml":          "[workspace]\nmembers = [\"crates/tron\"]\n",
		"Cargo.lock":          "",
	}
	engine.reset()
	engine.withTree(tree)
	dir, err := (&FoundryTools{Source: dag.Directory()}).Release(context.Background())
	if err != nil || dir == nil {
		t.Fatalf("dir %v err %v", dir, err)
	}
	build := engine.chain(`"cargo","build","--release","--locked","-p","tron"`)
	if build == "" {
		t.Fatalf("the rust release build never ran:\n%v", engine.chains())
	}
	if engine.chain(`"go","build"`) != "" {
		t.Errorf("a Rust star got a Go compile:\n%v", engine.chains())
	}
	// The directory is built from the binary, by name: the file is read off
	// the build container where cargo left it, and a fresh directory takes
	// it under its own name — lazily, so the chain is recorded when the
	// directory is first used, as the build lane uses it.
	if _, err := dag.Directory().WithDirectory("release", dir).Entries(context.Background()); err != nil {
		t.Fatalf("the release directory could not be used: %v", err)
	}
	bin := engine.chain(`file(path:"/work/target/release/tron")`, "{id}")
	if bin == "" {
		t.Fatalf("the binary is not read back out of cargo's target directory:\n%v", engine.chains())
	}
	if c := engine.chain(`directory{withFile(`, `path:"tron"`); c == "" || !strings.Contains(c, fakeID(bin)) {
		t.Errorf("the release directory does not carry the built binary under its name:\n%s\n%v", c, engine.chains())
	}
	if strings.Contains(engine.chain(`directory{withFile(`), "/work/target") {
		t.Errorf("the whole target tree was handed over, not the binary")
	}

	// A crate that does not compile is an error carrying the compiler's words.
	engine.reset()
	engine.withTree(tree)
	engine.exitCode(`"-p","tron"`, 101)
	engine.stderr(`"-p","tron"`, "error[E0425]: cannot find value `x`")
	if dir, err := (&FoundryTools{Source: dag.Directory()}).Release(context.Background()); err == nil || dir != nil {
		t.Errorf("a failed release build is an error, got dir %v err %v", dir, err)
	} else if !strings.Contains(err.Error(), "E0425") {
		t.Errorf("the error carries the compiler's own words: %v", err)
	}

	// A record's binary that is not a name is refused before anything runs.
	engine.reset()
	engine.withTree(tree)
	engine.withTree(map[string]string{"/dies/fleet/stars/tron/slag.json": `{"tools":{"build":{"binaries":["../x"]}}}`})
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(context.Background()); err == nil || !strings.Contains(err.Error(), "is not a binary name") {
		t.Errorf("err %v", err)
	}
	if engine.chain(`"cargo","build"`) != "" {
		t.Errorf("a refused plan still compiled:\n%v", engine.chains())
	}

	// Both toolchains at the root: refused, naming the ambiguity.
	engine.reset()
	engine.withTree(tree)
	engine.withTree(map[string]string{"go.mod": "module x\n"})
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(context.Background()); err == nil || !strings.Contains(err.Error(), "both a Cargo.toml and a root go.mod") {
		t.Errorf("err %v", err)
	}

	// An engine that goes away mid-build is an error about the run, worded
	// apart from a compile that failed: the build lane classifies on it.
	engine.reset()
	engine.withTree(tree)
	engine.fail(`"-p","tron"`, "dial tcp: i/o timeout")
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(context.Background()); err == nil || !strings.Contains(err.Error(), "did not run") || !strings.Contains(err.Error(), "i/o timeout") {
		t.Errorf("err %v", err)
	}

	// A root that cannot be read is an error about the read, not a guess.
	engine.reset()
	engine.withTree(tree)
	engine.fail("entries", "the root went away")
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(context.Background()); err == nil || !strings.Contains(err.Error(), "the root went away") {
		t.Errorf("err %v", err)
	}
}

// THE SEQUENCE OBEYS THE PLANNER, and this test watches the DISPATCH, not the
// verdict. Measured live on foundry-tools' own pre-push hook: with the lane
// gate moved into run.plan and only the fanout wired to it, the push stage ran
// rust:cargo-audit (could-not-run, cargo exit 101) and python:pip-audit (PASS)
// in a repo carrying neither manifest. A pass for a lane the tree does not
// have is worse than a failure — it is a check nobody ran, reported as a check
// that looked.
//
// Asserting on the verdicts alone does NOT pin this: most lane atoms also
// check their own manifest and answer absent, so the stage reads the same
// either way while the container still ran. The spy below fails if the runner
// is entered at all.
func TestThePushSequenceNeverDispatchesALaneTheTreeDoesNotHave(t *testing.T) {
	engine.reset()
	// A Go repo, and nothing else: no Cargo.toml, no pyproject.toml, no package.json.
	engine.withTree(map[string]string{"go.mod": "module x\n", "main.go": "package main\n", "main_test.go": "package main\n"})
	// The go atoms must PASS, or the sequence stops at the first red and never
	// reaches the audits — which is exactly how the first version of this test
	// passed against the very bug it was written for.
	engine.stdout(`"go","list"`, "11\n")

	var mu sync.Mutex
	dispatched := map[string]bool{}
	for _, id := range []string{"rust:cargo-audit", "python:pip-audit", "ts:bun-audit", "go:staticcheck"} {
		real := registry[id]
		registry[id] = func(ctx context.Context, r *run) checks.Verdict {
			mu.Lock()
			dispatched[id] = true
			mu.Unlock()
			return real(ctx, r)
		}
		defer func() { registry[id] = real }()
	}

	res, err := (&FoundryTools{Source: dag.Directory()}).Push(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"rust:cargo-audit", "python:pip-audit", "ts:bun-audit"} {
		if dispatched[id] {
			t.Errorf("%s was dispatched in a tree carrying no such manifest", id)
		}
	}
	// The go lane IS here, so the sequence still runs it — a planner that
	// skipped everything would pass the assertions above and check nothing.
	if !dispatched["go:staticcheck"] {
		t.Errorf("go:staticcheck must run: the tree carries a go.mod")
	}
	omitted := map[string]bool{}
	for _, a := range res.Omitted {
		omitted[a.Atom] = true
	}
	for _, id := range []string{"rust:cargo-audit", "python:pip-audit", "ts:bun-audit"} {
		if !omitted[id] {
			t.Errorf("%s is absent here and must say so", id)
		}
	}
}
