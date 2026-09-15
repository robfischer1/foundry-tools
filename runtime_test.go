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
		[]string{"withMountedCache", `path:"/usr/local/cargo/registry"`},
		[]string{"withMountedCache", `path:"/usr/local/cargo/git"`},
		[]string{"withMountedCache", `path:"/cache/cargo-target"`},
		[]string{"withEnvVariable", `name:"CARGO_TARGET_DIR"`, `value:"/cache/cargo-target"`},
	)
	if hasCall(c, "withMountedCache", `source:`) {
		t.Errorf("the upstream toolchains carry no warm layer; no cache may be seeded:\n%s", c)
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	runAtom(t, "go:vet", "")
	c = engine.chain(`"go","vet"`, "exitCode")
	wantCalls(t, c, []string{"withEnvVariable", `name:"GOCACHE"`, `value:"/opt/go-build-cache"`})
	if strings.Contains(c, "CARGO_TARGET_DIR") || hasCall(c, "withEnvVariable", `value:"/go/pkg/mod"`) {
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
	engine.withTree(tree)
	// go:mutation's resolve is a git exec on the readied repository, so its
	// chain carries everything gitReady did.
	wantState(t, runAtom(t, "go:mutation", "abc"), 0)
	c := engine.chain(`"git","rev-parse","--verify"`)
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
	c = engine.chain(`"git","rev-parse","--verify"`)
	if hasCall(c, "withExec", `"remote","add","origin"`) || !hasCall(c, "withExec", `args:["git","init","-q","."]`) {
		t.Errorf("no primary means init without an origin:\n%s", c)
	}

	// A primary checkout (.git is a directory) is left alone.
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "go:mutation", "abc"), 0)
	c = engine.chain(`"git","rev-parse","--verify"`)
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

// The lanes provision what their atoms exec, in the chain, pinned, in
// volatility order — the distro packages first, the copied binaries next,
// the scanner, the source-built tools last — and before any cache volume is
// mounted, so no layer depends on what a volume holds. A file copied out of
// another image or fetched from the mirror is its own query; the lane's chain
// carries it by id at the path it lands on.
func TestLanesProvisionTheirToolsPinnedAndInVolatilityOrder(t *testing.T) {
	order := func(t *testing.T, chain string, marks ...string) {
		t.Helper()
		last := -1
		for _, m := range marks {
			i := strings.Index(chain, m)
			if i < 0 {
				t.Errorf("chain lacks %s:\n%s", m, chain)
				return
			}
			if i < last {
				t.Errorf("%s is out of volatility order:\n%s", m, chain)
			}
			last = i
		}
	}
	fetched := func(t *testing.T, needle string) {
		t.Helper()
		if engine.chain(needle) == "" {
			t.Errorf("nothing asked the engine for %s", needle)
		}
	}
	noShell := func(t *testing.T, chain string) {
		t.Helper()
		if strings.Contains(chain, `"sh","-c"`) || strings.Contains(chain, `"bash","-c"`) || strings.Contains(chain, `"curl","-`) {
			t.Errorf("provisioning runs no shell and pipes nothing through curl:\n%s", chain)
		}
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	runAtom(t, "go:vet", "")
	c := engine.chain(`"go","vet"`, "exitCode")
	order(t, c, `from(address:"`+checks.ImageGo+`")`, `path:"/usr/local/bin/opengrep"`,
		`"go","install","`+checks.GremlinsModule+`"`, `"go","install","`+checks.StaticcheckModule+`"`, `"go","install","`+checks.GovulncheckModule+`"`, `withMountedCache`)
	fetched(t, `http(url:"`+checks.OpengrepMirror+`")`)
	noShell(t, c)
	// The go lane scores mutation in Go: nothing it runs needs python3, and it
	// installs no distro package.
	if strings.Contains(c, `"apt-get"`) || strings.Contains(c, `"python3"`) {
		t.Errorf("the go lane still installs from apt:\n%s", c)
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	runAtom(t, "python:ruff-check", "")
	c = engine.chain(`"uvx","ruff@`, "exitCode")
	order(t, c, `from(address:"`+checks.ImagePython+`")`, `"apt-get","install"`, `"git"`, `path:"/usr/local/bin/uv"`, `path:"/usr/local/bin/uvx"`,
		`path:"/usr/local/bin/opengrep"`, `withMountedCache`)
	wantCalls(t, c, []string{"withEnvVariable", `name:"UV_CACHE_DIR"`, `value:"/opt/uv-cache"`})
	fetched(t, `from(address:"`+checks.ImageUV+`")`)
	noShell(t, c)

	engine.reset()
	engine.withTree(everyLaneTree)
	runAtom(t, "rust:cargo-fmt", "")
	c = engine.chain(`"cargo","fmt"`, "exitCode")
	order(t, c, `from(address:"`+checks.ImageRust+`")`, `"rustup","component","add","rustfmt","clippy"`, `path:"/usr/local/bin/opengrep"`,
		`"cargo","install","cargo-audit","--locked","--version","`+checks.CargoAuditVersion+`"`,
		`"cargo","install","cargo-mutants","--locked","--version","`+checks.CargoMutantsVersion+`"`, `withMountedCache`)
	noShell(t, c)

	engine.reset()
	engine.withTree(everyLaneTree)
	runAtom(t, "ts:bun-audit", "")
	c = engine.chain(`"bun","audit"`, "exitCode")
	order(t, c, `from(address:"`+checks.ImageTS+`")`, `withUser(name:"root")`, `"apt-get","install"`, `"procps"`, `path:"/usr/local/bin/node"`,
		`path:"/usr/local/bin/opengrep"`, `withMountedCache`)
	fetched(t, `from(address:"`+checks.ImageNode+`")`)
	noShell(t, c)
}
