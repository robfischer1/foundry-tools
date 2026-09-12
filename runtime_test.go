package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// The runtime's own decisions, held on the wire.

func TestLaneMountsEachCacheAndExportsOnlyTheOneEnvVar(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	runAtom(t, "rust:cargo-clippy", "")
	c := engine.chain(`"cargo","clippy"`, "exitCode")
	wantCalls(t, c,
		[]string{"withMountedCache", `path:"/usr/local/cargo/registry"`, `source:`},
		[]string{"withMountedCache", `path:"/usr/local/cargo/git"`},
		[]string{"withMountedCache", `path:"/cache/cargo-target"`},
		[]string{"withEnvVariable", `name:"CARGO_TARGET_DIR"`, `value:"/cache/cargo-target"`},
	)
	if hasCall(c, "withMountedCache", `path:"/usr/local/cargo/git"`, `source:`) {
		t.Errorf("the cargo git cache has no seed in the image and must not be seeded:\n%s", c)
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	runAtom(t, "go:vet", "")
	c = engine.chain(`"go","vet"`, "exitCode")
	if strings.Contains(c, "CARGO_TARGET_DIR") || hasCall(c, "withEnvVariable", `value:"/go/pkg/mod"`) || hasCall(c, "withEnvVariable", `value:"/opt/go-build-cache"`) {
		t.Errorf("a cache without an EnvVar must export nothing:\n%s", c)
	}
}

func TestPopulationThatCannotBeEnumeratedIsACannotRun(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`glob(pattern:"**/*.go")`, "the engine could not walk the tree")
	wantState(t, runAtom(t, "go:gofmt", ""), 2, "could not enumerate", "could not walk the tree")
}

func TestOutputSeparatesTheEnginesErrorFromTheToolsCode(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	// The exit code answers but the stdout read fails: that is the engine's
	// error, and the atom never ran.
	engine.script(script{match: `"go","list"`, leaf: "stdout", fail: "stdout vanished"})
	wantState(t, runAtom(t, "go:test-race", ""), 2, "stdout vanished")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"gofmt","-l"`, "exec never started")
	wantState(t, runAtom(t, "go:gofmt", ""), 2, "exec never started")
}

func TestGitReadyRebuildsALinkedWorktreeAndNamesItsOrigin(t *testing.T) {
	engine.reset()
	tree := map[string]string{}
	for k, v := range everyLaneTree {
		tree[k] = v
	}
	delete(tree, ".git/HEAD")
	tree[".git"] = "gitdir: /home/rob/Forge/Outputs/tartarus/.git/worktrees/rowan\n"
	tree["/tmp/mutation/verdict"] = "0\n"
	tree["/tmp/mutation/reason"] = "clean"
	engine.withTree(tree)
	wantState(t, runAtom(t, "go:mutation", "abc"), 0)
	c := engine.chain(`go.sh","score"`)
	wantCalls(t, c,
		[]string{"withExec", `args:["git","config","--global","--add","safe.directory","*"]`},
		[]string{"withExec", `args:["git","init","-q","."]`},
		[]string{"withExec", `args:["git","add","-A"]`},
		[]string{"withExec", `args:["git","remote","add","origin","/home/rob/Forge/Outputs/tartarus.git"]`},
	)
	// The remounted tree is its own directory chain; the .git file is
	// dropped there, before the throwaway repository is made.
	if engine.chain(`withoutFile(path:".git")`) == "" {
		t.Errorf("the dangling .git file must be dropped from the tree that is remounted")
	}

	// A gitdir that names no primary gets a repository and no origin.
	engine.reset()
	tree[".git"] = "gitdir: /nowhere\n"
	engine.withTree(tree)
	wantState(t, runAtom(t, "go:mutation", "abc"), 0)
	c = engine.chain(`go.sh","score"`)
	if hasCall(c, "withExec", `"remote","add","origin"`) || !hasCall(c, "withExec", `args:["git","init","-q","."]`) {
		t.Errorf("no primary means init without an origin:\n%s", c)
	}

	// A primary checkout (.git is a directory) is left alone.
	engine.reset()
	engine.withTree(everyLaneTree)
	tree2 := map[string]string{"/tmp/mutation/verdict": "0\n", "/tmp/mutation/reason": "clean"}
	engine.withTree(tree2)
	wantState(t, runAtom(t, "go:mutation", "abc"), 0)
	c = engine.chain(`go.sh","score"`)
	if hasCall(c, "withExec", `args:["git","init","-q","."]`) || !hasCall(c, "withExec", `"safe.directory"`) {
		t.Errorf("a primary checkout keeps its repository and still gets safe.directory:\n%s", c)
	}
}

func TestFetchToolTriesTheMirrorThenUpstreamAndFailsHonestly(t *testing.T) {
	ctx := context.Background()
	engine.reset()
	if _, err := fetchTool(ctx, "https://nexus.example/x", "https://upstream.example/x"); err != nil {
		t.Fatalf("mirror up: %v", err)
	}
	if engine.chain(`http(url:"https://upstream.example/x")`) != "" {
		t.Errorf("upstream must not be asked while the mirror answers")
	}

	engine.reset()
	engine.fail(`https://nexus.example/x`, "404")
	if _, err := fetchTool(ctx, "https://nexus.example/x", "https://upstream.example/x"); err != nil {
		t.Fatalf("upstream up: %v", err)
	}
	if engine.chain(`http(url:"https://upstream.example/x")`, "sync") == "" {
		t.Errorf("a failed mirror must fall through to upstream")
	}

	engine.reset()
	engine.fail(`https://nexus.example/x`, "404")
	engine.fail(`https://upstream.example/x`, "timeout")
	_, err := fetchTool(ctx, "https://nexus.example/x", "https://upstream.example/x")
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "timeout") {
		t.Errorf("both failing must be the error, naming both: %v", err)
	}
}

func TestVerdictsAnswersTheVectorInCatalogueOrder(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	m := &FoundryTools{Source: dag.Directory()}
	out, err := m.Verdicts(context.Background(), "", "go:build,go:vet,fleet:check-yaml", "")
	if err != nil {
		t.Fatal(err)
	}
	var vs []checks.Verdict
	if err := json.Unmarshal([]byte(out), &vs); err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, v := range vs {
		got = append(got, v.Atom)
	}
	// Positions follow the request (checks.Select keeps its order), never the
	// finish order of the concurrent atoms.
	if strings.Join(got, ",") != "go:build,go:vet,fleet:check-yaml" {
		t.Errorf("vector order %v", got)
	}

	// The default stage set is the pull path and never the sweep.
	out, err = m.Verdicts(context.Background(), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"stage": "sweep"`) || strings.Contains(out, `"stage": "mutation"`) {
		t.Errorf("the bare vector must carry no sweep or mutation atom")
	}
	if _, err := m.Verdicts(context.Background(), "", "go:nope", ""); err == nil {
		t.Errorf("an unknown atom id must be an error, not an empty vector")
	}
}

func TestCheckAnswersTheWayDaggerCheckReads(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	if _, err := check(context.Background(), dag.Directory(), "go:vet"); err != nil {
		t.Errorf("a pass is nil: %v", err)
	}
	engine.exitCode(`"go","vet"`, 1)
	if _, err := check(context.Background(), dag.Directory(), "go:vet"); err == nil {
		t.Errorf("findings are the error dagger check reads")
	}
}
