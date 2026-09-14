package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE TS LANE against the paper engine (engine_fake_test.go), following
// atoms_go_test.go's exemplar.
//
// Two of this lane's claims are about a chain rather than about an exit code,
// and both are read back here: the fleet's eslint config is WRITTEN OVER the
// repository's and the tree re-mounted (not laid over the mount), and the
// install runs BEFORE the test-file population is counted, so a tree that
// cannot install frozen answers CANNOT RUN rather than being graded on files.

// tsNoTestTree is a package.json tree carrying no file bun would call a test —
// no .test/.spec and no _test_/_spec_ — and nothing the fleet exclude would
// have removed for it either.
var tsNoTestTree = map[string]string{
	"package.json":   "{}",
	"bun.lock":       "{}",
	"src/index.ts":   "export const x = 1\n",
	"src/gate.ts":    "export const gate = 1\n",
	"README.md":      "# x\n",
	".git/HEAD":      "ref: refs/heads/main\n",
	"testing/why.md": "not a test file\n",
}

// THE FLEET'S CONFIG IS WRITTEN INTO THE DIRECTORY AND THE TREE RE-MOUNTED.
// eslint resolves its plugins relative to the config file, so a config outside
// the tree cannot load them. The chain proves it: the /src mount the gate runs
// against is the stocks file laid into the source directory, not r.src.
func TestTSBunGateCommitRemountsTheTreeCarryingTheFleetsEslintConfig(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)

	wantState(t, runAtom(t, "ts:bun-gate-commit", ""), 0)

	c := engine.chain(`"bun","run","gate"`, "exitCode")
	if !strings.Contains(c, checks.ImageTS) {
		t.Errorf("ts:bun-gate-commit must run in the frontend lane image:\n%s", c)
	}

	// The file came off the stocks tree at its one home, went into the source
	// directory as eslint.config.mjs, and THAT directory is what /src is.
	cfg := engine.idOf(`path:"`+checks.StocksRulesets+`/eslint.config.mjs"`, "{id}")
	remount := engine.chain("withFile", `path:"eslint.config.mjs"`)
	if remount == "" {
		t.Fatal("nothing laid eslint.config.mjs into the source directory")
	}
	if !hasCall(remount, "withFile", `path:"eslint.config.mjs"`, `source:"`+cfg+`"`) {
		t.Errorf("the config laid into the tree is not the stocks file:\n%s", remount)
	}
	wantCalls(t, c,
		[]string{"withMountedCache", `path:"/root/.bun/install/cache"`},
		[]string{"withMountedDirectory", `path:"/src"`, `source:"` + fakeID(remount) + `"`},
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withExec", `expect:ANY`, `args:["bun","install","--frozen-lockfile"]`},
		[]string{"withExec", `expect:ANY`, `args:["bun","run","gate"]`},
	)
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("ts:bun-gate-commit must not read GATE_BASE:\n%s", c)
	}
	// The commit-stage atom does not require tests, so it never enumerates.
	if engine.chain("glob(pattern:") != "" {
		t.Error("ts:bun-gate-commit must not count test files; that is ts:bun-gate's flag")
	}

	// The gate script's own exit code is the verdict.
	engine.exitCode(`"bun","run","gate"`, 1)
	engine.stdout(`"bun","run","gate"`, "src/index.ts:1:7 - error TS2322")
	wantState(t, runAtom(t, "ts:bun-gate-commit", ""), 1, "TS2322")

	// A 127 that survives the install is a could-not-run on its own, where the
	// shell body's `|| exit 1` flattened it into FINDINGS.
	engine.exitCode(`"bun","run","gate"`, 127)
	engine.stderr(`"bun","run","gate"`, "prettier: command not found")
	wantState(t, runAtom(t, "ts:bun-gate-commit", ""), 2, "prettier: command not found")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"bun","run","gate"`, "engine went away")
	wantState(t, runAtom(t, "ts:bun-gate-commit", ""), 2, "never ran", "engine went away")
}

// A RULESET THE ATOM CANNOT READ IS A GATE THAT NEVER LOOKED, and that is
// never a pass. Decided in Go, off the stocks tree, before anything runs.
func TestTSGateCannotRunWithoutTheFleetsEslintConfig(t *testing.T) {
	for _, id := range []string{"ts:bun-gate-commit", "ts:bun-gate"} {
		engine.reset()
		engine.withTree(everyLaneTree)
		engine.fail(checks.StocksRulesets+"/eslint.config.mjs", "no such file or directory")

		wantState(t, runAtom(t, id, ""), 2,
			checks.RulesetsDir+"/eslint.config.mjs is absent",
			"foundry-stocks did not mount",
			"no such file or directory")
		if engine.chain(`"bun","install"`) != "" {
			t.Errorf("%s: an unreadable ruleset must not start a container", id)
		}
	}
}

// THE ORDER IS THE OLD BODY'S: the install runs first, so a tree that cannot
// install frozen answers CANNOT RUN rather than being graded on its test
// files. A lockfile that will not install frozen is a fact about the pull's
// reproducibility; reading it as a finding would put it in the wrong queue.
func TestTSGateFrozenLockfileInstallIsACannotRun(t *testing.T) {
	for _, id := range []string{"ts:bun-gate-commit", "ts:bun-gate"} {
		engine.reset()
		engine.withTree(everyLaneTree)
		engine.exitCode(`"bun","install","--frozen-lockfile"`, 1)
		engine.stderr(`"bun","install","--frozen-lockfile"`, "error: lockfile had changes, but lockfile is frozen")

		wantState(t, runAtom(t, id, ""), 2,
			"frozen lockfile install failed", "lockfile is frozen")
		if engine.chain(`"bun","run","gate"`) != "" {
			t.Errorf("%s: a tree that will not install must not run the gate", id)
		}
		if engine.chain("glob(pattern:") != "" {
			t.Errorf("%s: a tree that will not install must not be graded on its test files", id)
		}

		engine.reset()
		engine.withTree(everyLaneTree)
		engine.fail(`"bun","install","--frozen-lockfile"`, "failed to pull frontend-ci: 404")
		wantState(t, runAtom(t, id, ""), 2, "never ran", "404")
	}
}

// NO TESTS IS A FINDING, and the presence check is the atom's own: the repo's
// `gate` script may never reach `bun test` at all, so a tree with no test
// could run a green gate that tested nothing.
func TestTSBunGateRefusesATreeWithNoTestFile(t *testing.T) {
	engine.reset()
	engine.withTree(tsNoTestTree)
	wantState(t, runAtom(t, "ts:bun-gate", ""), 1,
		"no test file in the tree", "nothing is built without tests")
	if engine.chain(`"bun","run","gate"`) != "" {
		t.Error("a tree with no test file must not run the gate")
	}

	// The SAME tree passes the commit-stage atom: the flag is the only
	// difference between the two bodies.
	engine.reset()
	engine.withTree(tsNoTestTree)
	wantState(t, runAtom(t, "ts:bun-gate-commit", ""), 0)

	// One test file anywhere is enough, and it is found at the root as surely
	// as three directories down.
	for _, f := range []string{"a.test.ts", "src/deep/b.spec.tsx", "src/my_test_helpers.js"} {
		engine.reset()
		tree := map[string]string{}
		for k, v := range tsNoTestTree {
			tree[k] = v
		}
		tree[f] = ""
		engine.withTree(tree)
		wantState(t, runAtom(t, "ts:bun-gate", ""), 0)
	}

	// node_modules is gone before the patterns are applied (GateExclude),
	// which is what the shell body's `-path ./node_modules -prune` did by
	// hand: a dependency's tests are not this repo's.
	engine.reset()
	tree := map[string]string{}
	for k, v := range tsNoTestTree {
		tree[k] = v
	}
	tree["node_modules/left-pad/index.test.js"] = ""
	engine.withTree(tree)
	wantState(t, runAtom(t, "ts:bun-gate", ""), 1, "no test file in the tree")

	// A POPULATION THAT CANNOT BE ENUMERATED IS A COULD-NOT-RUN, where
	// `find ... 2>/dev/null` read a broken scan as an empty tree.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`pattern:"**/*.test.ts"`, "the filter would not evaluate")
	wantState(t, runAtom(t, "ts:bun-gate", ""), 2,
		"could not be enumerated", "unknown rather than empty")
}

// THE REGISTRY IS NAMED, AND IT IS npmjs.org. `bun audit` scans nothing
// locally — it posts the lockfile's package set to the registry's bulk
// advisory endpoint — and a Nexus mirror serves packages, not that API.
func TestTSBunAuditNamesTheRegistryAndReadsBunsExit(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)

	wantState(t, runAtom(t, "ts:bun-audit", ""), 0)

	c := engine.chain(`"bun","audit"`, "exitCode")
	wantCalls(t, c,
		[]string{"withEnvVariable", `name:"BUN_CONFIG_REGISTRY"`, `value:"https://registry.npmjs.org/"`},
		[]string{"withMountedCache", `path:"/root/.bun/install/cache"`},
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withExec", `expect:ANY`, `args:["bun","audit","--audit-level=high"]`},
	)
	if strings.Contains(c, `"bun","install"`) {
		t.Errorf("nothing is installed here; the audit reads the lockfile:\n%s", c)
	}
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("ts:bun-audit must not read GATE_BASE:\n%s", c)
	}

	// bun exits 1 when something at or above the threshold is reported, and
	// that 1 is the finding.
	engine.exitCode(`"bun","audit"`, 1)
	engine.stdout(`"bun","audit"`, "1 high severity vulnerability: GHSA-xxxx")
	wantState(t, runAtom(t, "ts:bun-audit", ""), 1, "GHSA-xxxx")

	engine.exitCode(`"bun","audit"`, 127)
	wantState(t, runAtom(t, "ts:bun-audit", ""), 2)

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"bun","audit"`, "dial tcp: connection refused")
	wantState(t, runAtom(t, "ts:bun-audit", ""), 2, "never ran", "connection refused")
}

// ts:mutation is rustTSMutation with the ts spec: the bare lane container as
// its base, two probes, FIVE phases — StrykerJS installs and builds where
// cargo-mutants does neither.
func TestTSMutationRunsItsFivePhases(t *testing.T) {
	engine.reset()
	engine.withTree(rustTSTree(map[string]string{
		".copier-answers.yml":   "critical_modules: src/gate.ts\n",
		"/tmp/mutation/verdict": "0\n",
		"/tmp/mutation/reason":  "every mutant killed",
	}))

	wantState(t, runAtom(t, "ts:mutation", "abc123"), 0)

	c := engine.chain(`ts.sh","score"`, "exitCode")
	if !strings.Contains(c, checks.ImageTS) {
		t.Errorf("ts:mutation must run in the frontend lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withMountedDirectory", `path:"/stocks"`},
		[]string{"withEnvVariable", `name:"GATE_BASE"`, `value:"abc123"`},
		[]string{"withEnvVariable", `name:"MUT_BASE"`, `value:"abc123"`},
		[]string{"withEnvVariable", `name:"MUT_MODE"`, `value:"diff"`},
		[]string{"withEnvVariable", `name:"MUT_DIR"`, `value:"/tmp/mutation"`},
		[]string{"withEnvVariable", `name:"MUT_MODULES"`, `value:"src/gate.ts"`},
		[]string{"withExec", `args:["bash","--version"]`},
		[]string{"withExec", `args:["bun","--version"]`},
	)
	for _, probe := range []string{`args:["bash","--version"]`, `args:["bun","--version"]`} {
		if hasCall(c, "withExec", probe, "expect:ANY") {
			t.Errorf("%s is provisioning and must run under the default Expect:\n%s", probe, c)
		}
	}
	for _, phase := range []string{"resolve", "install", "build", "mutate", "score"} {
		if !hasCall(c, "withExec", "expect:ANY", `"/stocks/ci/lib/mutation/ts.sh","`+phase+`"`) {
			t.Errorf("ts:mutation lacks phase %s under ANY:\n%s", phase, c)
		}
	}
	// The rust spec's base, not this one's: nothing here fetches crates.
	if strings.Contains(c, `"cargo"`) {
		t.Errorf("ts:mutation must not carry the rust lane's provisioning:\n%s", c)
	}

	engine.withTree(map[string]string{
		"/tmp/mutation/verdict": "1\n",
		"/tmp/mutation/reason":  "2 mutants survived StrykerJS",
	})
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 1,
		"2 mutants survived StrykerJS", "scoped to the declared critical modules: src/gate.ts")
}

func TestTSMutationCannotRunPaths(t *testing.T) {
	base := map[string]string{
		"/tmp/mutation/verdict": "0\n",
		"/tmp/mutation/reason":  "every mutant killed",
	}

	// THE PHASES NEVER EXIT NON-ZERO ON THEIR OWN — reaching a verdict is the
	// score phase's job — so one that does is a broken script, named.
	for _, phase := range []string{"resolve", "install", "build", "mutate", "score"} {
		engine.reset()
		engine.withTree(rustTSTree(base))
		engine.exitCode(`ts.sh","`+phase+`"`, 2)
		engine.stdout(`ts.sh","`+phase+`"`, "stryker: unknown reporter")
		wantState(t, runAtom(t, "ts:mutation", "abc123"), 2,
			"phase "+phase, "unknown reporter")
	}

	engine.reset()
	engine.withTree(rustTSTree(base))
	engine.fail(`"bun","--version"`, "exec: \"bun\": not found")
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 2, "never ran", "not found")

	engine.reset()
	engine.withTree(rustTSTree(base))
	engine.fail("ci/lib/mutation/ts.sh", "no such file or directory")
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 2,
		"/stocks/ci/lib/mutation/ts.sh is absent", "foundry-stocks did not mount")

	engine.reset()
	engine.withTree(everyLaneTree) // no verdict file
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 2, "no verdict")
}

// A LINKED WORKTREE'S `.git` IS A FILE, AND IT DANGLES IN THE CONTAINER. The
// mutation lane reads history through git, so gitReady gives the tree a
// throwaway repository and reconstructs origin from the gitdir path — an
// exemption keyed on the repository must not evaporate because the push came
// from a worktree.
func TestTSMutationRebuildsAWorktreesRepository(t *testing.T) {
	engine.reset()
	tree := rustTSTree(map[string]string{
		"/tmp/mutation/verdict": "0\n",
		"/tmp/mutation/reason":  "every mutant killed",
		".git":                  "gitdir: /home/rob/Forge/Outputs/foundry-tools/.git/worktrees/Tesla19\n",
	})
	delete(tree, ".git/HEAD")
	engine.withTree(tree)

	wantState(t, runAtom(t, "ts:mutation", "abc123"), 0)

	c := engine.chain(`ts.sh","score"`, "exitCode")
	wantCalls(t, c,
		[]string{"withExec", `args:["git","config","--global","--add","safe.directory","*"]`},
		[]string{"withExec", `args:["git","init","-q","."]`},
		[]string{"withExec", `args:["git","add","-A"]`},
		[]string{"withExec", `args:["git","remote","add","origin","/home/rob/Forge/Outputs/foundry-tools.git"]`},
	)

	// A primary checkout's `.git` is a directory: nothing to rebuild, and no
	// origin to reconstruct.
	engine.reset()
	engine.withTree(rustTSTree(map[string]string{
		"/tmp/mutation/verdict": "0\n",
		"/tmp/mutation/reason":  "every mutant killed",
	}))
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 0)
	c = engine.chain(`ts.sh","score"`, "exitCode")
	if strings.Contains(c, `"git","init"`) || strings.Contains(c, `"git","remote"`) {
		t.Errorf("a primary checkout's history is already there; nothing may rebuild it:\n%s", c)
	}
}

// A repo with no package.json never reaches a runner.
func TestTheTSLaneIsAbsentWithoutAPackageJson(t *testing.T) {
	for _, id := range []string{"ts:bun-gate-commit", "ts:bun-gate", "ts:bun-audit", "ts:mutation"} {
		engine.reset()
		engine.withTree(map[string]string{"Cargo.toml": "[package]\nname = \"x\"\n"})
		v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", ""), id)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "no package.json") {
			t.Errorf("%s: want an absent 0 naming package.json, got %+v", id, v)
		}
		if len(engine.chains()) != 1 {
			t.Errorf("%s: an absent lane must cost one entries read, not a container: %d queries",
				id, len(engine.chains()))
		}
	}
}
