package main

import (
	"fmt"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE EXEMPLAR TESTS for a lane, against the paper engine (engine_fake_test.go).
// Each atom gets: its happy path, the decision(s) it makes about the tool's
// answer, and the engine-error path. Read the chain back to hold the module's
// own rules — the image, the caches, which exec carries expect:ANY.

func TestGoVetBuildsTheModulesChainAndReadsTheExit(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)

	v := runAtom(t, "go:vet", "")
	wantState(t, v, 0)

	c := engine.chain(`"go","vet"`, "exitCode")
	if !strings.Contains(c, checks.ImageGo) {
		t.Errorf("go:vet must run in the go lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withEnvVariable", `name:"CI"`, `value:"true"`},
		[]string{"withMountedCache", `path:"/go/pkg/mod"`},
		[]string{"withMountedCache", `path:"/opt/go-build-cache"`, `source:`},
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withExec", `args:["go","mod","download"]`},
		[]string{"withExec", `expect:ANY`, `args:["go","vet","./..."]`},
	)
	if hasCall(c, "withExec", `args:["go","mod","download"]`, `expect:ANY`) {
		t.Errorf("the download is provisioning and must run under the default Expect:\n%s", c)
	}
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("go:vet must not read GATE_BASE — it would key the cache on the pull:\n%s", c)
	}

	engine.exitCode(`"go","vet"`, 1)
	engine.stdout(`"go","vet"`, "x.go:3: unreachable code")
	wantState(t, runAtom(t, "go:vet", ""), 1, "unreachable code")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"go","mod","download"`, "exit code: 1: proxy 502")
	wantState(t, runAtom(t, "go:vet", ""), 2, "never ran", "proxy 502")
}

func TestGoLaneIsAbsentWithoutAGoMod(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"pyproject.toml": ""})
	v, err := verdictFor(t.Context(), newRun(dag.Directory(), ""), "go:vet")
	if err != nil {
		t.Fatal(err)
	}
	if v.Result != "absent" || !strings.Contains(v.Reason, "no go.mod") {
		t.Errorf("want absent without go.mod, got %+v", v)
	}
	if len(engine.chains()) != 1 {
		t.Errorf("an absent lane must cost one entries read, not a container: %d queries", len(engine.chains()))
	}
}

func TestGoBuild(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "go:build", ""), 0)
	c := engine.chain(`"go","build"`, "exitCode")
	wantCalls(t, c, []string{"withExec", `args:["go","mod","download"]`}, []string{"withExec", `expect:ANY`, `args:["go","build","./..."]`})
	engine.exitCode(`"go","build"`, 1)
	wantState(t, runAtom(t, "go:build", ""), 1)
	engine.exitCode(`"go","build"`, 137)
	wantState(t, runAtom(t, "go:build", ""), 2)
}

func TestGoGofmtReadsTheListingNotTheExit(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "go:gofmt", ""), 0)
	c := engine.chain(`"gofmt","-l"`)
	if strings.Contains(c, "go\",\"mod\",\"download") {
		t.Errorf("gofmt is syntactic and must not download modules:\n%s", c)
	}
	if !strings.Contains(c, `"main.go"`) || strings.Contains(c, `vendor/`) {
		t.Errorf("gofmt must be handed the population, root files included, vendor excluded:\n%s", c)
	}

	engine.stdout(`"gofmt","-l"`, "main.go\n")
	wantState(t, runAtom(t, "go:gofmt", ""), 1, "main.go")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"gofmt","-l"`, 2)
	engine.stderr(`"gofmt","-l"`, "main.go:1:1: expected 'package'")
	wantState(t, runAtom(t, "go:gofmt", ""), 2, "expected 'package'")

	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module x\n"})
	wantState(t, runAtom(t, "go:gofmt", ""), 0)
	if engine.chain(`"gofmt"`) != "" {
		t.Errorf("no Go files means no container")
	}
}

func TestGoGofmtExcludesVendorThroughThePopulation(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module x\n", "vendor/a/b.go": "package a\n", "cmd/x.go": "package main\n"})
	wantState(t, runAtom(t, "go:gofmt", ""), 0)
	c := engine.chain(`"gofmt","-l"`)
	if !strings.Contains(c, `"cmd/x.go"`) || strings.Contains(c, "vendor") {
		t.Errorf("population must carry cmd/x.go and not vendor/:\n%s", c)
	}
}

func TestGoTestRaceCountsTestFilesBeforeRunning(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stdout(`"go","list"`, "11\n00\n")
	wantState(t, runAtom(t, "go:test-race", ""), 0)
	c := engine.chain(`"go","test","-race"`, "exitCode")
	wantCalls(t, c,
		[]string{"withMountedDirectory", `path:"/dies"`},
		[]string{"withEnvVariable", `name:"FOUNDRY_DIES"`, `value:"/dies"`},
		[]string{"withExec", `expect:ANY`, `args:["go","test","-race","./..."]`},
	)

	engine.stdout(`"go","list"`, "00\n00\n")
	wantState(t, runAtom(t, "go:test-race", ""), 1, "no test file in any package")

	engine.stdout(`"go","list"`, "11\n")
	engine.exitCode(`"go","test","-race"`, 1)
	wantState(t, runAtom(t, "go:test-race", ""), 1)

	engine.exitCode(`"go","list"`, 1)
	engine.stderr(`"go","list"`, "go: cannot find main module")
	wantState(t, runAtom(t, "go:test-race", ""), 1, "cannot be counted", "cannot find main module")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"go","list"`, "engine went away")
	wantState(t, runAtom(t, "go:test-race", ""), 2, "engine went away")
}

func TestGoStaticcheckAndGovulncheckUseTheBakedBinaries(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "go:staticcheck", ""), 0)
	c := engine.chain(`"staticcheck","-checks"`, "exitCode")
	if strings.Contains(c, "go\",\"install") {
		t.Errorf("staticcheck is baked; nothing may go install it:\n%s", c)
	}
	wantCalls(t, c, []string{"withExec", `args:["staticcheck","-version"]`}, []string{"withExec", `expect:ANY`, `-ST1023`})
	engine.exitCode(`"staticcheck","-checks"`, 1)
	wantState(t, runAtom(t, "go:staticcheck", ""), 1)

	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "go:govulncheck", ""), 0)
	c = engine.chain(`"govulncheck","./..."`, "exitCode")
	wantCalls(t, c, []string{"withExec", `args:["govulncheck","-version"]`}, []string{"withExec", `args:["go","mod","download"]`})
	// govulncheck answers 3, not 1, when it finds a vulnerability; the old
	// script's `|| exit 1` hid that, and the first typed cut filed it as
	// could-not-run — caught here, on the paper engine, 2026-09-12.
	engine.exitCode(`"govulncheck","./..."`, 3)
	wantState(t, runAtom(t, "go:govulncheck", ""), 1)
	engine.exitCode(`"govulncheck","./..."`, 2)
	wantState(t, runAtom(t, "go:govulncheck", ""), 2)
	engine.fail(`"govulncheck","-version"`, "not found")
	wantState(t, runAtom(t, "go:govulncheck", ""), 2, "not found")
}

func TestGoMutationRunsThePhasesAndReadsTheVerdictFile(t *testing.T) {
	engine.reset()
	tree := map[string]string{}
	for k, v := range everyLaneTree {
		tree[k] = v
	}
	tree["/tmp/mutation/verdict"] = "0\n"
	tree["/tmp/mutation/reason"] = "every mutant killed"
	engine.withTree(tree)
	// The verdict file is read off the scored container; the paper engine
	// serves file contents from the tree by path.
	v := runAtom(t, "go:mutation", "abc123")
	wantState(t, v, 0)
	c := engine.chain(`go.sh","score"`)
	wantCalls(t, c,
		[]string{"withEnvVariable", `name:"GATE_BASE"`, `value:"abc123"`},
		[]string{"withEnvVariable", `name:"MUT_BASE"`, `value:"abc123"`},
		[]string{"withMountedDirectory", `path:"/stocks"`},
		[]string{"withMountedDirectory", `path:"/dies"`},
	)
	for _, phase := range []string{"resolve", "setup", "cover", "mutate", "teardown", "score"} {
		if !hasCall(c, "withExec", `expect:ANY`, `"/stocks/ci/lib/mutation/go.sh","`+phase+`"`) {
			t.Errorf("go:mutation lacks phase %s under ANY:\n%s", phase, c)
		}
	}

	tree["/tmp/mutation/verdict"] = "1\n"
	tree["/tmp/mutation/reason"] = "3 mutants survived"
	engine.withTree(tree)
	wantState(t, runAtom(t, "go:mutation", "abc123"), 1, "3 mutants survived")

	engine.exitCode(`go.sh","cover"`, 1)
	wantState(t, runAtom(t, "go:mutation", "abc123"), 2, "phase cover")

	engine.reset()
	engine.withTree(everyLaneTree) // no verdict file
	wantState(t, runAtom(t, "go:mutation", "abc123"), 2, "no verdict")
}

// Rule 8: GATE_BASE reaches only the atoms that judge the change. Every other
// atom's cache key is a function of the tree alone.
func TestOnlyWitnessAndMutationReadGateBase(t *testing.T) {
	for _, a := range checks.Atoms {
		engine.reset()
		engine.withTree(everyLaneTree)
		runAtom(t, a.ID, "base-sha")
		reads := false
		for _, q := range engine.chains() {
			if strings.Contains(q, `GATE_BASE`) {
				reads = true
			}
		}
		want := a.ID == "fleet:witness" || a.Stage == checks.StageMutation
		if reads != want {
			t.Errorf("%s reads GATE_BASE=%v, want %v", a.ID, reads, want)
		}
	}
}

// Every atom answers a verdict for its own id against a tree that declares
// every lane, and never a panic — the paper engine answers exit 0 for every
// exec, so this is each atom's happy path end to end.
func TestEveryAtomAnswersForItsOwnID(t *testing.T) {
	for _, a := range checks.Atoms {
		engine.reset()
		engine.withTree(everyLaneTree)
		v := runAtom(t, a.ID, "")
		if v.Atom != a.ID {
			t.Errorf("%s answered as %s", a.ID, v.Atom)
		}
		if v.Stage != a.Stage || v.Lane != string(a.Lane) {
			t.Errorf("%s: verdict carries stage %q lane %q", a.ID, v.Stage, v.Lane)
		}
	}
}

// Rule 7, on the wire: no exec anywhere runs a shell.
func TestNoAtomExecsAShell(t *testing.T) {
	for _, a := range checks.Atoms {
		engine.reset()
		engine.withTree(everyLaneTree)
		runAtom(t, a.ID, "")
		for _, q := range engine.chains() {
			if strings.Contains(q, `args:["sh","-c"`) || strings.Contains(q, `args:["bash","-c"`) {
				t.Errorf("%s execs a shell:\n%s", a.ID, q)
			}
		}
	}
}

func TestGoMutationRefusesWithoutItsScriptAndOnAnEngineError(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`path:"ci/lib/mutation/go.sh"`, "no such file")
	wantState(t, runAtom(t, "go:mutation", "abc"), 2, "did not mount at its one home")
	if engine.chain(`go.sh","resolve"`) != "" {
		t.Errorf("a missing script must stop the atom before any phase runs")
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`go.sh","resolve"`, "engine gone")
	wantState(t, runAtom(t, "go:mutation", "abc"), 2, "never ran", "engine gone")
}

func TestGoGofmtHandsAHugePopulationInAsAFile(t *testing.T) {
	engine.reset()
	tree := map[string]string{"go.mod": "module x\n"}
	for i := 0; i < 2000; i++ {
		tree[fmt.Sprintf("pkg%04d/a_rather_long_file_name_to_fill_the_argv_budget_quickly_%04d.go", i, i)] = "package p\n"
	}
	engine.withTree(tree)
	wantState(t, runAtom(t, "go:gofmt", ""), 0)
	c := engine.chain(`"xargs","-0","-a"`)
	if c == "" || !hasCall(c, "withNewFile", `path:"`+gofmtArgFile+`"`) || !hasCall(c, "withExec", `expect:ANY`, `"gofmt","-l"`) {
		t.Errorf("a population over the argv budget goes in as a NUL file through xargs:\n%s", c)
	}
	engine.stdout(`"xargs","-0","-a"`, "pkg0001/x.go\n")
	wantState(t, runAtom(t, "go:gofmt", ""), 1, "pkg0001/x.go")
}
