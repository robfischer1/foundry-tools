package main

import (
	"context"
	"strings"
	"testing"
)

var (
	bunBase = "registry.notusmi.com/foundry/base-images/bun:stable@sha256:" + strings.Repeat("b", 64)
	pyBase  = "registry.notusmi.com/foundry/base-images/python:stable@sha256:" + strings.Repeat("c", 64)
	goBase  = "registry.notusmi.com/foundry/base-images/go:stable@sha256:" + strings.Repeat("d", 64)
)

// A bun star on the release path: its Dockerfile is the base and a COPY, its
// record names the steps, and the steps leave server.js under release/.
func bunStar(steps string) map[string]string {
	return map[string]string{
		"Dockerfile":                           "FROM " + bunBase + "\nCOPY release/ /app/\nCMD [\"bun\", \"server.js\"]\n",
		".copier-answers.yml":                  "service_name: calliope\n",
		"package.json":                         "{}",
		"bun.lock":                             "",
		"/dies/fleet/stars/calliope/slag.json": `{"tools":{"build":{"release":` + steps + `}}}`,
		"/src/release/server.js":               "",
	}
}

const calliopeSteps = `[["bun","run","build"],["bun","apps/calliope/scripts/bundle.ts","release"]]`

// A python star on the release path: the base and a COPY of the venv.
func pythonStar(extras string) map[string]string {
	t := map[string]string{
		"Dockerfile":          "FROM " + pyBase + "\nCOPY --chown=999:999 release/app /app\nCMD [\"iris\"]\n",
		".copier-answers.yml": "service_name: iris\n",
		"pyproject.toml":      "[project]\nname = \"iris\"\n",
		"uv.lock":             "",
		"/app/.venv/bin/iris": "",
	}
	if extras != "" {
		t["/dies/fleet/stars/iris/slag.json"] = `{"tools":{"build":{"extras":` + extras + `}}}`
	}
	return t
}

// THE BUN RELEASE IS THE RECORD'S STEPS, IN ORDER, ON THE IMAGE'S OWN BASE:
// the tree copied in without release/ or node_modules, the frozen install,
// then each step — and what they left under release/ is what the COPY gets.
func TestTSReleaseRunsTheRecordsStepsOnTheImagesBase(t *testing.T) {
	engine.reset()
	engine.withTree(bunStar(calliopeSteps))
	wantState(t, runAtom(t, "ts:release", ""), 0, "2 step(s) from tools.build.release", bunBase, "server.js")

	last := engine.chain(`"bun","apps/calliope/scripts/bundle.ts","release"`, "exitCode")
	if last == "" {
		t.Fatalf("the record's last step never ran:\n%v", engine.chains())
	}
	wantCalls(t, last,
		[]string{"from", `address:"` + bunBase + `"`},
		[]string{"withMountedCache", `path:"/root/.bun/install/cache"`},
		[]string{"withDirectory", `path:"/src"`},
		[]string{"withWorkdir", `path:"/src"`},
	)
	// The tree goes in by id; its own chain carries the filter.
	if engine.chain(`filter(`, `"release"`, `"**/node_modules"`) == "" {
		t.Errorf("the tree is not copied in without release/ and node_modules:\n%v", engine.chains())
	}
	install, build, bundle := strings.Index(last, `"bun","install","--frozen-lockfile"`), strings.Index(last, `"bun","run","build"`), strings.Index(last, `"apps/calliope/scripts/bundle.ts"`)
	if !(install >= 0 && install < build && build < bundle) {
		t.Errorf("install, then the steps in the record's order — got %d %d %d:\n%s", install, build, bundle, last)
	}
}

// A FRONTEND STAR IS NAMED BY ITS RECORD. The frontend template asks for
// repo_name, not service_name, and repo_name is not the star (demeter's is
// demeter-mcp): the record whose meta.repo is the clone URL's key names it,
// the way it names a repository with no answers file at all (calliope #46).
func TestAFrontendStarWithNoServiceNameIsNamedByItsRecord(t *testing.T) {
	engine.reset()
	tree := bunStar(calliopeSteps)
	tree[".copier-answers.yml"] = "repo_name: calliope-mcp\nframework: mcp\n"
	tree["/dies/fleet/stars/calliope/slag.json"] = `{"meta":{"repo":"rob/calliope"},"tools":{"build":{"release":` + calliopeSteps + `}}}`
	engine.withTree(tree)
	r := newRun(dag.Directory(), "http://ourea.default.svc.cluster.local:8215/calliope.git", "")
	wantState(t, registry["ts:release"](context.Background(), r), 0, "2 step(s) from tools.build.release")

	// With no clone URL to find the record by, it says what it lacked.
	engine.reset()
	engine.withTree(tree)
	v := runAtom(t, "ts:release", "")
	if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "no service_name in .copier-answers.yml and no clone URL") {
		t.Errorf("want an absent naming both lacks, got %+v", v)
	}
	// And a clone URL no record names says that.
	engine.reset()
	engine.withTree(tree)
	r = newRun(dag.Directory(), "http://ourea.default.svc.cluster.local:8215/elsewhere.git", "")
	v = registry["ts:release"](context.Background(), r)
	if v.State != 0 || !strings.Contains(v.Reason, "no service_name in .copier-answers.yml, and no record in foundry-dies names repo rob/elsewhere") {
		t.Errorf("want an absent naming the unrecorded repo, got %+v", v)
	}
}

// A STEP THAT FAILS IS THE TREE'S FINDING, NAMED, AND THE CHAIN STOPS AT IT;
// a step that could not run at all, or a network the substrate lost, is a
// could-not-run.
func TestTSReleaseSettlesAFailedStepByWhoseFaultItIs(t *testing.T) {
	engine.reset()
	engine.withTree(bunStar(calliopeSteps))
	engine.exitCode(`"bun","run","build"`, 2)
	engine.stderr(`"bun","run","build"`, "error TS2322: Type 'string' is not assignable")
	wantState(t, runAtom(t, "ts:release", ""), 1, "`bun run build` exited 2", "TS2322")
	if engine.chain(`"apps/calliope/scripts/bundle.ts"`) != "" {
		t.Errorf("a step ran on top of one that failed:\n%v", engine.chains())
	}

	// The frozen install is a step like any other.
	engine.reset()
	engine.withTree(bunStar(calliopeSteps))
	engine.exitCode(`"bun","install","--frozen-lockfile"`, 1)
	engine.stderr(`"bun","install","--frozen-lockfile"`, "error: lockfile had changes, but lockfile is frozen")
	wantState(t, runAtom(t, "ts:release", ""), 1, "`bun install --frozen-lockfile` exited 1", "lockfile is frozen")

	// A command the step names that is not there, and a network fault.
	for code, out := range map[int]string{127: "bunx: not found", 1: "error: ECONNRESET fetching the tarball"} {
		engine.reset()
		engine.withTree(bunStar(calliopeSteps))
		engine.exitCode(`"bun","run","build"`, code)
		engine.stderr(`"bun","run","build"`, out)
		wantState(t, runAtom(t, "ts:release", ""), 2, out)
	}

	// Clean steps that left nothing under release/ gave the COPY nothing.
	engine.reset()
	tree := bunStar(calliopeSteps)
	delete(tree, "/src/release/server.js")
	engine.withTree(tree)
	wantState(t, runAtom(t, "ts:release", ""), 1, "left nothing under release/")
}

// A BUN RELEASE HAS NO CONVENTION: a Dockerfile asking for release/ on the bun
// base, with a record that names no steps, cannot be built and says why.
func TestTSReleaseWithNoStepsInTheRecordCannotRun(t *testing.T) {
	engine.reset()
	tree := bunStar(calliopeSteps)
	delete(tree, "/dies/fleet/stars/calliope/slag.json")
	engine.withTree(tree)
	wantState(t, runAtom(t, "ts:release", ""), 2, "names no tools.build.release")
	if engine.chain(`"bun","install"`) != "" {
		t.Errorf("an install ran for a release with no steps:\n%v", engine.chains())
	}
}

// THE ABSENCES, EACH A 0 THAT SAYS WHY AND BUILDS NOTHING: no image, not a
// star, an image that builds itself, and an image on another lane's base —
// the one that matters for a tree declaring two toolchains.
func TestTheBaseBuiltReleasesAreAbsentWhereTheImageIsNotTheirs(t *testing.T) {
	cases := []struct {
		name, atom string
		tree       map[string]string
		why        string
	}{
		{"no Dockerfile", "ts:release", map[string]string{"package.json": "{}", ".copier-answers.yml": "service_name: x\n"}, "tracks no Dockerfile"},
		{"not a star", "python:release", map[string]string{"pyproject.toml": "", "Dockerfile": "FROM " + pyBase + "\nCOPY release/app /app\n"}, "not a star image"},
		{"builds itself", "ts:release", map[string]string{"package.json": "{}", ".copier-answers.yml": "service_name: x\n", "Dockerfile": "FROM " + bunBase + " AS b\nRUN bun build\nFROM " + bunBase + "\nCOPY --from=b /deploy /app\n"}, "builds itself"},
		// mnemosyne's shape: a go.mod and the pyproject its port left behind,
		// the image on the go base. python:release is not its release.
		{"python tree on the go base", "python:release", map[string]string{"go.mod": "module x\n", "pyproject.toml": "", ".copier-answers.yml": "service_name: mnemosyne\n", "Dockerfile": "FROM " + goBase + "\nCOPY release/mnemosyne /mnemosyne\n"}, "is built on " + goBase},
		{"bun tree on the python base", "ts:release", pythonStar(`["pg"]`), "not registry.notusmi.com/foundry/base-images/bun"},
		{"a base no lane names", "python:release", map[string]string{"pyproject.toml": "", ".copier-answers.yml": "service_name: x\n", "Dockerfile": "FROM ${BASE}\nCOPY release/app /app\n"}, "a base this lane cannot name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			engine.reset()
			engine.withTree(c.tree)
			v := runAtom(t, c.atom, "")
			if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, c.why) {
				t.Errorf("want an absent 0 saying %q, got %+v", c.why, v)
			}
			if engine.chain(`"bun","install"`) != "" || engine.chain(`"uv","sync"`) != "" {
				t.Errorf("an absence built something:\n%v", engine.chains())
			}
		})
	}
}

// THE QUESTION ITSELF FAILING IS A COULD-NOT-RUN: a tree that cannot be read,
// a Dockerfile that cannot be read.
func TestTheBaseBuiltReleasesSayWhyTheyCouldNotAsk(t *testing.T) {
	engine.reset()
	engine.withTree(pythonStar(""))
	engine.fail("glob", "the tree went away")
	wantState(t, runAtom(t, "python:release", ""), 2, "the tree could not be read", "the tree went away")

	engine.reset()
	engine.withTree(pythonStar(""))
	engine.failLeaf(`file(path:"Dockerfile")`, "contents", "the file went away")
	wantState(t, runAtom(t, "python:release", ""), 2, "Dockerfile could not be read", "the file went away")
	if engine.chain(`"uv","sync"`) != "" {
		t.Errorf("a Dockerfile that could not be read still synced:\n%v", engine.chains())
	}

	// A star named by its record, whose records cannot be listed: the name is
	// unknown, not absent, so the atom could not ask — never "not a star".
	engine.reset()
	tree := pythonStar("")
	delete(tree, ".copier-answers.yml")
	tree["/dies/fleet/stars/iris/slag.json"] = `{"meta":{"repo":"rob/iris"}}`
	engine.withTree(tree)
	engine.fail(`glob(pattern:"fleet/stars/*/slag.json")`, "the dies went away")
	r := newRun(dag.Directory(), "http://ourea.default.svc.cluster.local:8215/iris.git", "")
	wantState(t, registry["python:release"](context.Background(), r), 2, "python:release: CANNOT RUN", "the dies went away")

	// An engine that goes away under a step is could-not-run that says so.
	engine.reset()
	engine.withTree(bunStar(calliopeSteps))
	engine.fail(`"bun","run","build"`, "the engine went away")
	wantState(t, runAtom(t, "ts:release", ""), 2, "the atom never ran", "the engine went away")
}

// THE PYTHON RELEASE IS THE STAR'S VENV, BUILT AT /app ON THE IMAGE'S OWN
// BASE from its own lock, as a wheel, with the extras its record names.
func TestPythonReleaseBuildsTheVenvWithTheRecordsExtras(t *testing.T) {
	engine.reset()
	engine.withTree(pythonStar(`["pg","kafka"]`))
	wantState(t, runAtom(t, "python:release", ""), 0, "extras pg, kafka from tools.build.extras", pyBase)
	sync := engine.chain(`"uv","sync","--locked","--no-dev","--no-editable","--extra","pg","--extra","kafka"`, "exitCode")
	if sync == "" {
		t.Fatalf("the venv was not built from the lock with the record's extras:\n%v", engine.chains())
	}
	wantCalls(t, sync,
		[]string{"from", `address:"` + pyBase + `"`},
		[]string{"withMountedCache", `path:"/opt/uv-cache"`},
		[]string{"withEnvVariable", `name:"UV_CACHE_DIR"`, `value:"/opt/uv-cache"`},
		[]string{"withDirectory", `path:"/app"`},
		[]string{"withWorkdir", `path:"/app"`},
	)
	if engine.chain(`filter(`, `".venv"`, `"release"`) == "" {
		t.Errorf("the tree is not copied in without a local .venv and release/:\n%v", engine.chains())
	}

	// No extras in the record is the convention: the project and its
	// required dependencies.
	engine.reset()
	engine.withTree(pythonStar(""))
	wantState(t, runAtom(t, "python:release", ""), 0, "with no extras")
	if engine.chain(`"uv","sync","--locked","--no-dev","--no-editable"]`) == "" {
		t.Errorf("the extra-less sync is not the bare one:\n%v", engine.chains())
	}
}

// uv EXITS 2 ON EVERY ERROR, so the code cannot say whose fault it was: a
// lock behind its pyproject is the tree's finding, a network the substrate
// lost is a could-not-run, and a sync that left no venv gave the COPY
// nothing.
func TestPythonReleaseReadsUvsFailuresByTheirWords(t *testing.T) {
	sync := `"uv","sync"`
	engine.reset()
	engine.withTree(pythonStar(""))
	engine.exitCode(sync, 2)
	engine.stderr(sync, "error: The lockfile at `uv.lock` needs to be updated, but `--locked` was provided.")
	wantState(t, runAtom(t, "python:release", ""), 1, "exited 2", "needs to be updated")

	engine.reset()
	engine.withTree(pythonStar(""))
	engine.exitCode(sync, 2)
	engine.stderr(sync, "error: Failed to fetch: `https://pypi.org/simple/mcp/`\n  Caused by: dial tcp: i/o timeout")
	wantState(t, runAtom(t, "python:release", ""), 2, "i/o timeout")

	engine.reset()
	tree := pythonStar("")
	delete(tree, "/app/.venv/bin/iris")
	engine.withTree(tree)
	wantState(t, runAtom(t, "python:release", ""), 1, "left no /app/.venv")
}

// RELEASE() FOLLOWS THE IMAGE'S BASE. A python star's release is its venv,
// handed over under app/.venv for `COPY release/app /app`; a bun star's is
// the release/ its steps left; a star on the go base still compiles.
func TestReleaseFollowsTheImagesBase(t *testing.T) {
	ctx := context.Background()

	engine.reset()
	engine.withTree(pythonStar(`["pg"]`))
	dir, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx)
	if err != nil || dir == nil {
		t.Fatalf("dir %v err %v", dir, err)
	}
	if _, err := dag.Directory().WithDirectory("release", dir).Entries(ctx); err != nil {
		t.Fatalf("the release directory could not be used: %v", err)
	}
	venv := engine.chain(`directory(path:"/app/.venv")`, "{id}")
	if venv == "" || !strings.Contains(venv, `"--extra","pg"`) {
		t.Fatalf("the venv is not read back off the sync:\n%v", engine.chains())
	}
	if c := engine.chain(`withDirectory(`, `path:"app/.venv"`); c == "" || !strings.Contains(c, fakeID(venv)) {
		t.Errorf("the release directory does not carry the venv at app/.venv:\n%s\n%v", c, engine.chains())
	}
	if engine.chain(`"go","build"`) != "" || engine.chain(`"cargo","build"`) != "" {
		t.Errorf("a python star was compiled:\n%v", engine.chains())
	}

	// A bun star's release is its steps' release/.
	engine.reset()
	engine.withTree(bunStar(calliopeSteps))
	dir, err = (&FoundryTools{Source: dag.Directory()}).Release(ctx)
	if err != nil || dir == nil {
		t.Fatalf("dir %v err %v", dir, err)
	}
	if _, err := dag.Directory().WithDirectory("release", dir).Entries(ctx); err != nil {
		t.Fatalf("the release directory could not be used: %v", err)
	}
	if engine.chain(`"apps/calliope/scripts/bundle.ts"`, `directory(path:"/src/release")`) == "" {
		t.Errorf("the bun release is not read off the steps' release/:\n%v", engine.chains())
	}

	// A failed release is an error, never an empty directory; a bun star with
	// no steps is an error that says so.
	engine.reset()
	engine.withTree(pythonStar(""))
	engine.exitCode(`"uv","sync"`, 2)
	engine.stderr(`"uv","sync"`, "error: The lockfile needs to be updated")
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx); err == nil || !strings.Contains(err.Error(), "the release build failed") {
		t.Errorf("err %v", err)
	}
	engine.reset()
	tree := bunStar(calliopeSteps)
	delete(tree, "/dies/fleet/stars/calliope/slag.json")
	engine.withTree(tree)
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx); err == nil || !strings.Contains(err.Error(), "names no tools.build.release") {
		t.Errorf("err %v", err)
	}

	// A Go star on the fleet's go base is the compiled lane's, as before.
	engine.reset()
	engine.withTree(map[string]string{
		"go.mod": "module x\n", "cmd/mnemosyne/main.go": "package main\n",
		".copier-answers.yml": "service_name: mnemosyne\n",
		"Dockerfile":          "FROM " + goBase + "\nCOPY release/mnemosyne /mnemosyne\n",
	})
	if _, err := (&FoundryTools{Source: dag.Directory()}).Release(ctx); err != nil {
		t.Fatalf("err %v", err)
	}
	if engine.chain(`"go","build"`) == "" || engine.chain(`"uv","sync"`) != "" {
		t.Errorf("a go-based image was not compiled by the go lane:\n%v", engine.chains())
	}
}
