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
