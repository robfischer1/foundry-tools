package main

import (
	"context"
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

// THE COMMIT'S SUITE IS THE UNIT SUITE: the same packages, no race detector,
// no database binding and no build tags — so a DB-gated suite does not even
// compile in. The push's go:test-race is the one that brings both.
func TestGoTestIsTheUnitSuiteWithoutRaceOrADatabase(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{
		".copier-answers.yml":           "service_name: x\n",
		"/dies/fleet/stars/x/slag.json": `{"backends":{"postgres":{"cnpg_cluster":"x-db","database":"x","owner":"x"}}}`,
	})
	engine.stdout(`"go","list"`, "11\n")
	wantState(t, runAtom(t, "go:test", ""), 0, "unit suite")
	c := engine.chain(`"go","test","./..."`, "exitCode")
	wantCalls(t, c, []string{"withExec", `expect:ANY`, `args:["go","test","./..."]`})
	if hasCall(c, "withServiceBinding") {
		t.Errorf("the commit's suite binds no database:\n%s", c)
	}
	// It counts tests exactly as the push's run does.
	engine.stdout(`"go","list"`, "00\n00\n")
	wantState(t, runAtom(t, "go:test", ""), 1, "no test file in any package")
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
	// go:test-race KEEPS THE SHARED SERVER — it runs in the sequence, so it
	// contends with nothing. Its service must carry no lane word, or it would be
	// a third database nobody asked for.
	if hasCall(engine.chain(checks.ImagePgvector, "asService"), "withEnvVariable", `name:"FOUNDRY_TEST_DB_LANE"`) {
		t.Error("go:test-race took a lane-scoped server; it shares by design")
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
	// THE TWO CONTROLS BOTH RUN `--workers","1"`, which is why neither needle is
	// that argv: it would match both chains and chain() answers the newest. The
	// workdir is unique per control, and the closing quote keeps each needle off
	// the withNewFile paths under it.
	goCanaryNeedle     = `path:"/tmp/mutation/canary"`
	goMainCanaryNeedle = `path:"/tmp/mutation/maincanary"`
	// goMainListNeedle is the `go list` that names which files are in a
	// `package main` — the set the package-main control governs.
	goMainListNeedle = `"go","list","-e","-f"`
	// goClassifyNeedle is the testkit's gate, run in the lane after gremlins
	// for its classification of what no test could kill.
	goClassifyNeedle = `"mutation-gate","-report","mutation-go.json","-C",".","-json"`
	goDiffNeedle     = `"git","diff","--relative"`
	goReportRead     = `file(path:"/src/mutation-go.json"){contents}`
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
	engine.stdout(mergeBaseNeedle, sinceSha+"\n")
	engine.stdout(goDiffNeedle, "a.go\n")
	engine.stdout(goCanaryNeedle, "Killed: 0, Lived: 1, Not covered: 0\n")
	// THE PACKAGE-MAIN CONTROL ANSWERS BROKEN BY DEFAULT, because that is what
	// gremlins 0.6.0 answers: the lane's ordinary state is one working harness
	// and one bug it is working around. A test that wants the other world says
	// so (TestGoMutationDoesNotCountAMainPackageItCannotGrade).
	engine.stdout(goMainCanaryNeedle, "Killed: 1, Lived: 0, Not covered: 0\n")
	// The classifier answered, and named nothing: every survivor is real.
	engine.stdout(goClassifyNeedle, `{"noise":[]}`+"\n")
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
	// THE MISGRADED-FILE LISTING IS TAGGED LIKE THE COVER STEP. A build-tagged
	// main file is placed by the tags in force, so an untagged `go list` would
	// leave it out of the set and its mutants would be trusted. Asserted on THIS
	// run's chain, not in a defer: this test runs the atom twice and the second
	// run carries no record, so the newest listing is the untagged one.
	if list := engine.chain(goMainListNeedle, "stdout"); !strings.Contains(list, `"-tags","live_db"`) {
		t.Errorf("the misgraded-file listing dropped the record's build tags:\n%s", list)
	}
	c := engine.chain(goMutantsNeedle, "exitCode")
	wantCalls(t, c,
		// ITS OWN SERVER, not the one go:test-race binds. This lane runs BESIDE
		// the complex checks, both reset the same schema per test, and dagger
		// content-addresses services — so a shared definition handed both lanes
		// one Postgres and they tore each other apart (chaos: 150/50/72 distinct
		// failures over three runs of one unchanged suite).
		[]string{"withServiceBinding", `alias:"db-mutation"`},
		[]string{"withEnvVariable", `name:"TEST_DATABASE_URL"`, `value:"` + checks.TestDBs[0].DSNFor("mutation") + `"`},
		// the coverage run compiles the tag and serialises on the one database
		// the coverage run covers the diff's package (a.go: the root) and no other
		[]string{"withExec", `"-coverprofile","mutation-cover.out","-tags","live_db","-p","1","."`},
		[]string{"withExec", `"--tags","live_db"`},
	)
	if hasCall(c, "withServiceBinding", `alias:"db-novector"`) {
		t.Errorf("only the tags the tree carries are bound:\n%s", c)
	}
	// AND IT MUST NOT REACH THE SHARED ONE. This is the assertion the defect
	// would have failed: before the lane scope, this atom bound plain `db`.
	if hasCall(c, "withServiceBinding", `alias:"db"`) {
		t.Errorf("the mutation gate bound the shared server it races:\n%s", c)
	}
	// THE SERVICE DEFINITION ITSELF CARRIES THE LANE, and that is what makes it
	// a second Postgres rather than a second name for the first: dagger
	// content-addresses services, so two identical definitions resolve to one
	// container however many aliases point at it. Asserted on the SERVICE's own
	// recorded chain, not the lane container's — the lane only ever sees an
	// opaque service id.
	svc := engine.chain(checks.ImagePgvector, "asService")
	if !hasCall(svc, "withEnvVariable", `name:"FOUNDRY_TEST_DB_LANE"`, `value:"mutation"`) {
		t.Errorf("the mutation lane's server is defined identically to the shared one:\n%s", svc)
	}

	// NO BACKEND, NO TAGS AT ALL — not an empty one: an empty -tags word is
	// still `-tags ""`, and `-p 1` on a suite that shares no database.
	engine.withTree(map[string]string{"/dies/fleet/stars/x/slag.json": `{"backends":{}}`})
	wantState(t, runAtom(t, "go:mutation", "abc123"), 0, "test databases: none")
	c = engine.chain(goMutantsNeedle, "exitCode")
	if strings.Contains(c, `"-tags"`) || strings.Contains(c, `"--tags"`) || hasCall(c, "withServiceBinding") {
		t.Errorf("a record without postgres sets no tags and binds nothing:\n%s", c)
	}
	// ...and the listing likewise: an empty `-tags ""` is not "no tags".
	if list := engine.chain(goMainListNeedle, "stdout"); strings.Contains(list, `"-tags"`) {
		t.Errorf("a record without postgres tagged the misgraded-file listing:\n%s", list)
	}
}

func TestGoStaticcheckAndGovulncheckUseTheBakedBinaries(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "go:staticcheck", ""), 0)
	c := engine.chain(`"staticcheck","-checks"`, "exitCode")
	// staticcheck is provisioned ONCE, pinned, by the lane — never by the
	// atom, and never at @latest.
	if n := strings.Count(c, `"go","install"`); n != 4 || !strings.Contains(c, checks.StaticcheckModule) || !strings.Contains(c, checks.MutationGateModule) || strings.Contains(c, "@latest") {
		t.Errorf("the lane provisions gremlins, mutation-gate, staticcheck and govulncheck at their pins, and nothing else installs:\n%s", c)
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

// The gate measures the pull's diff in Go: the canonical config, the canary, the
// bound through the environment, the fleet's exclusions and the base — no
// script, no shell, no foundry-stocks mount.
func TestGoMutationMeasuresTheDiffAndSettlesInGo(t *testing.T) {
	scriptGoMutation(nil)
	wantState(t, runAtom(t, "go:mutation", "abc123"), 0)
	c := engine.chain(goMutantsNeedle, "exitCode")
	wantCalls(t, c,
		[]string{"withEnvVariable", `name:"GATE_BASE"`, `value:"abc123"`},
		[]string{"withMountedDirectory", `path:"/dies"`},
		[]string{"withNewFile", `path:"/tmp/mutation/gremlins-canonical.yaml"`},
		[]string{"withExec", `"go","test","-cover","-coverprofile","mutation-cover.out","."`},
		[]string{"withEnvVariable", `name:"GOMAXPROCS"`, `value:"1"`},
		// -count=1: gremlins' own coverage gather is the baseline every
		// mutant's timeout is ten times, and a cached one is ~1s for a module
		// that tests in minutes (ourea aeb9cd9: killed 72, TIMED OUT 79).
		[]string{"withEnvVariable", `name:"GOFLAGS"`, `value:"-p=1 -count=1"`},
		[]string{"withExec", `expect:ANY`, `"gremlins","unleash","--config","/tmp/mutation/gremlins-canonical.yaml","--output","mutation-go.json","--workers","4","--exclude-files","` + strings.ReplaceAll(goMutationExclude, `\`, `\\`) + `","--diff","since0","."`},
	)
	if strings.Contains(c, `path:"/stocks"`) || strings.Contains(c, `"bash"`) {
		t.Errorf("the gate mounted foundry-stocks or ran bash:\n%s", c)
	}
	wantCalls(t, engine.chain(goCanaryNeedle, "stdout"),
		[]string{"withNewFile", `path:"/tmp/mutation/canary/go.mod"`},
		[]string{"withNewFile", `path:"/tmp/mutation/canary/canary_test.go"`},
		[]string{"withWorkdir", `path:"/tmp/mutation/canary"`},
		[]string{"withExec", `"gremlins","unleash","--config","/tmp/mutation/gremlins-canonical.yaml","--workers","1","."`},
	)
	// THE SECOND CONTROL'S SHAPE IS THE TEST. A main package AT the module root
	// is graded correctly by the gremlins this works around, so a control
	// written that way proves nothing; the main package must sit in a
	// subdirectory with NOTHING at the root. cmd/tool/main.go, and no root .go
	// file, is that shape — assert it here, because a later tidy-up that moves
	// the file to the root would leave a control that always answers OK.
	main := engine.chain(goMainCanaryNeedle, "stdout")
	wantCalls(t, main,
		[]string{"withNewFile", `path:"/tmp/mutation/maincanary/go.mod"`},
		[]string{"withNewFile", `path:"/tmp/mutation/maincanary/cmd/tool/main.go"`},
		[]string{"withNewFile", `path:"/tmp/mutation/maincanary/cmd/tool/main_test.go"`},
		[]string{"withWorkdir", `path:"/tmp/mutation/maincanary"`},
		[]string{"withExec", `"gremlins","unleash","--config","/tmp/mutation/gremlins-canonical.yaml","--workers","1","."`},
	)
	if strings.Contains(main, `path:"/tmp/mutation/maincanary/main.go"`) {
		t.Errorf("the package-main control grew a root package, which this gremlins grades correctly:\n%s", main)
	}
	// And the set that control governs is read from the toolchain, in the module
	// — `go list`, not a grep over package clauses.
	list := engine.chain(goMainListNeedle, "stdout")
	wantCalls(t, list,
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withExec", `"go","list","-e","-f"`, `"./..."`},
	)
	// NO RECORD, NO TAGS. This repo declares no databases, so the listing is
	// untagged; TestGoMutationCompilesTheRecordsDBTags holds the other side, and
	// an empty `-tags ""` would place a build-tagged main file wrongly.
	if strings.Contains(list, `"-tags"`) {
		t.Errorf("a repo with no test databases tagged its go list:\n%s", list)
	}
}

// What the run measured decides the verdict, through checks.GoMutationVerdict.
func TestGoMutationSettlesWhatItMeasured(t *testing.T) {
	survivors := `{"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"KILLED","line":1,"column":1},{"type":"T","status":"LIVED","line":2,"column":3}]}]}`
	declaration := `{"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"KILLED","line":1,"column":1},{"type":"ARITHMETIC_BASE","status":"NOT COVERED","line":3,"column":16}]}]}`
	declarationAndSurvivor := `{"files":[{"file_name":"a.go","mutations":[{"type":"T","status":"LIVED","line":2,"column":3},{"type":"ARITHMETIC_BASE","status":"NOT COVERED","line":3,"column":16}]}]}`
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
		// THE CLASS THIS RUN EXISTS FOR. A mutant in a top-level declaration
		// has no coverage block, so gremlins says NOT COVERED forever; the
		// testkit's gate reads the source in the lane and names it. Forgiven
		// is a pass that SAYS what it set aside, and a survivor the classifier
		// did not name is still a survivor.
		"a declaration the classifier forgave": {func() {
			engine.withTree(map[string]string{"/src/mutation-go.json": declaration})
			engine.stdout(goClassifyNeedle, `{"noise":[{"file":"a.go","line":3,"column":16,"type":"ARITHMETIC_BASE","status":"NOT COVERED","noise_reason":"declaration"}]}`)
		}, 0, nil},
		"a declaration forgiven beside a real survivor": {func() {
			engine.withTree(map[string]string{"/src/mutation-go.json": declarationAndSurvivor})
			engine.stdout(goClassifyNeedle, `{"noise":[{"file":"a.go","line":3,"column":16,"noise_reason":"declaration"}]}`)
		}, 1, []string{"1 mutant(s) survived or were never covered", "[declaration]", "a.go:2:3  LIVED"}},
		// A CLASSIFIER THAT DID NOT ANSWER IS NOT A CLASSIFIER THAT SAID
		// "NOTHING". Scored without it, a clean pull is red for a reason no
		// test can fix; so a survivor with no classification is "could not
		// measure", carrying what the gate said, and a clean run is still
		// clean — there was nothing to classify.
		"a survivor and the classifier said nothing": {func() {
			engine.withTree(map[string]string{"/src/mutation-go.json": survivors})
			engine.stdout(goClassifyNeedle, "")
			engine.stderr(goClassifyNeedle, "mutation-gate: mutation-go.json: report names a.go, which is not under /src\n")
		}, 2, []string{"the unkillability classifier did not answer", "report names a.go, which is not under /src"}},
		"a survivor and the classifier wrote noise that is not JSON": {func() {
			engine.withTree(map[string]string{"/src/mutation-go.json": survivors})
			engine.stdout(goClassifyNeedle, "| killed | missed |\n")
		}, 2, []string{"the unkillability classifier did not answer", "not mutation-gate JSON"}},
		"every mutant caught and the classifier said nothing": {func() {
			engine.stdout(goClassifyNeedle, "")
		}, 0, nil},
		// The engine failing the classifier's exec is a reason the verdict
		// carries by name, never an empty answer that reads as "nothing".
		"a survivor and the engine could not run the classifier": {func() {
			engine.withTree(map[string]string{"/src/mutation-go.json": survivors})
			engine.failLeaf(goClassifyNeedle, "exitCode", "engine gone")
		}, 2, []string{"the unkillability classifier did not answer (the engine could not run mutation-gate: engine gone)"}},
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
	// The classifier runs on gremlins' container — after the mutants, on the
	// report they wrote — and a run with no report has nothing to classify.
	scriptGoMutation(nil)
	runAtom(t, "go:mutation", "abc123")
	if c := engine.chain(goClassifyNeedle); c == "" || !strings.Contains(c, goMutantsNeedle) {
		t.Errorf("mutation-gate did not run on gremlins' own container:\n%s", c)
	}
	scriptGoMutation(nil)
	engine.fail(goReportRead, "no such file")
	runAtom(t, "go:mutation", "abc123")
	if engine.chain(goClassifyNeedle) != "" {
		t.Error("mutation-gate ran with no report to read")
	}

	// A canary that did not build never runs gremlins, either control.
	for _, c := range []struct{ name, file, needle string }{
		{"the harness control", `"/tmp/mutation/canary/canary_test.go"`, goCanaryNeedle},
		{"the package-main control", `"/tmp/mutation/maincanary/cmd/tool/main_test.go"`, goMainCanaryNeedle},
	} {
		scriptGoMutation(nil)
		engine.exitCode(c.file, 1)
		runAtom(t, "go:mutation", "abc123")
		if chain := engine.chain(c.needle, `"gremlins"`); chain != "" {
			t.Errorf("gremlins ran %s, whose tests do not build:\n%s", c.name, chain)
		}
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
			[]string{baseNeedle}, []string{mergeBaseNeedle, goDiffNeedle}},
		"no merge base": {"abc123", func() { engine.exitCode(mergeBaseNeedle, 1) }, 2,
			"no merge base between the base abc123 and HEAD (exit 1)", []string{mergeBaseNeedle}, []string{goDiffNeedle}},
		"no Go changed": {"abc123", func() { engine.stdout(goDiffNeedle, "") }, 0, "",
			[]string{goDiffNeedle}, []string{`"-coverprofile"`}},
		"git cannot diff": {"abc123", func() { engine.exitCode(goDiffNeedle, 1) }, 2,
			"git could not diff the pull against its base since0", []string{goDiffNeedle}, []string{`"-coverprofile"`}},
		"the base check never ran": {"abc123", func() { engine.fail(baseNeedle, "engine gone") }, 2, "never ran", nil, []string{mergeBaseNeedle}},
		"the merge base never ran": {"abc123", func() { engine.fail(mergeBaseNeedle, "engine gone") }, 2, "never ran", nil, []string{goDiffNeedle}},
		"the diff never ran":       {"abc123", func() { engine.fail(goDiffNeedle, "engine gone") }, 2, "never ran", nil, []string{`"-coverprofile"`}},
		"coverage never ran": {"abc123", func() {
			engine.failLeaf(`"-coverprofile","mutation-cover.out","."`, "exitCode", "engine gone")
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
	engine.stdout(mergeBaseNeedle, sinceSha+"\n")
	engine.stdout(goDiffNeedle, "internal/oci/oci.go\n")
	engine.stdout(goCanaryNeedle, "Killed: 0, Lived: 1, Not covered: 0\n")
	engine.stdout(goClassifyNeedle, `{"noise":[]}`)
	wantState(t, runAtom(t, "go:mutation", "abc123"), 0, "PASS in 1 Go module (tools/forge)")
	c := engine.chain(goMutantsNeedle, "exitCode")
	wantCalls(t, c,
		[]string{"withWorkdir", `path:"/src/tools/forge"`},
		[]string{"withEnvVariable", `name:"GIT_CONFIG_COUNT"`, `value:"2"`},
		[]string{"withEnvVariable", `name:"GIT_CONFIG_KEY_0"`, `value:"diff.relative"`},
		[]string{"withEnvVariable", `name:"GIT_CONFIG_VALUE_0"`, `value:"true"`},
		// diff.context=0: gremlins scopes a fragment as one range from its
		// first added line, so context lines between two nearby insertions
		// would otherwise come into scope as code the pull never wrote.
		[]string{"withEnvVariable", `name:"GIT_CONFIG_KEY_1"`, `value:"diff.context"`},
		[]string{"withEnvVariable", `name:"GIT_CONFIG_VALUE_1"`, `value:"0"`},
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

// TestGoMutationReadsTheCanonicalConfigAtItsOneHome.
//
// THE CONFIG IS NOT AUTHORED HERE. It used to be a comment-only file this atom
// wrote — "neutral", meaning it stated nothing and inherited whatever gremlins
// defaulted to, so the fleet's gate was defined by the tool's defaults in two
// places that agreed by luck. It now comes from forge-testkit-go, read at its
// one home, exactly as the stocks scripts are.
//
// A REPO STILL HAS NO SAY: the file comes from that pinned repo through the
// door, never from the tree under check, so a star cannot dial its own gate
// down by editing itself.
func TestGoMutationReadsTheCanonicalConfigAtItsOneHome(t *testing.T) {
	scriptGoMutation(nil)
	wantState(t, runAtom(t, "go:mutation", "abc123"), 0)
	c := engine.chain(goMutantsNeedle, "exitCode")
	// The paper engine answers every git-home file with its own marker, so
	// seeing it here is seeing that the contents came from the REMOTE tree.
	wantCalls(t, c,
		[]string{"withNewFile", `path:"/tmp/mutation/gremlins-canonical.yaml"`, "the paper engine's copy"},
	)
	if hasCall(c, "withNewFile", `path:"/tmp/mutation/gremlins-canonical.yaml"`, "neutral") {
		t.Errorf("the atom is still authoring its own config:\n%s", c)
	}
}

// TestGoMutationTakesTheTimeoutCoefficientFromTheConfigNotAFlag.
//
// THE COEFFICIENT DECIDES WHETHER THE RUN IS CORRECT, so it belongs with the
// other knobs rather than duplicated in this argv. gremlins records a mutant
// TIMED OUT when parallel workers slow each other past the timeout computed
// from the unmutated suite, and TIMED OUT counts as neither killed nor
// survived — it silently shrinks the population. MEASURED on an unchanged
// 30-mutant tree: --workers 4 alone gave 15/12, 15/13, 14/14, 15/13 and 14/9
// on five runs; with the coefficient, 16/14 three times.
//
// --workers stays a flag: 4 is this LANE's deliberate override of a config
// default of 1, which is what a repo whose suite binds a loopback server needs
// and a CI runner does not.
func TestGoMutationTakesTheTimeoutCoefficientFromTheConfigNotAFlag(t *testing.T) {
	scriptGoMutation(nil)
	wantState(t, runAtom(t, "go:mutation", "abc123"), 0)
	c := engine.chain(goMutantsNeedle, "exitCode")
	if strings.Contains(c, "--timeout-coefficient") {
		t.Errorf("the coefficient is still a flag here, so it is defined twice:\n%s", c)
	}
	if !strings.Contains(c, `"--workers","4"`) {
		t.Errorf("the lane's deliberate worker override is gone:\n%s", c)
	}
}

// TestGoMutationCannotRunWithoutTheCanonicalConfig.
//
// WITHOUT THE FILE, GREMLINS FALLS BACK TO ITS OWN DEFAULTS and scores a
// different population — so a config that could not be read is a lane that
// could not measure (state 2), never a gate that quietly passes on defaults.
func TestGoMutationCannotRunWithoutTheCanonicalConfig(t *testing.T) {
	scriptGoMutation(nil)
	// A checkout of the testkit that does NOT carry the config: the paper
	// engine then answers "no such file" instead of its marker.
	engine.withTree(map[string]string{"/testkit/README.md": "# no config here\n"})
	v := runAtom(t, "go:mutation", "abc123")
	if v.State != 2 {
		t.Fatalf("a missing canonical config answered state %d, want 2 (could not run): %s", v.State, v.Reason)
	}
	if !strings.Contains(v.Reason, "canonical gremlins config") {
		t.Errorf("the reason does not name what was missing: %s", v.Reason)
	}
}

// copiesHades is the three-line shape: a Dockerfile that asks for the Gate's
// artifact by copying it from release/ (buildlane.CopiesRelease).
const copiesHades = "FROM x\nCOPY release/hades /hades\n"

// THE RELEASE BUILD IS THE IMAGE'S COMPILE, derived: the star's own binary from
// ./cmd/<star> with the fleet's flags, or the binaries the record declares.
func TestGoReleaseBuildsWhatTheImageWillCarry(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": copiesHades, ".copier-answers.yml": "service_name: hades\n"})
	wantState(t, runAtom(t, "go:release", ""), 0, "release build: hades", "the star's own name")
	wantCalls(t, engine.chain(`"go","build","-trimpath"`, "exitCode"),
		[]string{"withEnvVariable", `name:"CGO_ENABLED"`, `value:"0"`},
		[]string{"withExec", `expect:ANY`, `args:["go","build","-trimpath","-ldflags=-s -w","-o","/out/hades","./cmd/hades"]`},
	)

	// VENDOR IS READ OFF THE TREE, not declared: a tracked vendor/ is the flag.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{
		"Dockerfile":          "FROM x\nCOPY release/ourea /ourea\n",
		".copier-answers.yml": "service_name: ourea\n",
		"vendor/modules.txt":  "# x\n",
	})
	wantState(t, runAtom(t, "go:release", ""), 0, "-mod=vendor")
	wantCalls(t, engine.chain(`"-o","/out/ourea"`, "exitCode"),
		[]string{"withExec", `args:["go","build","-mod=vendor","-trimpath","-ldflags=-s -w","-o","/out/ourea","./cmd/ourea"]`})
	// AND NOTHING IS DOWNLOADED: a vendored release build reaches no proxy and
	// no door — ourea's image must build with the door down, and the door is
	// where GONOPROXY sends its first-party module (ourea
	// TestImageBuildsWithoutReachingTheDoor, which now guards the release path).
	if strings.Contains(engine.chain(`"-o","/out/ourea"`, "exitCode"), `"go","mod","download"`) {
		t.Errorf("a vendored release build ran go mod download:\n%s", engine.chain(`"-o","/out/ourea"`, "exitCode"))
	}
	// An unvendored module is still provisioned by the download.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": copiesHades, ".copier-answers.yml": "service_name: hades\n"})
	wantState(t, runAtom(t, "go:release", ""), 0, "release build: hades")
	if !strings.Contains(engine.chain(`"-o","/out/hades"`, "exitCode"), `"go","mod","download"`) {
		t.Errorf("an unvendored release build skipped go mod download:\n%s", engine.chain(`"-o","/out/hades"`, "exitCode"))
	}

	// The record names more than one, and each gets its own exec.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{
		"Dockerfile":          "FROM x\nCOPY release/blade-runner /blade-runner\nCOPY release/blade-controller /blade-controller\n",
		".copier-answers.yml": "service_name: blade-runner\n",
		"/dies/fleet/stars/blade-runner/slag.json": `{"tools":{"build":{"binaries":["blade-runner","blade-controller"]}}}`,
	})
	wantState(t, runAtom(t, "go:release", ""), 0, "blade-controller, blade-runner", "tools.build.binaries")
	for _, b := range []string{"blade-controller", "blade-runner"} {
		if engine.chain(`"-o","/out/`+b+`"`) == "" {
			t.Errorf("no exec builds %s:\n%v", b, engine.chains())
		}
	}
}

// A REPOSITORY WITH DOCKERFILES AND NO STAR IS NOT A STAR IMAGE — ABSENT, never
// a could-not-run. foundry-stocks measured it (Wonka17, 2026-09-17, nine gate
// runs on #205): it ships the CI bases and forge-tools, is not copier-templated,
// and the first cut wedged every landing there over "names no star".
func TestGoReleaseIsAbsentForARepoThatNamesNoStar(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\n"})
	v := runAtom(t, "go:release", "")
	if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "names no star") || !strings.Contains(v.Reason, "not a star image") {
		t.Errorf("want an absent 0 saying it is not a star image, got %+v", v)
	}
	if engine.chain(`"go","build","-trimpath"`) != "" {
		t.Errorf("nothing compiles for a repo that is not a star:\n%v", engine.chains())
	}
	// Its Dockerfile did not copy from release/ either — and the reason is
	// still "names no star": the star is read first, so a non-star says so.
	if strings.Contains(v.Reason, "compiles itself") {
		t.Errorf("a non-star was told it compiles itself: %s", v.Reason)
	}
	// A .copier-answers.yml that names nothing is the same absence.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\n", ".copier-answers.yml": "project: x\n"})
	if v := runAtom(t, "go:release", ""); v.Result != "absent" || !strings.Contains(v.Reason, "no service_name") {
		t.Errorf("%+v", v)
	}
}

// A DOCKERFILE THAT COMPILES ITSELF ASKS FOR NO RELEASE BUILD — ABSENT, the
// build lane's own reading (buildlane.CopiesRelease, build.go stageRelease:
// "built exactly as before"). narcissus is the case (foundry-tools #10307,
// narrowed 2026-09-18): tree-sitter through cgo, compiled statically in its
// own build stage, so the CGO_ENABLED=0 compile here would red a binary its
// image never carries.
func TestGoReleaseIsAbsentForADockerfileThatCompilesItself(t *testing.T) {
	self := "FROM docker.notusmi.com/library/golang:1.27 AS build\nRUN CGO_ENABLED=1 go build -o /out/hades ./cmd/hades\nFROM x\nCOPY --from=build /out/hades /hades\n"
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": self, ".copier-answers.yml": "service_name: hades\n"})
	v := runAtom(t, "go:release", "")
	if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "compiles itself") || !strings.Contains(v.Reason, "release/") {
		t.Errorf("want an absent 0 saying the image compiles itself, got %+v", v)
	}
	if engine.chain(`"go","build","-trimpath"`) != "" {
		t.Errorf("nothing compiles for a Dockerfile that compiles itself:\n%v", engine.chains())
	}

	// A second Dockerfile that does copy from release/ asks on the repo's
	// behalf: the compile runs.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{
		"Dockerfile": self, "docker/Dockerfile.hades": copiesHades,
		".copier-answers.yml": "service_name: hades\n",
	})
	wantState(t, runAtom(t, "go:release", ""), 0, "release build: hades")
	if engine.chain(`"go","build","-trimpath"`) == "" {
		t.Errorf("a Dockerfile that copies from release/ asked and nothing compiled:\n%v", engine.chains())
	}

	// A Dockerfile that cannot be read is a could-not-run that names it, never
	// an absence read off a fault — and the compile does not run on top of it.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"Dockerfile": self, ".copier-answers.yml": "service_name: hades\n"})
	engine.failLeaf(`file(path:"Dockerfile")`, "contents", "the file went away")
	wantState(t, runAtom(t, "go:release", ""), 2, "Dockerfile could not be read", "the file went away")
	if engine.chain(`"go","build","-trimpath"`) != "" {
		t.Errorf("a Dockerfile that could not be read still compiled:\n%v", engine.chains())
	}
}

// A REPO THAT SHIPS NO IMAGE IS ABSENT, NEVER A COULD-NOT-RUN. foundry-tools
// is the case that measured it: no Dockerfile, no image, no release build —
// and the first cut reded its own gate over a build nobody asked for.
func TestGoReleaseIsAbsentWhereThereIsNoImage(t *testing.T) {
	engine.reset()
	// A Go module and no Dockerfile: foundry-tools' own shape.
	engine.withTree(map[string]string{"go.mod": "module x\n", "main.go": "package main\n"})
	v := runAtom(t, "go:release", "")
	if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "tracks no Dockerfile") {
		t.Errorf("want an absent 0 naming the missing Dockerfile, got %+v", v)
	}
	if engine.chain(`"go","build","-trimpath"`) != "" {
		t.Errorf("nothing compiles for a repo that ships no image:\n%v", engine.chains())
	}
}

// EVERY WAY THE RELEASE BUILD CANNOT ANSWER IS A COULD-NOT-RUN THAT SAYS WHY,
// and none of them is a pass: a tree it cannot read, a module walk it cannot
// do, a repo with no root module, and a record whose binaries are unusable.
func TestGoReleaseSaysWhyItCouldNotRun(t *testing.T) {
	image := map[string]string{"Dockerfile": copiesHades, ".copier-answers.yml": "service_name: hades\n"}

	// The tree itself could not be read: the population is the first thing it asks for.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(image)
	engine.fail("glob", "the tree went away")
	wantState(t, runAtom(t, "go:release", ""), 2, "the tree could not be read", "the tree went away")

	// The module walk failed, so nothing knows whether there is a root module.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(image)
	engine.failLeaf("**/go.mod", "glob", "the module walk went away")
	wantState(t, runAtom(t, "go:release", ""), 2, "Go modules")

	// A module somewhere, but not at the root: no star binary to build.
	engine.reset()
	engine.withTree(map[string]string{
		"Dockerfile": copiesHades, ".copier-answers.yml": "service_name: hades\n",
		"tools/go.mod": "module x\n",
	})
	wantState(t, runAtom(t, "go:release", ""), 2, "no go.mod at the repository root")

	// The record names a binary that is not a binary name.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{
		"Dockerfile": copiesHades, ".copier-answers.yml": "service_name: hades\n",
		"/dies/fleet/stars/hades/slag.json": `{"tools":{"build":{"binaries":["../escape"]}}}`,
	})
	wantState(t, runAtom(t, "go:release", ""), 2, "is not a binary name")
}

// THE COVER STEP RUNS ONLY THE PACKAGES THE DIFF TOUCHES: one package per
// directory holding a changed Go file, the root as ".", sorted, no repeats;
// an empty list is the whole module.
func TestGoMutationCoverPackagesAreTheDiffs(t *testing.T) {
	got := goMutationCoverPackages("internal/gatejob/engines.go\ninternal/gatejob/gatejob.go\ncmd/ourea/main.go\nmain.go\ndocs/x.md\n\n")
	want := []string{".", "./cmd/ourea", "./internal/gatejob"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("packages %v, want %v", got, want)
	}
	if got := goMutationCoverPackages("docs/only.md\n"); len(got) != 1 || got[0] != "./..." {
		t.Fatalf("no Go file: the whole module, got %v", got)
	}
}

// A REPOSITORY WITH NO ANSWERS FILE IS NAMED BY ITS RECORD (CA F17, Rob
// 2026-09-19). hephaestus is not copier-templated and carries no
// .copier-answers.yml, but foundry-dies holds fleet/stars/hephaestus/slag.json
// saying repo rob/hephaestus — the record the door routes its lanes by. The
// release build reads the same name off the same record, keyed by the clone
// URL the door (--repo) or the hook (--origin) named; a run given neither, or
// a repo no record names, is still "not a star".
func TestGoReleaseNamesAStarWithNoAnswersFileByItsRecord(t *testing.T) {
	// everyLaneTree carries an answers file (critical_modules, no
	// service_name); this repository carries none at all.
	noAnswers := map[string]string{}
	for k, v := range everyLaneTree {
		if k != ".copier-answers.yml" {
			noAnswers[k] = v
		}
	}
	dies := map[string]string{
		"/dies/fleet/stars/hephaestus/slag.json":  `{"meta":{"name":"hephaestus","repo":"rob/hephaestus"},"tools":{}}`,
		"/dies/fleet/stars/tron/slag.json":        `{"meta":{"name":"tron","repo":"rob/tron"}}`,
		"/dies/fleet/stars/base-images/slag.json": `{"meta":{"name":"base-images","repo":"foundry/base-images"}}`,
	}
	// The door's Job: the tree was fetched from --repo.
	engine.reset()
	engine.withTree(noAnswers)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/hephaestus /hephaestus\n"})
	engine.withTree(dies)
	v := registry["go:release"](context.Background(), newRun(dag.Directory(), "http://ourea.default.svc.cluster.local:8215/hephaestus.git", ""))
	wantState(t, v, 0, "release build: hephaestus", "the star's own name")
	if engine.chain(`"-o","/out/hephaestus"`) == "" {
		t.Errorf("the record's name did not reach the compile:\n%v", engine.chains())
	}

	// The hook: the tree is a snapshot, and --origin names the clone.
	engine.reset()
	engine.withTree(noAnswers)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/hephaestus /hephaestus\n"})
	engine.withTree(dies)
	r := newRun(dag.Directory(), "", "").fromOrigin("http://ourea.notusmi.com:8215/hephaestus.git")
	wantState(t, registry["go:release"](context.Background(), r), 0, "release build: hephaestus")

	// An owner-qualified clone matches an owner-qualified record.
	engine.reset()
	engine.withTree(noAnswers)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/base-images /base-images\n"})
	engine.withTree(dies)
	r = newRun(dag.Directory(), "https://git.notusmi.com/foundry/base-images.git", "")
	wantState(t, registry["go:release"](context.Background(), r), 0, "release build: base-images")

	// No record names this repo: not a star, an absence that says which key it looked for.
	engine.reset()
	engine.withTree(noAnswers)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\n"})
	engine.withTree(dies)
	r = newRun(dag.Directory(), "http://ourea.default.svc.cluster.local:8215/nobody.git", "")
	v = registry["go:release"](context.Background(), r)
	if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "no record in foundry-dies names repo rob/nobody") {
		t.Errorf("%+v", v)
	}

	// No clone URL at all: nothing to look a record up by.
	engine.reset()
	engine.withTree(noAnswers)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\n"})
	engine.withTree(dies)
	v = runAtom(t, "go:release", "")
	if v.Result != "absent" || !strings.Contains(v.Reason, "no clone URL to find a record by") {
		t.Errorf("%+v", v)
	}

	// The records could not be listed or read: could-not-run, never "no star".
	engine.reset()
	engine.withTree(noAnswers)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/hephaestus /hephaestus\n"})
	engine.withTree(dies)
	engine.fail(`glob(pattern:"fleet/stars/*/slag.json")`, "the dies went away")
	r = newRun(dag.Directory(), "http://ourea.default.svc.cluster.local:8215/hephaestus.git", "")
	wantState(t, registry["go:release"](context.Background(), r), 2, "could not be listed", "the dies went away")
	engine.reset()
	engine.withTree(noAnswers)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/hephaestus /hephaestus\n"})
	engine.withTree(dies)
	engine.failLeaf(`file(path:"fleet/stars/base-images/slag.json")`, "contents", "the record went away")
	r = newRun(dag.Directory(), "http://ourea.default.svc.cluster.local:8215/hephaestus.git", "")
	wantState(t, registry["go:release"](context.Background(), r), 2, "could not be read", "the record went away")

	// The Rust atom reads the same name the same way, and files the same
	// could-not-run when the records cannot be listed.
	engine.reset()
	engine.withTree(noAnswers)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/hephaestus /hephaestus\n"})
	engine.withTree(dies)
	engine.fail(`glob(pattern:"fleet/stars/*/slag.json")`, "the dies went away")
	r = newRun(dag.Directory(), "http://ourea.default.svc.cluster.local:8215/hephaestus.git", "")
	wantState(t, registry["rust:release"](context.Background(), r), 2, "rust:release: CANNOT RUN - the fleet's records could not be listed", "the dies went away")
	engine.reset()
	engine.withTree(noAnswers)
	engine.withTree(map[string]string{"Dockerfile": "FROM x\nCOPY release/tron /tron\n"})
	engine.withTree(dies)
	r = newRun(dag.Directory(), "http://ourea.default.svc.cluster.local:8215/tron.git", "")
	wantState(t, registry["rust:release"](context.Background(), r), 0, "release build: tron")
}

// A FIXED GREMLINS COUNTS A MISGRADED FILE AGAIN, all the way through the atom,
// and the control is the ONLY thing that tells a false red from a real survivor.
// gremlins reports LIVED for a main package below the module root either way:
// while #268 stands it ran the root's tests, and once it is fixed it ran the
// right ones. Same report, same file, two verdicts.
func TestGoMutationCountsAMisgradedFileOnceItsControlPasses(t *testing.T) {
	report := `{"files":[{"file_name":"verdict/main.go","mutations":[{"type":"T","status":"LIVED","line":2,"column":3}]}]}`
	for _, c := range []struct {
		name, canary string
		state        int
		reason       string
	}{
		{"the control passes, so the survivor is real", "Killed: 0, Lived: 1, Not covered: 0\n", 1, "1 mutant(s) survived or were never covered"},
		// A PASS DISCARDS THE ATOM'S OUTPUT, so this green has to carry its own
		// headline or it prints as an unqualified pass - the misreading the whole
		// bucket exists to prevent.
		{"the control is broken, so nothing was graded", "Killed: 1, Lived: 0, Not covered: 0\n", 0, checks.GoMutationNothingGraded},
		// A control nobody could read has not cleared the runner.
		{"the control could not be read", "gremlins: no such module\n", 0, checks.GoMutationNothingGraded},
	} {
		t.Run(c.name, func(t *testing.T) {
			scriptGoMutation(map[string]string{"/src/mutation-go.json": report})
			engine.stdout(goDiffNeedle, "verdict/main.go\n")
			// atoms_go.go is the module root's own main package: graded correctly,
			// and never in the set.
			engine.stdout(goMainListNeedle, "/src/verdict/main.go\n/src/atoms_go.go\n")
			engine.stdout(goMainCanaryNeedle, c.canary)
			wantState(t, runAtom(t, "go:mutation", "abc123"), c.state, c.reason)
		})
	}
}

// A LISTING THAT DID NOT ANSWER IS NOT AN EMPTY LISTING. While the runner
// misgrades a main package and the lane cannot say which files are in one, there
// is nothing to exclude WITH — every count may be about other code and the gate
// cannot point at which. That is a could-not-measure, not a pass.
func TestGoMutationCannotMeasureWithoutTheMisgradedSet(t *testing.T) {
	for _, c := range []struct {
		name   string
		script func()
		reason string
	}{
		{"go list could not be read", func() { engine.fail(goMainListNeedle, "engine gone") }, "engine gone"},
		{"go list exited non-zero", func() { engine.exitCode(goMainListNeedle, 1) }, "go list exited 1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			scriptGoMutation(nil)
			c.script()
			wantState(t, runAtom(t, "go:mutation", "abc123"), 2,
				"could not list which files are in one", c.reason)
		})
	}
	// ...and once the runner is fixed there is nothing to exclude, so the same
	// failed listing is MOOT: a clean report passes and says nothing about it.
	scriptGoMutation(nil)
	engine.exitCode(goMainListNeedle, 1)
	engine.stdout(goMainCanaryNeedle, "Killed: 0, Lived: 1, Not covered: 0\n")
	v := runAtom(t, "go:mutation", "abc123")
	wantState(t, v, 0)
	if strings.Contains(v.Reason, "could not list") || strings.Contains(v.Reason, checks.GoMutationNothingGraded) {
		t.Errorf("a fixed runner still complained about the listing:\n%s", v.Reason)
	}
	// AND AN ORDINARY PASS STAYS QUIET. The lift exists for ONE case; riding on
	// every green would be noise that stops being read, and it would smuggle back
	// the output checks.VerdictOf discards on a pass. So the assertion is about
	// the discard itself, not about the one headline: a clean run's own verdict
	// line ("every viable mutant was caught") must not reach Reason either.
	// MEASURED: asserting only on GoMutationNothingGraded left `if state == 0`
	// alone — a clean pass's first line does not contain that string, so the
	// weaker test could not see the mutant.
	scriptGoMutation(nil)
	engine.stdout(goMainCanaryNeedle, "Killed: 0, Lived: 1, Not covered: 0\n")
	v = runAtom(t, "go:mutation", "abc123")
	for _, leaked := range []string{checks.GoMutationNothingGraded, "every viable mutant was caught", "Mutation gate"} {
		if strings.Contains(v.Reason, leaked) {
			t.Errorf("an ordinary pass lifted %q out of the output a pass discards:\n%s", leaked, v.Reason)
		}
	}
	// The summary is not LOST by that discard — it is in the logs, which is where
	// a forgiveness or an exclusion stays readable on a green.
	if !strings.Contains(strings.Join(v.Logs, "\n"), "Mutation gate") {
		t.Errorf("a passing atom kept no logs, so its summary is unreadable:\n%s", v.Logs)
	}
}

// allowedVulnReport is govulncheck's own text shape for the advisory the gate
// currently allows (internal/checks/vulnallow.go).
const allowedVulnReport = `=== Symbol Results ===

Vulnerability #1: GO-2026-6508
  More info: https://pkg.go.dev/vuln/GO-2026-6508
    Found in: go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc@v0.19.0
    Fixed in: go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc@v0.21.0

Your code is affected by 1 vulnerability from 1 module.
`

// THE ALLOWANCE IS APPLIED BY THE ATOM, not merely available to it — which is
// the half checks' own tests cannot see, because they never run the atom.
//
// EVERY CASE ANSWERS A SURVIVING MUTANT reported on 68432ba: atoms_go.go:638
// (the exit == 1 guard) LIVED, and :640 (the reason joined to the output) was
// NOT COVERED — nothing executed the allowance branch at all.
func TestTheGovulncheckAtomAppliesTheGatesAllowance(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)

	// An ALLOWED advisory on a findings exit passes — loudly, with govulncheck's
	// own report still attached as evidence.
	engine.exitCode(`"govulncheck","./..."`, 3)
	engine.stdout(`"govulncheck","./..."`, allowedVulnReport)
	v := runAtom(t, "go:govulncheck", "")
	wantState(t, v, 0, "GO-2026-6508", "ALLOWED", "not a clean scan", "dagger/otel-go")
	// AND THE REPORT IS STILL THE EVIDENCE. The reason says what was allowed;
	// govulncheck's own output stays as the atom's lines, exactly as a red
	// run's would, so the allowance can be checked rather than taken on trust.
	if !strings.Contains(strings.Join(v.Logs, "\n"), "Fixed in") {
		t.Errorf("the scan's own report must survive as the atom's lines:\n%v", v.Logs)
	}

	// An advisory NOBODY allowed still reds, whatever it arrived beside.
	engine.stdout(`"govulncheck","./..."`, allowedVulnReport+"\nVulnerability #2: GO-2026-9999\n")
	wantState(t, runAtom(t, "go:govulncheck", ""), 1)

	// AND AN ALLOWANCE NEVER RESCUES A COULD-NOT-RUN. exit 2 is the advisory
	// database not answering; a scan that did not complete has found nothing to
	// allow, and reading one as a pass would turn a substrate fault into a
	// clean bill of health — the exact conflation AuditVerdict exists to stop.
	engine.exitCode(`"govulncheck","./..."`, 2)
	engine.stdout(`"govulncheck","./..."`, allowedVulnReport)
	wantState(t, runAtom(t, "go:govulncheck", ""), 2)
}
