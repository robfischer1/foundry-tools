package main

import (
	"slices"
	"strings"
	"sync"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/catalogue"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE DAGGER ADAPTER'S TESTS, against the paper engine (engine_fake_test.go):
// which container an atom of its file runs in, what the exec carries and how its
// answer becomes a verdict. What the atom decides is tested beside its file.

// UT-500: every atom that lives in a file is registered to the adapter, and the
// adapter answers for the id it was asked.
func TestUT_500_EveryPortedAtomIsRegisteredAndAnswersForItsID(t *testing.T) {
	entries := catalogue.Entries()
	if len(entries) == 0 {
		t.Fatal("no ported atoms")
	}
	for _, e := range entries {
		if registry[e.ID] == nil {
			t.Errorf("%s is not registered", e.ID)
			continue
		}
		engine.reset()
		engine.withTree(everyLaneTree)
		if v := runAtom(t, e.ID, ""); v.Atom != e.ID {
			t.Errorf("%s answered as %s", e.ID, v.Atom)
		}
	}
	if v := adapterFor("st:none")(t.Context(), newRun(dag.Directory(), "", "")); v.State != 2 || v.Atom != "st:none" ||
		!strings.Contains(v.Reason, "not in the generated registry") {
		t.Errorf("an id the registry lacks: %+v", v)
	}
}

// IT-133 / UT-496: a command atom runs its manifest's argv in the go lane after
// the toolchain's preflight, each its own exec under expect:ANY, and the exit is
// the state.
func TestIT_133_AManifestRunsItsArgvAfterThePreflight(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "go:vet", ""), 0, "go:vet: PASS")

	c := engine.chain(`"go","vet"`, "exitCode")
	if !strings.Contains(c, checks.ImageGo) {
		t.Errorf("go:vet must run in the go lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withEnvVariable", `name:"CI"`, `value:"true"`},
		[]string{"withMountedCache", `path:"/go/pkg/mod"`},
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withExec", `expect:ANY`, `args:["go","vet","./..."]`},
	)
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("go:vet must not read GATE_BASE: it would key the cache on the pull:\n%s", c)
	}
	// the preflight is tools.toml's, an exec of its own under expect:ANY (UT-702)
	pre := engine.chain(`"go","mod","download"`, "exitCode")
	wantCalls(t, pre, []string{"withWorkdir", `path:"/src"`}, []string{"withExec", `expect:ANY`, `args:["go","mod","download"]`})

	engine.exitCode(`"go","vet"`, 1)
	engine.stdout(`"go","vet"`, "x.go:3:2: fmt.Printf format %d has arg \"x\" of wrong type string")
	v := runAtom(t, "go:vet", "")
	wantState(t, v, 1, "go:vet: FINDINGS", "wrong type string")
	if !strings.Contains(strings.Join(v.Logs, "\n"), `fmt.Printf format %d has arg "x" of wrong type string`) {
		t.Errorf("the output is the atom's logs: %q", v.Logs)
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"go","build"`, 1)
	wantState(t, runAtom(t, "go:build", ""), 1)
	engine.exitCode(`"go","build"`, 137)
	wantState(t, runAtom(t, "go:build", ""), 2)
}

// A preflight that fails is the atom's could-not-run, and the command is not run.
func TestAPreflightThatFailsStopsTheCommand(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"go","mod","download"`, 1)
	engine.stderr(`"go","mod","download"`, "go: proxy 502\nlast line")
	wantState(t, runAtom(t, "go:vet", ""), 2, "go:vet: CANNOT RUN - preflight `go mod download` exited 1: last line")
	if engine.chain(`"go","vet"`) != "" {
		t.Error("go vet ran after its preflight failed")
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"go","mod","download"`, "exit code: 1: proxy 502")
	wantState(t, runAtom(t, "go:vet", ""), 2, "preflight", "never ran", "proxy 502")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"go","vet"`, "image would not pull")
	wantState(t, runAtom(t, "go:vet", ""), 2, "never ran", "image would not pull")
}

// The staticcheck manifest carries the fleet's check set, and the lane the
// tool is baked into carries it at its pin.
func TestGoStaticcheckRunsItsManifestsCheckSet(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "go:staticcheck", ""), 0)
	c := engine.chain(`"staticcheck","-checks"`, "exitCode")
	if n := strings.Count(c, `"go","install"`); n != 4 || !strings.Contains(c, checks.StaticcheckModule) || strings.Contains(c, "@latest") {
		t.Errorf("the lane provisions its tools at their pins, and nothing else installs:\n%s", c)
	}
	wantCalls(t, c, []string{"withExec", `expect:ANY`, `-ST1023`})
	engine.exitCode(`"staticcheck","-checks"`, 1)
	wantState(t, runAtom(t, "go:staticcheck", ""), 1)
}

// A tree with no go.mod is absent in the lane's own words, before any container.
func TestAGoManifestOnATreeWithNoModuleIsAbsent(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"pyproject.toml": ""})
	v := runAtom(t, "go:build", "")
	if v.Result != "absent" || !strings.Contains(v.Reason, "no go.mod anywhere in the tree") || engine.chain(`"go","build"`) != "" {
		t.Errorf("want absent and no container, got %+v", v)
	}
}

// Every Go module runs on its own, and a finding in one is the atom's, worded
// as the chains worded it (R20).
func TestAGoManifestRunsInEveryModule(t *testing.T) {
	tree := map[string]string{"styx/go.mod": "module styx\n", "styx/styx.go": "package styx\n"}
	for k, v := range everyLaneTree {
		tree[k] = v
	}
	for _, atom := range []struct{ id, exec string }{
		{"go:vet", `"go","vet"`},
		{"go:build", `"go","build"`},
		{"go:staticcheck", `"staticcheck","-checks"`},
	} {
		engine.reset()
		engine.withTree(tree)
		wantState(t, runAtom(t, atom.id, ""), 0, atom.id+": PASS in 2 Go modules (., styx)")
		wantCalls(t, engine.chain(atom.exec, `/src/styx`, "exitCode"), []string{"withWorkdir", `path:"/src/styx"`})
		wantCalls(t, engine.chain(`"go","mod","download"`, `/src/styx`, "exitCode"), []string{"withWorkdir", `path:"/src/styx"`})

		engine.exitCodeAll(1, atom.exec, `/src/styx`)
		engine.stdoutAll("styx.go:1: a finding", atom.exec, `/src/styx`)
		wantState(t, runAtom(t, atom.id, ""), 1, "FINDINGS in 1 of 2 Go modules (styx)", "── module styx ──")
	}
}

// R20: a per-module manifest on a toolchain other than go has no module walk.
func TestAPerModuleManifestOffTheGoToolchainCannotRun(t *testing.T) {
	e, _ := catalogue.Lookup("go:vet")
	m := *e.Manifest
	m.Toolchain = "bun"
	engine.reset()
	engine.withTree(everyLaneTree)
	v := runManifest(t.Context(), newRun(dag.Directory(), "", ""), catalogue.Entry{ID: e.ID, Manifest: &m})
	wantState(t, v, 2, "per_module is wired for the go toolchain only")
}

// rust:cargo-fmt runs after `cargo fetch --locked` (CONF-5-017, DEF-B3-?), and
// reads cargo's exit in cargo's vocabulary.
func TestCargoFmtRunsAfterTheLockedFetchAndReadsCargosExit(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "rust:cargo-fmt", ""), 0, "rust:cargo-fmt: PASS")
	c := engine.chain(`"cargo","fmt","--all"`, "exitCode")
	if !strings.Contains(c, checks.ImageRust) {
		t.Errorf("cargo fmt must run in the rust lane image:\n%s", c)
	}
	wantCalls(t, c, []string{"withExec", `expect:ANY`, `args:["cargo","fmt","--all","--check"]`})
	wantCalls(t, engine.chain(`"cargo","fetch","--locked"`, "exitCode"), []string{"withExec", `expect:ANY`, `args:["cargo","fetch","--locked"]`})
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("cargo fmt must not read GATE_BASE:\n%s", c)
	}

	engine.exitCode(`"cargo","fmt","--all"`, 1)
	engine.stdout(`"cargo","fmt","--all"`, "Diff in /src/src/lib.rs")
	wantState(t, runAtom(t, "rust:cargo-fmt", ""), 1, "Diff in /src/src/lib.rs")
	// cargo answers 101 for a command that failed; that is a finding here
	engine.exitCode(`"cargo","fmt","--all"`, 101)
	wantState(t, runAtom(t, "rust:cargo-fmt", ""), 1)
	engine.exitCode(`"cargo","fmt","--all"`, 137)
	wantState(t, runAtom(t, "rust:cargo-fmt", ""), 2)
}

// The three audits ask a live advisory source: a network fault on a non-zero
// exit is a could-not-run, which verdictFor asks again past the cache.
func TestTheAuditsReadANetworkFaultAsACouldNotRun(t *testing.T) {
	for _, tc := range []struct{ id, needle, image string }{
		{"rust:cargo-audit", `"cargo","audit"`, checks.ImageRust},
		{"python:pip-audit", `"pip-audit"`, checks.ImagePython},
		{"ts:bun-audit", `"bun","audit"`, checks.ImageTS},
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		wantState(t, runAtom(t, tc.id, ""), 0, tc.id+": PASS")
		c := engine.chain(tc.needle, "exitCode")
		if !strings.Contains(c, tc.image) || strings.Contains(c, "GATE_BASE") {
			t.Errorf("%s: wrong lane or a base read:\n%s", tc.id, c)
		}
		engine.exitCode(tc.needle, 1)
		engine.stdout(tc.needle, "GHSA-xxxx high severity")
		wantState(t, runAtom(t, tc.id, ""), 1, "GHSA-xxxx")
		engine.stderr(tc.needle, "dial tcp: connection refused")
		wantState(t, runAtom(t, tc.id, ""), 2, "could not reach its advisory source")
	}
}

// bun audit names npmjs.org, from the manifest's [env] (the reasoning is in the
// manifest), and pip-audit runs the fleet's pinned auditor, not the repo's.
func TestTheManifestsEnvAndArgvReachTheExec(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	runAtom(t, "ts:bun-audit", "")
	wantCalls(t, engine.chain(`"bun","audit"`, "exitCode"),
		[]string{"withEnvVariable", `name:"BUN_CONFIG_REGISTRY"`, `value:"https://registry.npmjs.org/"`},
		[]string{"withExec", `expect:ANY`, `args:["bun","audit","--audit-level=high"]`})

	engine.reset()
	engine.withTree(everyLaneTree)
	runAtom(t, "python:pip-audit", "")
	c := engine.chain(`"pip-audit"`, "exitCode")
	if !strings.Contains(c, `"uv","run","--with","`+checks.PythonWith("pip-audit")+`","pip-audit"`) || strings.Contains(c, "@latest") {
		t.Errorf("pip-audit runs the fleet's pinned auditor:\n%s", c)
	}
}

// DEF-B3-009: a manifest whose row had NeedsManifest keeps its absent sentence.
func TestPipAuditStandsDownWithoutAPyprojectInTheManifestsWords(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"x.py": ""})
	v := checks.AbsentVerdict(checks.AtomByID("python:pip-audit"))
	if v.Result != "absent" || !strings.Contains(v.Reason, "no pyproject.toml at the repository root") {
		t.Errorf("%+v", v)
	}
}

// IT-132 / UT-495: a logic atom is compiled from its file with the stub and the
// reproducible flags, in the go toolchain, from the atom's own source, and run
// in the lane it ran in with ATOM_OUT naming its verdict.
func TestIT_132_ALogicAtomIsCompiledFromItsFileAndRunInItsLane(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.contents("go__gofmt.json", `{"atom":"go:gofmt","stage":"precommit","lane":"go","state":1,"result":"findings","reason":"go:gofmt: FINDINGS (exit 1)\nmain.go","logs":["main.go"]}`)
	v := runAtom(t, "go:gofmt", "")
	wantState(t, v, 1, "go:gofmt: FINDINGS (exit 1)")
	if v.Atom != "go:gofmt" || !slices.Equal(v.Logs, []string{"main.go"}) {
		t.Errorf("%+v", v)
	}

	build := engine.chain(`"go","build","-trimpath"`, "exitCode")
	if !strings.Contains(build, checks.ImageGo) {
		t.Errorf("an atom is compiled in the go toolchain (UT-499):\n%s", build)
	}
	wantCalls(t, build,
		[]string{"withEnvVariable", `name:"CGO_ENABLED"`, `value:"0"`},
		[]string{"withExec", `expect:ANY`, `"-trimpath","-buildvcs=false","-ldflags=-s -w -buildid=","-o","/out/atom","./_atoms/go/gofmt"`},
		[]string{"withEnvVariable", `name:"GOFLAGS"`, `value:"-mod=mod"`},
		[]string{"withEnvVariable", `name:"GOARCH"`, `value:"amd64"`})
	stub := engine.chain("withNewFile", "_atoms/go/gofmt/main.go")
	if !strings.Contains(stub, "atomkit.Main(atom.Def)") || !strings.Contains(stub, "dagger/foundry-tools/atoms/go/gofmt") {
		t.Errorf("the stub is atomkit.Stub of the atom's package:\n%s", stub)
	}
	for _, in := range atomStubInclude("dagger/foundry-tools/atoms/go/gofmt") {
		if !strings.Contains(stub, in) {
			t.Errorf("the compile's source filter lacks %s:\n%s", in, stub)
		}
	}

	run := engine.chain("go__gofmt.json", "contents")
	if !strings.Contains(run, checks.ImageGo) {
		t.Errorf("go:gofmt runs in the go lane it ran in:\n%s", run)
	}
	wantCalls(t, run,
		[]string{"withEnvVariable", `name:"ATOM_OUT"`, `value:"/work/out/go__gofmt.json"`},
		[]string{"withFile", `path:"/atom"`},
		[]string{"withExec", `expect:ANY`, `args:["/atom","/src"]`})
	if strings.Contains(run, "GATE_BASE") || strings.Contains(run, "ATOM_BASE") {
		t.Errorf("an atom that does not judge the change is not handed the pull's base:\n%s", run)
	}
}

// IT-134 / UT-497: a file that does not compile is the atom's could-not-run,
// and the tail of the compiler's stderr is the reason.
func TestIT_134_AnAtomThatDoesNotCompileIsACouldNotRun(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"-trimpath"`, 1)
	engine.stderr(`"-trimpath"`, "atoms/go/gofmt/gofmt.go:3:6: syntax error: unexpected (")
	wantState(t, runAtom(t, "go:gofmt", ""), 2, "atom go:gofmt did not compile: ", "syntax error")
	if engine.chain("go__gofmt.json") != "" {
		t.Error("the atom ran after it failed to compile")
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"-trimpath"`, "the toolchain image would not pull")
	wantState(t, runAtom(t, "go:gofmt", ""), 2, "atom go:gofmt did not compile: ", "would not pull")
}

// UT-498: an atom that wrote no verdict could not judge: the exit it left and
// its last line are the reason. A verdict that is not JSON is refused likewise.
func TestUT_498_AnAtomThatWritesNoVerdictIsACouldNotRun(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"/atom","/src"`, 3)
	engine.stderr(`"/atom","/src"`, "warming up\nbye")
	wantState(t, runAtom(t, "go:gofmt", ""), 2, "exit 3: bye")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.contents("go__gofmt.json", "not json")
	wantState(t, runAtom(t, "go:gofmt", ""), 2, "not atom-verdict@1")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"/atom","/src"`, "exit code: 1: mount would not evaluate")
	wantState(t, runAtom(t, "go:gofmt", ""), 2, "the atom never ran", "mount would not evaluate")
}

// An environment may hand the atom the change's facts, and an atom's needs
// mount foundry-dies and the SPIRE socket; an atom that asks for none gets none.
func TestAnEnvironmentHandsAnAtomWhatItAsksFor(t *testing.T) {
	old, had := atomEnvironments["go:gofmt"]
	t.Cleanup(func() {
		if had {
			atomEnvironments["go:gofmt"] = old
		} else {
			delete(atomEnvironments, "go:gofmt")
		}
	})
	atomEnvironments["go:gofmt"] = atomEnv{container: func(r *run) *dagger.Container { return r.lane(checks.ImageGo) }, facts: true}
	e := catalogue.Entry{ID: "go:gofmt", ImportPath: "dagger/foundry-tools/atoms/go/gofmt",
		Def: &atomkit.Def{ID: "go:gofmt", Needs: []string{"dies", "svid"}}}

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.contents("go__gofmt.json", `{"atom":"go:gofmt","state":0,"result":"pass","logs":[]}`)
	r := newRun(dag.Directory(), "rob/nyx", "abc123").withSpire(dag.LoadSocketFromID("spire-agent-socket"))
	wantState(t, runAtomOnDagger(t.Context(), r, e), 0)
	run := engine.chain("go__gofmt.json", "contents")
	wantCalls(t, run,
		[]string{"withEnvVariable", `name:"ATOM_BASE"`, `value:"abc123"`},
		[]string{"withEnvVariable", `name:"ATOM_ORIGIN"`, `value:"rob/nyx"`},
		[]string{"withEnvVariable", `name:"ATOM_FORK"`},
		[]string{"withEnvVariable", `name:"GATE_BASE"`, `value:"abc123"`},
		[]string{"withMountedDirectory", `path:"/dies"`},
		[]string{"withEnvVariable", `name:"ATOM_DIES"`, `value:"/dies"`},
		[]string{"withUnixSocket", `path:"` + atomsSpirePath + `"`},
		[]string{"withEnvVariable", `name:"SPIFFE_ENDPOINT_SOCKET"`, `value:"unix://` + atomsSpirePath + `"`},
	)
}

// UT-500's other half: the stub's source list is the atom's directory, the
// module files and what a stub imports: no more (an edit to another atom would
// rebuild this one) and no less (the engine's build would fail where no unit
// test can see it).
func TestAtomStubSourceCoversImports(t *testing.T) {
	for _, e := range catalogue.Entries() {
		if e.Def == nil {
			continue
		}
		own := strings.TrimPrefix(e.ImportPath, "dagger/foundry-tools/")
		wanted := importClosure(t, own) // every atom imports atomkit, which the stub imports
		var got []string
		for _, in := range atomStubInclude(e.ImportPath) {
			dir, ok := strings.CutSuffix(in, "/**")
			switch {
			case !ok && in != "go.mod" && in != "go.sum":
				t.Errorf("%s: %q is neither a module file nor a directory", e.ID, in)
			case ok && dir != own:
				got = append(got, dir)
			}
		}
		under := func(dir, root string) bool { return dir == root || strings.HasPrefix(dir, root+"/") }
		for _, w := range wanted {
			if !slices.ContainsFunc(got, func(g string) bool { return under(w, g) }) {
				t.Errorf("%s: the stub imports %s and the source list omits it: %v", e.ID, w, got)
			}
		}
		for _, g := range got {
			if !slices.ContainsFunc(wanted, func(w string) bool { return under(w, g) }) {
				t.Errorf("%s: the source list names %s, which the stub does not import", e.ID, g)
			}
		}
	}
}

// A tools.toml that does not parse is the command atom's could-not-run, said
// before any container is built.
func TestAToolsFileThatDoesNotParseIsACouldNotRun(t *testing.T) {
	old := toolsSource
	t.Cleanup(func() { toolsSource, toolsOnce, toolsData, toolsErr = old, sync.Once{}, atomkit.ToolsFile{}, nil })
	toolsSource, toolsOnce = []byte("schema = \"tools@9\"\n"), sync.Once{}
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "go:vet", ""), 2, "tools.toml: ")
	if len(engine.chains()) != 0 {
		t.Errorf("a container was built: %v", engine.chains())
	}
}

// Catalogue lists the atoms that live in files beside the table's.
func TestCatalogueListsThePortedAtoms(t *testing.T) {
	out, err := (&FoundryTools{}).Catalogue(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range catalogue.Entries() {
		if !strings.Contains(out, `"atom": "`+e.ID+`"`) {
			t.Errorf("%s is not in the catalogue listing", e.ID)
		}
	}
}
