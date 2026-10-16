package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE ENVIRONMENT OF THE GO SUITES, against the paper engine: which container
// the three suite atoms and govulncheck run in, which servers the tree's tags
// bind to it and which generator the regen tests are given. What each atom asks
// of the tree is tested beside the atom (atoms/go/*) and in internal/atoms.

const (
	// goRaceOut and the others are the files the adapter reads each atom's
	// verdict from.
	goRaceNeedle = "go__test-race.json"
	goCovNeedle  = "go__diff-coverage.json"
	goTestNeedle = "go__test.json"
	goVulnNeedle = "go__govulncheck.json"
	// goTagsNeedle is the one recursive read of the tree's build constraints
	// that decides the servers.
	goTagsNeedle = `"grep","-rhoE"`

	regenRev = "f450dd4c17fabbc2820904727f2d07f3f4916eb7"
)

// goVerdict is the verdict an atom of the go lane wrote.
func goVerdict(id, stage string) string {
	return `{"atom":"` + id + `","stage":"` + stage + `","lane":"go","state":0,"result":"pass","logs":[]}`
}

// raceRun runs go:test-race through the adapter and answers the chain of the
// exec that ran the atom.
func raceRun(t *testing.T, tree map[string]string, grep string) string {
	t.Helper()
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{".copier-answers.yml": "service_name: x\n"})
	engine.withTree(tree)
	engine.contents(goRaceNeedle, goVerdict("go:test-race", "prepush"))
	engine.stdout(goTagsNeedle, grep)
	if grep == "" {
		engine.exitCode(goTagsNeedle, 1)
	}
	wantState(t, runAtom(t, "go:test-race", ""), 0)
	return engine.chain(goRaceNeedle, "contents")
}

// THE TREE SAYS WHICH TAGS, THE LANE BRINGS THE DATABASE (checks/testdb.go).
// Before this the DB-gated suites never compiled and every DB-touching line
// read NOT COVERED (foundry-tools#8608).
func TestGoTestRaceRunsWithTheFleetRecordsAndTheTreesDatabases(t *testing.T) {
	c := raceRun(t, nil, "//go:build live_db\n//go:build live_db_novector\n//go:build integration\n")
	wantCalls(t, c,
		[]string{"withMountedDirectory", `path:"/dies"`},
		[]string{"withEnvVariable", `name:"FOUNDRY_DIES"`, `value:"/dies"`},
		[]string{"withEnvVariable", `name:"ATOM_DIES"`, `value:"/dies"`},
		[]string{"withServiceBinding", `alias:"db"`},
		[]string{"withServiceBinding", `alias:"db-novector"`},
		[]string{"withEnvVariable", `name:"TEST_DATABASE_URL"`, `value:"` + checks.TestDBs[0].DSN() + `"`},
		[]string{"withEnvVariable", `name:"TEST_NOVECTOR_DATABASE_URL"`, `value:"` + checks.TestDBs[1].DSN() + `"`},
		[]string{"withExec", `expect:ANY`, `args:["/atom","/src"]`},
	)
	// The servers are the pinned images, run as services on their port.
	for _, img := range []string{checks.ImagePgvector, checks.ImagePostgres} {
		if engine.chain(img, "asService") == "" {
			t.Errorf("no service chain runs %s:\n%v", img, engine.chains())
		}
	}
	// The race atom KEEPS THE SHARED SERVER: it must carry no lane word, or it
	// would be a third database nobody asked for.
	if hasCall(engine.chain(checks.ImagePgvector, "asService"), "withEnvVariable", `name:"FOUNDRY_TEST_DB_LANE"`) {
		t.Error("go:test-race took a lane-scoped server; it shares by design")
	}
	if !hasCall(engine.chain(checks.ImagePgvector, "asService"), "withExposedPort", "5432") {
		t.Errorf("the pgvector service must expose 5432:\n%v", engine.chains())
	}
	// AND RUNS NON-DURABLE, BUILT THE WAY A SERVICE BECOMES READY: the server
	// flags as the container's default args, started through the entrypoint.
	// A WithExec service never reads ready (checks.TestBroker.StartArgs).
	for _, d := range checks.TestDBs {
		svc := engine.chain(d.Image, "asService")
		args := `args:["` + strings.Join(d.ServerArgs(), `","`) + `"]`
		if !hasCall(svc, "withDefaultArgs", args) {
			t.Errorf("the %s service is not started with ServerArgs:\n%s", d.Tag, svc)
		}
		if !hasCall(svc, "asService", "useEntrypoint:true") {
			t.Errorf("the %s service must start through the image's entrypoint:\n%s", d.Tag, svc)
		}
		if strings.Contains(svc, "withExec") {
			t.Errorf("the %s service carries a withExec and will never read ready:\n%s", d.Tag, svc)
		}
	}
	// The tag enumeration is one grep, in the lane, off the tree: the exec runs
	// under ANY because "no match" is exit 1.
	if !hasCall(engine.chain(goTagsNeedle, "exitCode"), "withExec", "expect:ANY", `--include=*_test.go`) {
		t.Errorf("the build-tag read must tolerate grep's exit 1:\n%v", engine.chains())
	}
}

// NO TAGGED SUITE: nothing is bound. grep exits 1 for no match, which is an
// answer.
func TestGoTestRaceBindsNothingForATreeWithNoTaggedSuite(t *testing.T) {
	c := raceRun(t, nil, "")
	if hasCall(c, "withServiceBinding") {
		t.Errorf("a tree with no tagged suite binds nothing:\n%s", c)
	}
	wantCalls(t, c, []string{"withMountedDirectory", `path:"/dies"`}, []string{"withExec", `expect:ANY`, `args:["/atom","/src"]`})
}

// A BROKER ALONE STILL BINDS: a tree that states a Kafka flow and a live_kafka
// suite gets the broker (and no database).
func TestGoTestRaceBindsABrokerForAKafkaTree(t *testing.T) {
	c := raceRun(t, map[string]string{"go.mod": "module example.com/x\n", "flows.go": oneKafkaFlow}, "//go:build live_kafka\n")
	wantCalls(t, c,
		[]string{"withServiceBinding", `alias:"broker"`},
		[]string{"withEnvVariable", `name:"KAFKA_BOOTSTRAP"`, `value:"` + checks.TestBrokers[0].AddrFor("") + `"`},
	)
	if hasCall(c, "withServiceBinding", `alias:"db"`) {
		t.Errorf("only the tags the tree carries are bound:\n%s", c)
	}
}

// A tree whose Kafka flows would not read binds no broker; the atom says why.
func TestGoTestRaceBindsNoBrokerWhenTheFlowsWouldNotRead(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{"flows.go": oneKafkaFlow})
	engine.fail(`file(path:"flows.go")`, "i/o error")
	engine.stdout(goTagsNeedle, "//go:build live_kafka\n")
	engine.contents(goRaceNeedle, goVerdict("go:test-race", "prepush"))
	runAtom(t, "go:test-race", "")
	if c := engine.chain(goRaceNeedle, "contents"); hasCall(c, "withServiceBinding") {
		t.Errorf("a broker was bound over flows that would not read:\n%s", c)
	}
}

// EVERY MODULE IS READ IN ITS OWN DIRECTORY, as its suite is run there; the lane
// binds what any of them asks for, each once.
func TestGoTestRaceBindsTheUnionOfEveryModulesServers(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{
		"a/go.mod": "module a\n", "a/a_test.go": "package a\n",
		"b/go.mod": "module b\n", "b/b_test.go": "package b\n",
		"c/go.mod": "module c\n", "c/c_test.go": "package c\n",
	})
	engine.stdoutAll("//go:build live_db_novector\n", goTagsNeedle, `path:"/src/a"`)
	engine.stdoutAll("//go:build live_db\n//go:build live_db_novector\n", goTagsNeedle, `path:"/src/b"`)
	engine.contents(goRaceNeedle, goVerdict("go:test-race", "prepush"))
	runAtom(t, "go:test-race", "")
	c := engine.chain(goRaceNeedle, "contents")
	if n := strings.Count(c, "withServiceBinding("); n != 2 {
		t.Errorf("want the two databases once each, got %d bindings:\n%s", n, c)
	}
	// in the vocabulary's binding order, whichever module asked first
	if db, novec := strings.Index(c, `alias:"db"`), strings.Index(c, `alias:"db-novector"`); db < 0 || novec < db {
		t.Errorf("the databases are not bound in the vocabulary's order:\n%s", c)
	}
}

// A tree whose modules would not enumerate binds nothing: the atom reports it.
func TestGoTestRaceBindsNothingWhenTheModulesWouldNotEnumerate(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail("glob(", "engine went away")
	engine.contents(goRaceNeedle, goVerdict("go:test-race", "prepush"))
	runAtom(t, "go:test-race", "")
	if c := engine.chain(goRaceNeedle, "contents"); c != "" && hasCall(c, "withServiceBinding") {
		t.Errorf("servers bound for a tree whose modules could not be listed:\n%s", c)
	}
}

// THE COMMIT'S SUITE IS THE UNIT SUITE: the same packages, no race detector and
// no database binding — so a DB-gated suite does not even compile in. It still
// carries the fleet records, which its armed goldens read.
func TestGoTestBindsNoServerAndCarriesTheFleetRecords(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{".copier-answers.yml": "service_name: x\n"})
	engine.stdout(goTagsNeedle, "//go:build live_db\n")
	engine.contents(goTestNeedle, goVerdict("go:test", "precommit"))
	runAtom(t, "go:test", "")
	c := engine.chain(goTestNeedle, "contents")
	wantCalls(t, c,
		[]string{"withMountedDirectory", `path:"/dies"`},
		[]string{"withEnvVariable", `name:"FOUNDRY_DIES"`, `value:"/dies"`},
		[]string{"withExec", `expect:ANY`, `args:["/atom","/src"]`},
	)
	if hasCall(c, "withServiceBinding") {
		t.Errorf("the commit's suite binds no database:\n%s", c)
	}
}

// The diff-coverage atom RUNS THE SUITE ITSELF, on servers of its own: dagger
// content-addresses services, so a server shared with go:test-race (which runs
// beside it) has its schema reset under both. It judges the change, so it is
// handed the base, the origin and the fork record, and it reads git against the
// whole tree.
func TestGoDiffCoverageRunsOnItsOwnServersAndJudgesTheChange(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{".copier-answers.yml": "service_name: x\n", "flows.go": oneKafkaFlow})
	engine.stdout(goTagsNeedle, "//go:build live_db\n//go:build live_kafka\n")
	engine.contents(goCovNeedle, goVerdict("go:diff-coverage", "prepush"))
	runAtom(t, "go:diff-coverage", "abc123")
	c := engine.chain(goCovNeedle, "contents")
	wantCalls(t, c,
		[]string{"withServiceBinding", `alias:"db-diffcov"`},
		[]string{"withServiceBinding", `alias:"broker-diffcov"`},
		[]string{"withEnvVariable", `name:"TEST_DATABASE_URL"`, `value:"` + checks.TestDBs[0].DSNFor("diffcov") + `"`},
		[]string{"withEnvVariable", `name:"KAFKA_BOOTSTRAP"`, `value:"` + checks.TestBrokers[0].AddrFor("diffcov") + `"`},
		[]string{"withEnvVariable", `name:"ATOM_BASE"`, `value:"abc123"`},
		[]string{"withEnvVariable", `name:"ATOM_ORIGIN"`},
		[]string{"withEnvVariable", `name:"ATOM_FORK"`},
		[]string{"withEnvVariable", `name:"GATE_BASE"`, `value:"abc123"`},
		[]string{"withMountedDirectory", `path:"/dies"`},
	)
	if hasCall(c, "withServiceBinding", `alias:"db"`) || hasCall(c, "withServiceBinding", `alias:"broker"`) {
		t.Errorf("go:diff-coverage took go:test-race's servers:\n%s", c)
	}
	svc := engine.chain(checks.ImagePgvector, "asService", "diffcov")
	if !hasCall(svc, "withEnvVariable", `name:"FOUNDRY_TEST_DB_LANE"`, `value:"diffcov"`) {
		t.Errorf("the lane's server is defined identically to the shared one:\n%s", svc)
	}
	narrowed := `filter(exclude:["` + strings.Join(checks.InertPaths, `","`) + `"])`
	if strings.Contains(c, narrowed) {
		t.Errorf("a git-reading atom must see the whole tree:\n%s", c)
	}
}

func TestGoVulncheckRunsInTheCodeLane(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.contents(goVulnNeedle, goVerdict("go:govulncheck", "prepush"))
	runAtom(t, "go:govulncheck", "")
	c := engine.chain(goVulnNeedle, "contents")
	if !strings.Contains(c, checks.ImageGo) || hasCall(c, "withServiceBinding") || strings.Contains(c, "GATE_BASE") {
		t.Errorf("go:govulncheck runs in the go lane, binds nothing and does not read the base:\n%s", c)
	}
	narrowed := `filter(exclude:["` + strings.Join(checks.InertPaths, `","`) + `"])`
	if src := engine.chain(narrowed); src == "" || !strings.Contains(c, fakeID(src)) {
		t.Errorf("the code lane mounts the narrowed tree:\n%s", c)
	}
}

func gravityProv(rev string) string {
	return `{"generator":{"command":"gravity --name x-core --package x ./guest.wasm","commit":"` + rev + `"}}`
}

func gravityRun(t *testing.T, extra map[string]string) string {
	t.Helper()
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(extra)
	engine.contents(goTestNeedle, goVerdict("go:test", "precommit"))
	runAtom(t, "go:test", "")
	return engine.chain(goTestNeedle, "contents")
}

// THE GENERATOR IS STAGED where the tree has a package that opted in: gravity
// at the pinned rev, built by cargo from the fleet's fork on the rust image and
// asked its version before it is trusted. The variables that make a missing
// generator a failure are the atom's, set on its own exec.
func TestGoTestStagesGravityAtThePinnedRev(t *testing.T) {
	c := gravityRun(t, map[string]string{
		"classescore/provenance.json": gravityProv(regenRev),
		"classescore/regen_test.go":   "package classescore\n",
		"alarm/provenance.json":       gravityProv("824e00f7108f62fac3c5e0d730e3bded5673da5e"), // no regen test: not held
	})
	wantCalls(t, c, []string{"withFile", `path:"/usr/local/bin/gravity"`, `permissions:493`})
	g := engine.chain(`"cargo","install"`, "file(")
	if !strings.Contains(g, checks.ImageRust) {
		t.Errorf("gravity builds on the rust lane image:\n%s", g)
	}
	wantCalls(t, g,
		[]string{"withExec", `"cargo","install","--locked","--git","` + checks.GravityGit + `","--rev","` + regenRev + `","--root","/opt/gravity","arcjet-gravity"`},
		[]string{"withExec", `"/opt/gravity/bin/gravity","--version"`},
		[]string{"file", `path:"/opt/gravity/bin/gravity"`},
	)
}

// A rev with a prebuilt binary in the mirror is fetched at its sha256, not
// compiled: the cargo install is the fallback for a rev nobody seeded.
func TestGoTestStagesThePrebuiltGravityWithoutCompiling(t *testing.T) {
	var rev, sum string
	for rev, sum = range checks.GravityPrebuilt {
	}
	c := gravityRun(t, map[string]string{
		"classescore/provenance.json": gravityProv(rev),
		"classescore/regen_test.go":   "package classescore\n",
	})
	wantCalls(t, c, []string{"withFile", `path:"/usr/local/bin/gravity"`, `permissions:493`})
	if h := engine.chain(checks.ToolMirrorURL(sum), "sha256:"+sum); h == "" {
		t.Errorf("gravity is fetched from the mirror at its pin:\n%v", engine.chains())
	}
	if engine.chain(`"cargo","install"`) != "" {
		t.Error("a prebuilt gravity must not be compiled")
	}
}

func TestGoTestStagesNothingWithoutAGravityPackageThatHasTheTest(t *testing.T) {
	c := gravityRun(t, map[string]string{
		"alarm/provenance.json": gravityProv(regenRev),
		"other/regen_test.go":   "package other\n",
		"ts/provenance.json":    `{"tools":{"jco":"1.35.0"}}`,
	})
	if hasCall(c, "withFile", `/usr/local/bin/gravity`) {
		t.Errorf("nothing is staged where nothing opted in:\n%s", c)
	}
	if engine.chain(`"cargo","install"`) != "" {
		t.Error("gravity must not be built for a tree that does not use it")
	}
}

// A provenance the lane cannot honour, or cannot read, stages nothing: the atom
// reads the same tree and files the finding or the could-not-run itself.
func TestGoTestStagesNothingForAProvenanceItCannotHonourOrRead(t *testing.T) {
	for name, extra := range map[string]map[string]string{
		"two pins":     {"a/provenance.json": gravityProv(regenRev), "a/regen_test.go": "", "b/provenance.json": gravityProv("824e00f7108f62fac3c5e0d730e3bded5673da5e"), "b/regen_test.go": ""},
		"not JSON":     {"a/provenance.json": "{", "a/regen_test.go": ""},
		"listing fail": {"a/provenance.json": gravityProv(regenRev), "a/regen_test.go": ""},
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		engine.withTree(extra)
		if name == "listing fail" {
			engine.failLeaf(`provenance.json`, "glob", "engine: glob failed")
		}
		engine.contents(goTestNeedle, goVerdict("go:test", "precommit"))
		runAtom(t, "go:test", "")
		if c := engine.chain(goTestNeedle, "contents"); hasCall(c, "withFile", `/usr/local/bin/gravity`) {
			t.Errorf("%s: gravity was staged for a provenance that cannot be honoured:\n%s", name, c)
		}
	}
}
