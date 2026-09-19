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

// Every lane carries the OTel SDK's kill switch — the engine hands each exec
// an OTLP endpoint it cannot accept metrics on, and a star its tests boot
// would otherwise push there — and the trust variables for uv, node and bun.
// Pinned on one atom per lane image, because laneBase is the only place the
// variables may come from.
func TestEveryLaneDisablesTheOTelSDKAndTrustsTheEnginesCA(t *testing.T) {
	for atom, probe := range map[string]string{
		"go:vet":            `"go","vet"`,
		"rust:cargo-clippy": `"cargo","clippy"`,
		"python:pytest":     `"pytest","-q"`,
		"ts:bun-gate":       `"bun","run","gate"`,
		"fleet:witness":     `"git","--version"`,
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		runAtom(t, atom, "")
		c := engine.chain(probe, "exitCode")
		if c == "" {
			c = engine.chain(probe)
		}
		if c == "" {
			t.Errorf("%s: no exec matched %s — the probe is stale, not the lane", atom, probe)
			continue
		}
		if !hasCall(c, "withEnvVariable", `name:"OTEL_SDK_DISABLED"`, `value:"true"`) {
			t.Errorf("%s runs without OTEL_SDK_DISABLED=true — a star its tests boot will push metrics at the engine:\n%s", atom, c)
		}
		// The clients that do not read the system pool trust the engine's CA
		// — the transparent cache's signer — by name, on every lane, before
		// the engine names the intercept face (the F4 incident's fix, not
		// its repeat).
		if !hasCall(c, "withEnvVariable", `name:"UV_NATIVE_TLS"`, `value:"1"`) {
			t.Errorf("%s runs without UV_NATIVE_TLS=1 — uv would refuse the cache's certificate:\n%s", atom, c)
		}
		if !hasCall(c, "withEnvVariable", `name:"NODE_EXTRA_CA_CERTS"`, `value:"/etc/ssl/certs/ca-certificates.crt"`) {
			t.Errorf("%s runs without NODE_EXTRA_CA_CERTS — node and bun would refuse the cache's certificate:\n%s", atom, c)
		}
		// The certifi readers (pip-audit measured, 2026-09-19): requests,
		// urllib3 and pip itself read these, not the system store.
		for _, name := range []string{"SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "PIP_CERT"} {
			if !hasCall(c, "withEnvVariable", `name:"`+name+`"`, `value:"/etc/ssl/certs/ca-certificates.crt"`) {
				t.Errorf("%s runs without %s — a certifi-reading python client would refuse the cache's certificate:\n%s", atom, name, c)
			}
		}
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
	engine.stdout(`"git","merge-base","abc","HEAD"`, sinceSha+"\n")
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
	engine.stdout(`"git","merge-base","abc","HEAD"`, sinceSha+"\n")
	wantState(t, runAtom(t, "go:mutation", "abc"), 0)
	c = engine.chain(`"git","rev-parse","--verify"`)
	if hasCall(c, "withExec", `"remote","add","origin"`) || !hasCall(c, "withExec", `args:["git","init","-q","."]`) {
		t.Errorf("no primary means init without an origin:\n%s", c)
	}

	// A primary checkout (.git is a directory) is left alone.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stdout(`"git","merge-base","abc","HEAD"`, sinceSha+"\n")
	wantState(t, runAtom(t, "go:mutation", "abc"), 0)
	c = engine.chain(`"git","rev-parse","--verify"`)
	if hasCall(c, "withExec", `args:["git","init","-q","."]`) || !hasCall(c, "withExec", `"safe.directory"`) {
		t.Errorf("a primary checkout keeps its repository and still gets safe.directory:\n%s", c)
	}
}

func TestFetchToolFetchesTheURLAndFailsHonestly(t *testing.T) {
	ctx := context.Background()
	engine.reset()
	if _, err := fetchTool(ctx, "https://upstream.example/x"); err != nil {
		t.Fatalf("upstream up: %v", err)
	}
	if engine.chain(`http(url:"https://upstream.example/x")`, "sync") == "" {
		t.Errorf("the URL must be fetched and synced")
	}

	engine.reset()
	engine.fail(`https://upstream.example/x`, "timeout")
	_, err := fetchTool(ctx, "https://upstream.example/x")
	if err == nil || !strings.Contains(err.Error(), "timeout") || !strings.Contains(err.Error(), "https://upstream.example/x") {
		t.Errorf("a failed fetch must be the error, naming the URL and the cause: %v", err)
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
		`"go","install","`+checks.GremlinsModule+`"`, `"go","install","`+checks.MutationGateModule+`"`, `"go","install","`+checks.StaticcheckModule+`"`, `"go","install","`+checks.GovulncheckModule+`"`, `withMountedCache`)
	fetched(t, `http(url:"`+checks.OpengrepURL+`")`)
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

// THE NARROWING (CA F12). A compile, a vet, a lint or a test suite mounts the
// tree less checks.InertPaths — the README, the changelog, the hooks, the
// justfiles, pre-commit's and copier's files — so its exec is keyed on the
// code and an edit to any of those re-runs nothing. The git-reading atoms
// (mutation, the witness) keep the whole tree: an excluded file reads as
// deleted to a working-tree diff.
func TestToolchainAtomsMountTheNarrowedTreeAndGitAtomsDoNot(t *testing.T) {
	narrowed := `filter(exclude:["` + strings.Join(checks.InertPaths, `","`) + `"])`

	for _, id := range []string{"go:vet", "go:build", "go:gofmt", "go:test", "go:staticcheck", "go:govulncheck"} {
		engine.reset()
		engine.withTree(everyLaneTree)
		engine.stdout(`"go","list","-f"`, "11\n")
		runAtom(t, id, "")
		c := engine.chain(`withMountedDirectory(path:"/src"`)
		src := engine.chain(narrowed)
		if src == "" {
			t.Errorf("%s: no directory in its chain is filtered by InertPaths — its exec is keyed on the whole tree:\n%s", id, c)
			continue
		}
		if !strings.Contains(c, fakeID(src)) {
			t.Errorf("%s: the narrowed directory was built but /src does not mount it:\n%s", id, c)
		}
	}

	for _, id := range []string{"go:mutation", "fleet:witness"} {
		engine.reset()
		engine.withTree(everyLaneTree)
		engine.stdout(`"git","merge-base","abc123","HEAD"`, "since0\n")
		engine.stdout(`"git","diff","--relative"`, "a.go\n")
		runAtom(t, id, "abc123")
		if src := engine.chain(narrowed); src != "" {
			t.Errorf("%s runs git against the mount and must see the whole tree, but built a narrowed one:\n%s", id, src)
		}
	}
}

// The narrowed tree is the same tree gitReady swaps in for a linked
// worktree: a suite that shells out to git still gets a rebuilt repository,
// and still does not get the README.
func TestGitReadyOnKeepsTheNarrowedTreeThroughTheWorktreeSwap(t *testing.T) {
	engine.reset()
	tree := map[string]string{}
	for k, v := range everyLaneTree {
		tree[k] = v
	}
	delete(tree, ".git/HEAD")
	tree[".git"] = "gitdir: /home/rob/Forge/Outputs/tartarus/.git/worktrees/rowan\n"
	engine.withTree(tree)
	engine.stdout(`"go","list","-f"`, "11\n")
	runAtom(t, "go:test", "")
	narrowed := `filter(exclude:["` + strings.Join(checks.InertPaths, `","`) + `"])`
	swap := engine.chain(`withoutFile(path:".git")`)
	if swap == "" || !strings.Contains(swap, narrowed) {
		t.Errorf("the worktree swap must drop .git from the NARROWED tree, not from the whole one:\n%s", swap)
	}
}
