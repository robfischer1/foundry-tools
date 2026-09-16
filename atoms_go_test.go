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
		[]string{"withMountedCache", `path:"/opt/go-build-cache"`},
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
	v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "go:vet")
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

// THE RECORD SAYS POSTGRES, THE TREE SAYS WHICH TAGS, THE LANE BRINGS THE
// DATABASE (checks/testdb.go). Before this the DB-gated suites never compiled
// and every DB-touching line read NOT COVERED (foundry-tools#8608).
func TestGoTestRaceBringsTheRecordsPostgres(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{
		".copier-answers.yml":           "service_name: x\n",
		"/dies/fleet/stars/x/slag.json": `{"backends":{"postgres":{"cnpg_cluster":"x-db","database":"x","owner":"x"}}}`,
	})
	engine.stdout(`"go","list"`, "11\n")
	engine.stdout(`"grep","-rhoE"`, "//go:build live_db\n//go:build live_db_novector\n//go:build integration\n")

	wantState(t, runAtom(t, "go:test-race", ""), 0,
		"live_db → TEST_DATABASE_URL", "live_db_novector → TEST_NOVECTOR_DATABASE_URL", "uncompiled", "integration")
	c := engine.chain(`"go","test","-race"`, "exitCode")
	wantCalls(t, c,
		[]string{"withServiceBinding", `alias:"db"`},
		[]string{"withServiceBinding", `alias:"db-novector"`},
		[]string{"withEnvVariable", `name:"TEST_DATABASE_URL"`, `value:"` + checks.TestDBs[0].DSN() + `"`},
		[]string{"withEnvVariable", `name:"TEST_NOVECTOR_DATABASE_URL"`, `value:"` + checks.TestDBs[1].DSN() + `"`},
		// The vocabulary's two tags, in its order, and one database at a time.
		[]string{"withExec", `expect:ANY`, `args:["go","test","-race","-tags","live_db,live_db_novector","-p","1","./..."]`},
	)
	// The servers are the pinned images, run as services on their port.
	for _, img := range []string{checks.ImagePgvector, checks.ImagePostgres} {
		if engine.chain(img, "asService") == "" {
			t.Errorf("no service chain runs %s:\n%v", img, engine.chains())
		}
	}
	if !hasCall(engine.chain(checks.ImagePgvector, "asService"), "withExposedPort", "5432") {
		t.Errorf("the pgvector service must expose 5432:\n%v", engine.chains())
	}
	// The tag enumeration is one grep, in the lane, off the tree — not a
	// judgement: the exec runs under ANY because "no match" is exit 1.
	if !hasCall(engine.chain(`"grep","-rhoE"`, "exitCode"), "withExec", "expect:ANY", `--include=*_test.go`) {
		t.Errorf("the build-tag read must tolerate grep's exit 1:\n%v", engine.chains())
	}

	// NO BACKEND IN THE RECORD: nothing is bound, the tags stay uncompiled,
	// and the scope line says why.
	engine.withTree(map[string]string{"/dies/fleet/stars/x/slag.json": `{"backends":{}}`})
	wantState(t, runAtom(t, "go:test-race", ""), 0, "test databases: none", "no postgres backend")
	c = engine.chain(`"go","test","-race"`, "exitCode")
	if hasCall(c, "withServiceBinding") || !hasCall(c, "withExec", `args:["go","test","-race","./..."]`) {
		t.Errorf("a record without postgres binds nothing and compiles no tag:\n%s", c)
	}

	// A BACKEND BUT NO TAGGED SUITE: nothing to bind, said.
	engine.withTree(map[string]string{"/dies/fleet/stars/x/slag.json": `{"backends":{"postgres":{}}}`})
	engine.stdout(`"grep","-rhoE"`, "")
	engine.exitCode(`"grep","-rhoE"`, 1)
	wantState(t, runAtom(t, "go:test-race", ""), 0, "test databases: none", "no test file sits behind a tag")

	// NO RECORD AT ALL — a star the dies do not know — is none, not an error.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{
		".copier-answers.yml":           "service_name: nobody\n",
		"/dies/fleet/stars/x/slag.json": `{"backends":{"postgres":{}}}`, // someone else's
	})
	engine.stdout(`"go","list"`, "11\n")
	wantState(t, runAtom(t, "go:test-race", ""), 0, "no record at fleet/stars/nobody")
}

// The go:mutation needles, each in one exec's chain.
const (
	goMutantsNeedle = `"--output","mutation-go.json"`
	goCanaryNeedle  = `"--workers","1"`
	goDiffNeedle    = `"git","diff","--relative"`
	goReportRead    = `file(path:"/src/mutation-go.json"){contents}`
	// goCleanReport is a report where every mutant was killed.
	goCleanReport = `{"elapsed_time":1,"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"KILLED","line":1,"column":1}]}]}`
)

// scriptGoMutation answers a pull that changed Go, with a canary that honestly
// survives and a clean report.
func scriptGoMutation(tree map[string]string) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"/src/mutation-go.json": goCleanReport})
	engine.withTree(tree)
	engine.stdout(goDiffNeedle, "a.go\n")
	engine.stdout(goCanaryNeedle, "Killed: 0, Lived: 1, Not covered: 0\n")
}

func TestGoMutationCompilesTheRecordsDBTags(t *testing.T) {
	scriptGoMutation(map[string]string{
		".copier-answers.yml":           "service_name: x\n",
		"/dies/fleet/stars/x/slag.json": `{"backends":{"postgres":{}}}`,
	})
	engine.stdout(`"grep","-rhoE"`, "//go:build live_db\n")
	// A pass keeps no output (checks.VerdictOf); the scope line is set on
	// the verdict and survives it.
	wantState(t, runAtom(t, "go:mutation", "abc123"), 0, "live_db → TEST_DATABASE_URL")
	c := engine.chain(goMutantsNeedle, "exitCode")
	wantCalls(t, c,
		[]string{"withServiceBinding", `alias:"db"`},
		[]string{"withEnvVariable", `name:"TEST_DATABASE_URL"`},
		// the coverage run compiles the tag and serialises on the one database
		[]string{"withExec", `"-coverprofile","mutation-cover.out","-tags","live_db","-p","1","./..."`},
		[]string{"withExec", `"--tags","live_db"`},
	)
	if hasCall(c, "withServiceBinding", `alias:"db-novector"`) {
		t.Errorf("only the tags the tree carries are bound:\n%s", c)
	}

	// NO BACKEND, NO TAGS AT ALL — not an empty one: an empty -tags word is
	// still `-tags ""`, and `-p 1` on a suite that shares no database.
	engine.withTree(map[string]string{"/dies/fleet/stars/x/slag.json": `{"backends":{}}`})
	wantState(t, runAtom(t, "go:mutation", "abc123"), 0, "test databases: none")
	c = engine.chain(goMutantsNeedle, "exitCode")
	if strings.Contains(c, `"-tags"`) || strings.Contains(c, `"--tags"`) || hasCall(c, "withServiceBinding") {
		t.Errorf("a record without postgres sets no tags and binds nothing:\n%s", c)
	}
}

func TestGoStaticcheckAndGovulncheckUseTheBakedBinaries(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "go:staticcheck", ""), 0)
	c := engine.chain(`"staticcheck","-checks"`, "exitCode")
	// staticcheck is provisioned ONCE, pinned, by the lane — never by the
	// atom, and never at @latest.
	if n := strings.Count(c, `"go","install"`); n != 3 || !strings.Contains(c, checks.StaticcheckModule) || strings.Contains(c, "@latest") {
		t.Errorf("the lane provisions gremlins, staticcheck and govulncheck at their pins, and nothing else installs:\n%s", c)
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

// The gate measures the pull's diff in Go: the neutral config, the canary, the
// bound through the environment, the fleet's exclusions and the base — no
// script, no shell, no foundry-stocks mount.
func TestGoMutationMeasuresTheDiffAndSettlesInGo(t *testing.T) {
	scriptGoMutation(nil)
	wantState(t, runAtom(t, "go:mutation", "abc123"), 0)
	c := engine.chain(goMutantsNeedle, "exitCode")
	wantCalls(t, c,
		[]string{"withEnvVariable", `name:"GATE_BASE"`, `value:"abc123"`},
		[]string{"withMountedDirectory", `path:"/dies"`},
		[]string{"withNewFile", `path:"/tmp/mutation/gremlins-neutral.yaml"`},
		[]string{"withExec", `"go","test","-cover","-coverprofile","mutation-cover.out","./..."`},
		[]string{"withEnvVariable", `name:"GOMAXPROCS"`, `value:"1"`},
		[]string{"withEnvVariable", `name:"GOFLAGS"`, `value:"-p=1"`},
		[]string{"withExec", `expect:ANY`, `"gremlins","unleash","--config","/tmp/mutation/gremlins-neutral.yaml","--output","mutation-go.json","--timeout-coefficient","10","--workers","4","--exclude-files","` + strings.ReplaceAll(goMutationExclude, `\`, `\\`) + `","--diff","abc123","."`},
	)
	if strings.Contains(c, `path:"/stocks"`) || strings.Contains(c, `"bash"`) {
		t.Errorf("the gate mounted foundry-stocks or ran bash:\n%s", c)
	}
	wantCalls(t, engine.chain(goCanaryNeedle, "stdout"),
		[]string{"withNewFile", `path:"/tmp/mutation/canary/go.mod"`},
		[]string{"withNewFile", `path:"/tmp/mutation/canary/canary_test.go"`},
		[]string{"withWorkdir", `path:"/tmp/mutation/canary"`},
		[]string{"withExec", `"gremlins","unleash","--config","/tmp/mutation/gremlins-neutral.yaml","--timeout-coefficient","10","--workers","1","."`},
	)
}

// What the run measured decides the verdict, through checks.GoMutationVerdict.
func TestGoMutationSettlesWhatItMeasured(t *testing.T) {
	survivors := `{"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"KILLED","line":1,"column":1},{"type":"T","status":"LIVED","line":2,"column":3}]}]}`
	cases := map[string]struct {
		script func()
		state  int
		reason []string
	}{
		"every mutant caught": {nil, 0, nil},
		"a survivor": {func() { engine.withTree(map[string]string{"/src/mutation-go.json": survivors}) }, 1,
			[]string{"1 mutant(s) survived or were never covered", "a.go:2:3  LIVED"}},
		"a canary killed": {func() { engine.stdout(goCanaryNeedle, "Killed: 1, Lived: 0, Not covered: 0\n") }, 2,
			[]string{"the harness scores unrun tests as kills"}},
		// a canary that could not build is a control that could not control,
		// and a clean report still stands on its own
		"a canary that does not build": {func() { engine.exitCode(`"/tmp/mutation/canary/canary_test.go"`, 1) }, 0, nil},
		"gremlins broken, no report": {func() {
			engine.exitCode(goMutantsNeedle, 3)
			engine.fail(goReportRead, "no such file")
		}, 2, []string{"gremlins exited 3 and wrote no mutation-go.json"}},
		"nothing to report": {func() { engine.fail(goReportRead, "no such file") }, 0,
			nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			scriptGoMutation(nil)
			if c.script != nil {
				c.script()
			}
			wantState(t, runAtom(t, "go:mutation", "abc123"), c.state, c.reason...)
		})
	}
	// A canary that did not build never runs gremlins.
	scriptGoMutation(nil)
	engine.exitCode(`"/tmp/mutation/canary/canary_test.go"`, 1)
	runAtom(t, "go:mutation", "abc123")
	if engine.chain(goCanaryNeedle) != "" {
		t.Error("gremlins ran a canary whose tests do not build")
	}
}

// A pull with nothing to scope to stands down clean, and never mutates the
// whole module; what the gate cannot evaluate is could-not-run.
func TestGoMutationStandsDownOrCannotRun(t *testing.T) {
	const baseNeedle = `"git","rev-parse","--verify","--quiet","abc123^{commit}"`
	cases := map[string]struct {
		base   string
		script func()
		state  int
		// reason is checked only off a could-not-run: a pass keeps no output
		// (checks.VerdictOf), so a stand-down is told apart by what it ran.
		reason  string
		reached []string
		never   []string
	}{
		"no base": {"", nil, 0, "", nil, []string{baseNeedle, goDiffNeedle}},
		"a base the history lacks": {"abc123", func() { engine.exitCode(baseNeedle, 1) }, 0, "",
			[]string{baseNeedle}, []string{goDiffNeedle}},
		"no Go changed": {"abc123", func() { engine.stdout(goDiffNeedle, "") }, 0, "",
			[]string{goDiffNeedle}, []string{`"-coverprofile"`}},
		"git cannot diff": {"abc123", func() { engine.exitCode(goDiffNeedle, 1) }, 2,
			"git could not diff the pull against its base abc123", []string{goDiffNeedle}, []string{`"-coverprofile"`}},
		"the base check never ran": {"abc123", func() { engine.fail(baseNeedle, "engine gone") }, 2, "never ran", nil, []string{goDiffNeedle}},
		"the diff never ran":       {"abc123", func() { engine.fail(goDiffNeedle, "engine gone") }, 2, "never ran", nil, []string{`"-coverprofile"`}},
		"coverage never ran": {"abc123", func() {
			engine.failLeaf(`"-coverprofile","mutation-cover.out","./..."`, "exitCode", "engine gone")
		}, 2, "never ran", nil, nil},
		"gremlins never ran": {"abc123", func() { engine.failLeaf(goMutantsNeedle, "exitCode", "engine gone") }, 2, "never ran", nil, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			scriptGoMutation(nil)
			if c.script != nil {
				c.script()
			}
			if c.reason == "" {
				wantState(t, runAtom(t, "go:mutation", c.base), c.state)
			} else {
				wantState(t, runAtom(t, "go:mutation", c.base), c.state, c.reason)
			}
			if c.state == 0 && engine.chain(goMutantsNeedle) != "" {
				t.Errorf("a stand-down ran gremlins")
			}
			for _, n := range c.reached {
				if engine.chain(n) == "" {
					t.Errorf("never reached %s", n)
				}
			}
			for _, n := range c.never {
				if engine.chain(n) != "" {
					t.Errorf("went on to %s", n)
				}
			}
		})
	}
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

// ── every module, not only the root's (2026-09-16, Rob: fleet wide) ─────────

// nestedTree is foundry-stocks' shape: no root go.mod, a module one directory
// down, and a vendored go.mod that must not count.
var nestedTree = map[string]string{
	"tools/forge/go.mod":          "module forge\n\ngo 1.26\n",
	"tools/forge/main.go":         "package main\n",
	"tools/forge/main_test.go":    "package main\n",
	"tools/forge/vendor/x/go.mod": "module x\n",
	"bases/x/testdata/go.mod":     "module fixture\n",
	"README.md":                   "",
}

func TestGoLaneRunsInANestedModuleWithNoRootGoMod(t *testing.T) {
	engine.reset()
	engine.withTree(nestedTree)
	v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "go:vet")
	if err != nil {
		t.Fatal(err)
	}
	wantState(t, v, 0, "go:vet: PASS in 1 Go module (tools/forge)")
	c := engine.chain(`"go","vet"`, "exitCode")
	wantCalls(t, c,
		[]string{"withWorkdir", `path:"/src/tools/forge"`},
		[]string{"withExec", `args:["go","mod","download"]`},
		[]string{"withExec", `expect:ANY`, `args:["go","vet","./..."]`},
	)
	if strings.Contains(c, "vendor") || strings.Contains(c, "testdata") {
		t.Errorf("a vendored or testdata go.mod is not a module:\n%s", c)
	}
	globs := 0
	for _, q := range engine.chains() {
		if strings.Contains(q, "glob(") {
			globs++
		}
	}
	if globs != 1 {
		t.Errorf("the lane check and the atom must share one read of the module list, got %d globs", globs)
	}
}

func TestGoLaneIsAbsentWhenEveryGoModIsOneGoWouldNotBuild(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"pyproject.toml": "", "vendor/x/go.mod": "", "a/testdata/go.mod": "", "_old/go.mod": "", ".tools/go.mod": ""})
	v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "go:build")
	if err != nil {
		t.Fatal(err)
	}
	if v.Result != "absent" || !strings.Contains(v.Reason, "no go.mod anywhere in the tree") {
		t.Errorf("want absent, got %+v", v)
	}
	if engine.chain(`"go","build"`) != "" {
		t.Error("an absent lane builds no container")
	}
}

// A root module and a nested one each get their own run, and a finding in
// either is the atom's.
func TestGoAtomsRunInEveryModuleAndAFindingInOneIsTheAtoms(t *testing.T) {
	tree := map[string]string{}
	for k, v := range everyLaneTree {
		tree[k] = v
	}
	tree["styx/go.mod"] = "module styx\n"
	tree["styx/styx.go"] = "package styx\n"

	for _, atom := range []struct{ id, exec string }{
		{"go:vet", `"go","vet"`},
		{"go:build", `"go","build"`},
		{"go:staticcheck", `"staticcheck","-checks"`},
		{"go:govulncheck", `"govulncheck","./..."`},
	} {
		engine.reset()
		engine.withTree(tree)
		wantState(t, runAtom(t, atom.id, ""), 0, atom.id+": PASS in 2 Go modules (., styx)")
		roots := 0
		for _, q := range engine.chains() {
			if strings.Contains(q, atom.exec) && strings.Contains(q, "exitCode") && !strings.Contains(q, `/src/styx`) {
				roots++
			}
		}
		if roots != 1 {
			t.Errorf("%s: want one run at the root, in /src alone; got %d", atom.id, roots)
		}
		wantCalls(t, engine.chain(atom.exec, `/src/styx`, "exitCode"), []string{"withWorkdir", `path:"/src/styx"`})

		engine.exitCode(`/src/styx`, 1)
		engine.stdout(`/src/styx`, "styx.go:1: a finding")
		wantState(t, runAtom(t, atom.id, ""), 1, "FINDINGS in 1 of 2 Go modules (styx)", "── module styx ──")
	}
}

func TestGoTestRaceCountsAndRunsEachModule(t *testing.T) {
	engine.reset()
	engine.withTree(nestedTree)
	engine.stdout(`"go","list"`, "10\n")
	wantState(t, runAtom(t, "go:test-race", ""), 0, "PASS in 1 Go module (tools/forge)")
	c := engine.chain(`"go","test","-race"`, "exitCode")
	wantCalls(t, c,
		[]string{"withMountedDirectory", `path:"/dies"`},
		[]string{"withWorkdir", `path:"/src/tools/forge"`},
		[]string{"withExec", `args:["go","mod","download"]`},
	)

	engine.stdout(`"go","list"`, "00\n")
	wantState(t, runAtom(t, "go:test-race", ""), 1, "no test file in any package", "── module tools/forge ──")
}

// gremlins in a nested module reads its diff relative to the module, and the
// report and profile are read where the module wrote them.
func TestGoMutationInANestedModuleDiffsRelativeToIt(t *testing.T) {
	engine.reset()
	engine.withTree(nestedTree)
	engine.withTree(map[string]string{"/src/tools/forge/mutation-go.json": goCleanReport})
	engine.stdout(goDiffNeedle, "internal/oci/oci.go\n")
	engine.stdout(goCanaryNeedle, "Killed: 0, Lived: 1, Not covered: 0\n")
	wantState(t, runAtom(t, "go:mutation", "abc123"), 0, "PASS in 1 Go module (tools/forge)")
	c := engine.chain(goMutantsNeedle, "exitCode")
	wantCalls(t, c,
		[]string{"withWorkdir", `path:"/src/tools/forge"`},
		[]string{"withEnvVariable", `name:"GIT_CONFIG_COUNT"`, `value:"1"`},
		[]string{"withEnvVariable", `name:"GIT_CONFIG_KEY_0"`, `value:"diff.relative"`},
		[]string{"withEnvVariable", `name:"GIT_CONFIG_VALUE_0"`, `value:"true"`},
	)
	if !strings.Contains(strings.Join(engine.chains(), "\n"), `/src/tools/forge/mutation-go.json`) {
		t.Error("the report must be read from the module's directory")
	}
	if !strings.Contains(strings.Join(engine.chains(), "\n"), `/src/tools/forge/mutation-cover.out`) {
		t.Error("the coverage profile must be read from the module's directory")
	}

	// The report is what decides: a survivor in the module's report reds it.
	engine.withTree(map[string]string{"/src/tools/forge/mutation-go.json": `{"files":[{"file_name":"internal/oci/oci.go","mutations":[{"type":"T","status":"LIVED","line":2,"column":3}]}]}`})
	wantState(t, runAtom(t, "go:mutation", "abc123"), 1, "── module tools/forge ──")
}

func TestTheGoLaneCannotRunWhenTheTreeCannotBeEnumerated(t *testing.T) {
	engine.reset()
	engine.withTree(nestedTree)
	engine.fail("glob(", "engine went away")
	v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "go:vet")
	if err != nil {
		t.Fatal(err)
	}
	wantState(t, v, 2, "could not enumerate the tree's Go modules", "engine went away")
	wantState(t, runAtom(t, "go:build", ""), 2, "could not enumerate the tree's Go modules", "engine went away")
}

// Called without the lane check (a +check reaches the runner through
// verdictFor, but the runner does not rely on it), an atom on a tree with no
// module still stands down rather than building a container.
func TestAGoAtomOnATreeWithNoModuleIsAbsent(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"pyproject.toml": ""})
	v := runAtom(t, "go:vet", "")
	if v.Result != "absent" || engine.chain(`"go","vet"`) != "" {
		t.Errorf("want absent and no container, got %+v", v)
	}
}

func TestLanesNamesTheNestedModulesTheGateRuns(t *testing.T) {
	engine.reset()
	engine.withTree(nestedTree)
	engine.withTree(map[string]string{"package.json": "{}"})
	out, err := (&FoundryTools{Source: dag.Directory()}).Lanes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if out != "go (go.mod in tools/forge)\nts (package.json)\n" {
		t.Errorf("Lanes = %q", out)
	}
	engine.reset()
	engine.withTree(map[string]string{"README.md": ""})
	out, _ = (&FoundryTools{Source: dag.Directory()}).Lanes(t.Context())
	if !strings.Contains(out, "declares no lane") {
		t.Errorf("Lanes on a tree with no lane = %q", out)
	}
}
