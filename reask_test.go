package main

import (
	"regexp"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// A COULD-NOT-RUN IS ASKED AGAIN PAST THE CACHE, and the second answer is the
// one reported. The re-ask carries CA_REASK on its lane, so every exec after
// it is keyed afresh; the first ask never does.
func TestACouldNotRunIsAskedAgainPastTheCache(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"go","vet"`, 2)
	engine.exitCode(`name:"CA_REASK"`, 0)
	v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "go:vet")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != 0 || strings.Contains(v.Reason, "asked twice") {
		t.Errorf("a could-not-run that looks clean the second time is clean: %+v", v)
	}
	fresh, reasked := 0, 0
	for _, c := range engine.chains() {
		if !strings.Contains(c, `"go","vet"`) {
			continue
		}
		if strings.Contains(c, `name:"CA_REASK"`) {
			reasked++
		} else {
			fresh++
		}
	}
	if fresh == 0 || reasked == 0 {
		t.Errorf("want the first ask without CA_REASK and the re-ask with it: %d fresh, %d reasked", fresh, reasked)
	}

	// Could not run both times: still a could-not-run, and it says it looked twice.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"go","vet"`, 2)
	v, err = verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "go:vet")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != 2 || !strings.Contains(v.Reason, "asked twice, the second time past the engine's cache") {
		t.Errorf("twice could-not-run is could-not-run, named as asked twice: %+v", v)
	}
}

// A pass or a finding is the tool's answer and is never asked again.
func TestOnlyACouldNotRunIsAskedAgain(t *testing.T) {
	for _, code := range []int{0, 1} {
		engine.reset()
		engine.withTree(everyLaneTree)
		engine.exitCode(`"go","vet"`, code)
		v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "go:vet")
		if err != nil {
			t.Fatal(err)
		}
		if v.State != code {
			t.Errorf("exit %d: state %d", code, v.State)
		}
		if engine.chain(`name:"CA_REASK"`) != "" {
			t.Errorf("exit %d was asked again", code)
		}
	}
}

// GO MUTATION RE-ASKS THE MODULE, NOT THE ATOM. verdictFor does not ask the
// Go mutation atom twice whole; it asks a module that could not run once
// more, past the cache — so a transient is still looked at again, and a run
// that could not run both times says so once.
func TestGoMutationIsReaskedByModuleNotWhole(t *testing.T) {
	scriptGoMutation(nil)
	engine.exitCode(goDiffNeedle, 1)
	engine.exitCode(`name:"CA_REASK"`, 0)
	v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", "abc123"), "go:mutation")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != 0 || strings.Contains(v.Reason, "asked twice") {
		t.Errorf("a module that looks clean the second time is clean: %+v", v)
	}

	scriptGoMutation(nil)
	engine.exitCode(goDiffNeedle, 1)
	v, err = verdictFor(t.Context(), newRun(dag.Directory(), "", "abc123"), "go:mutation")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != 2 || strings.Count(v.Reason, "asked twice") != 1 {
		t.Errorf("could not run both times, said once: %+v", v)
	}
	if n := len(reaskNonces()); n != 1 {
		t.Errorf("the module was re-asked %d times, want once", n)
	}
}

// A MODULE THAT GRADED IS NOT GRADED AGAIN because another module could not
// run: only tools/forge, which could not, is asked past the cache.
func TestOnlyTheModuleThatCouldNotRunIsReasked(t *testing.T) {
	scriptGoMutation(nil)
	engine.withTree(nestedTree)
	engine.stdout(goDiffNeedle, "a.go\n")
	engine.fail(`path:"/src/tools/forge"){withExec(args:["go","mod","download"]`, "proxy said 404")
	v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", "abc123"), "go:mutation")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != 2 || !strings.Contains(v.Reason, "asked twice") {
		t.Fatalf("tools/forge could not run both times: %+v", v)
	}
	rootRan, rootReasked, forgeReasked := false, false, false
	for _, c := range engine.chains() {
		forge := strings.Contains(c, `path:"/src/tools/forge"`)
		again := strings.Contains(c, `name:"CA_REASK"`)
		rootRan = rootRan || (!forge && strings.Contains(c, goMutantsNeedle))
		rootReasked = rootReasked || (!forge && again)
		forgeReasked = forgeReasked || (forge && again)
	}
	if !rootRan || rootReasked || !forgeReasked || len(reaskNonces()) != 1 {
		t.Errorf("root graded %v (re-asked %v), tools/forge re-asked %v, re-asks %d: want true, false, true, 1", rootRan, rootReasked, forgeReasked, len(reaskNonces()))
	}
}

// THE RE-ASK KEEPS THE LOOKUP it was armed with, so a unit a stored grading
// answers is not graded cold the second time.
func TestAModuleReaskKeepsItsLookup(t *testing.T) {
	scriptTwoUnits()
	engine.failLeaf(goMutantsNeedle, "exitCode", "engine gone")
	asked := 0
	v := runWithLookup(t, func(keys []checks.UnitKey) (map[string]checks.ReusedGrading, error) {
		asked++
		return hitFor("internal/x", checks.ReusedGrading{Lane: "mutation", RunNumber: 9})(keys)
	})
	if v.State != 2 || asked != 2 {
		t.Fatalf("state %d, lookups %d: want 2 and 2", v.State, asked)
	}
	if c := engine.chain(goMutantsNeedle, `name:"CA_REASK"`); !strings.Contains(c, `"-changed-since","since0","."]`) {
		t.Errorf("the re-ask graded what the lookup answered:\n%s", c)
	}
}

// reaskNonces are the distinct CA_REASK keys the recorded chains carry: one
// per re-ask, whatever number of leaves each read.
func reaskNonces() map[string]bool {
	re := regexp.MustCompile(`name:"CA_REASK", value:"([^"]*)"|value:"([^"]*)", name:"CA_REASK"`)
	out := map[string]bool{}
	for _, c := range engine.chains() {
		for _, m := range re.FindAllStringSubmatch(c, -1) {
			out[m[1]+m[2]] = true
		}
	}
	return out
}

// goAllTimedOutReport is a report whose only mutant TIMED OUT: 100% over the
// 10% budget, which GoMutationVerdict settles as could-not-run.
const goAllTimedOutReport = `{"elapsed_time":1,"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"TIMED OUT","line":1,"column":1}]}]}`

func goMutationAt(t *testing.T, sha string) checks.Verdict {
	t.Helper()
	v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", "abc123").withArtifacts(nil, sha), "go:mutation")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// AN OVER-BUDGET TIMEOUT IS DETERMINISTIC (foundry-tools#16081, Rob's ruling):
// the same tree times out the same way, so the second ask for the same commit
// sha neither re-asks inside the run nor runs again on the next ask — it
// returns the cached verdict. A new sha is a new tree and runs.
func TestAnOverBudgetTimeoutIsNotRerunForTheSameSha(t *testing.T) {
	scriptGoMutation(map[string]string{"/src/mutation-go.json": goAllTimedOutReport})
	first := goMutationAt(t, "sha-overbudget-1")
	if first.State != 2 || !strings.Contains(first.Reason, "over the 10% budget") {
		t.Fatalf("want the over-budget could-not-run: %+v", first)
	}
	if n := len(reaskNonces()); n != 0 {
		t.Errorf("a deterministic over-budget result was re-asked %d times, want 0", n)
	}
	if strings.Contains(first.Reason, "asked twice") {
		t.Errorf("it was not asked twice: %+v", first)
	}
	ran := gradingChains()
	if ran == 0 {
		t.Fatal("the first ask never reached gomutants")
	}

	again := goMutationAt(t, "sha-overbudget-1")
	if again.State != first.State || again.Reason != first.Reason {
		t.Errorf("the same sha must return the cached verdict:\nfirst: %+v\nagain: %+v", first, again)
	}
	if got := gradingChains(); got != ran {
		t.Errorf("the same sha ran gomutants %d more times, want none", got-ran)
	}
}

// A NEW COMMIT RE-RUNS.
func TestANewShaRerunsAnOverBudgetTimeout(t *testing.T) {
	scriptGoMutation(map[string]string{"/src/mutation-go.json": goAllTimedOutReport})
	goMutationAt(t, "sha-overbudget-2a")
	ran := gradingChains()
	v := goMutationAt(t, "sha-overbudget-2b")
	if got := gradingChains(); got <= ran {
		t.Errorf("a new sha must run gomutants, runs %d -> %d", ran, got)
	}
	if v.State != 2 {
		t.Errorf("state %d, want 2", v.State)
	}
}

// A NON-DETERMINISTIC could-not-run (a transient engine failure) is neither
// skipped nor cached: it is still re-asked, and asking again re-runs.
func TestATransientCouldNotRunIsStillReaskedAndNeverCached(t *testing.T) {
	scriptGoMutation(nil)
	engine.exitCode(goDiffNeedle, 1)
	v := goMutationAt(t, "sha-flaky")
	if v.State != 2 || strings.Count(v.Reason, "asked twice") != 1 {
		t.Fatalf("a flaky could-not-run is re-asked: %+v", v)
	}
	if n := len(reaskNonces()); n != 1 {
		t.Errorf("re-asked %d times, want once", n)
	}
	ran := len(engine.chains())
	goMutationAt(t, "sha-flaky")
	if got := len(engine.chains()); got <= ran {
		t.Errorf("a non-deterministic result must not be served from the cache, chains %d -> %d", ran, got)
	}
}

// gradingChains counts the recorded chains that run gomutants.
func gradingChains() int {
	n := 0
	for _, c := range engine.chains() {
		if strings.Contains(c, goMutantsNeedle) {
			n++
		}
	}
	return n
}
