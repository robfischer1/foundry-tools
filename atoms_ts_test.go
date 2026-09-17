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
func TestTSBunGateRemountsTheTreeCarryingTheFleetsEslintConfig(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)

	wantState(t, runAtom(t, "ts:bun-gate", ""), 0)

	c := engine.chain(`"bun","run","gate"`, "exitCode")
	if !strings.Contains(c, checks.ImageTS) {
		t.Errorf("ts:bun-gate must run in the frontend lane image:\n%s", c)
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
		t.Errorf("ts:bun-gate must not read GATE_BASE:\n%s", c)
	}

	// The gate script's own exit code is the verdict.
	engine.exitCode(`"bun","run","gate"`, 1)
	engine.stdout(`"bun","run","gate"`, "src/index.ts:1:7 - error TS2322")
	wantState(t, runAtom(t, "ts:bun-gate", ""), 1, "TS2322")

	// A 127 that survives the install is a could-not-run on its own, where the
	// shell body's `|| exit 1` flattened it into FINDINGS.
	engine.exitCode(`"bun","run","gate"`, 127)
	engine.stderr(`"bun","run","gate"`, "prettier: command not found")
	wantState(t, runAtom(t, "ts:bun-gate", ""), 2, "prettier: command not found")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"bun","run","gate"`, "engine went away")
	wantState(t, runAtom(t, "ts:bun-gate", ""), 2, "never ran", "engine went away")
}

// A RULESET THE ATOM CANNOT READ IS A GATE THAT NEVER LOOKED, and that is
// never a pass. Decided in Go, off the stocks tree, before anything runs.
func TestTSGateCannotRunWithoutTheFleetsEslintConfig(t *testing.T) {
	for _, id := range []string{"ts:bun-gate"} {
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
	for _, id := range []string{"ts:bun-gate"} {
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

// ts:mutation's needles: each exec the atom runs, by the words only it has.
const (
	tsBaseNeedle    = `"git","rev-parse","--verify","--quiet","abc123^{commit}"`
	tsDiffNeedle    = `"git","diff","--unified=0","abc123","HEAD"`
	tsInstallNeedle = `args:["bun","install","--frozen-lockfile"]`
	tsFindNeedle    = `"find","/src","-type","f"`
	tsStrykerNeedle = `"--reporters","clear-text,json"`
	tsGlobNeedle    = `glob(pattern:"**/*stryker.con*")`
	tsDiff          = "diff --git a/src/gate.ts b/src/gate.ts\n--- a/src/gate.ts\n+++ b/src/gate.ts\n@@ -1,0 +2,2 @@\n+a\n+b"
)

// tsReport is a Stryker mutation.json over one file, with the mutants given.
func tsReport(mutants string) string {
	return `{"config":{"testRunner":"vitest","mutate":["src/gate.ts:2-3"],"coverageAnalysis":"perTest"},` +
		`"testFiles":{"a.test.ts":{"tests":[{"id":"1"}]}},` +
		`"files":{"src/gate.ts":{"source":"a\nb\n","mutants":[` + mutants + `]}}}`
}

const tsKilled = `{"status":"Killed","mutatorName":"BooleanLiteral","testsCompleted":1,"location":{"start":{"line":2}}}`

// scriptTSMutation answers a pull that added TypeScript lines at the root of a
// repo whose root carries a Stryker config and its bin, an install that
// succeeds, and a run that killed every mutant. No critical modules are
// declared unless tree hands the repo an answers file.
func scriptTSMutation(tree map[string]string) {
	engine.reset()
	base := rustTSTree(map[string]string{
		"stryker.config.json":                 "{}",
		"/src/node_modules/.bin/stryker":      "",
		"/src/reports/mutation/mutation.json": tsReport(tsKilled),
	})
	delete(base, ".copier-answers.yml")
	engine.withTree(base)
	engine.withTree(tree)
	engine.stdout(tsDiffNeedle, tsDiff)
}

// ts:mutation runs git, bun and the package's own stryker as plain execs in the
// frontend lane, and no script: nothing mounts foundry-stocks and nothing runs
// bash.
func TestTSMutationMeasuresTheDiffWithThePackagesStryker(t *testing.T) {
	scriptTSMutation(nil)
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 0)

	c := engine.chain(tsStrykerNeedle, "exitCode")
	if !strings.Contains(c, checks.ImageTS) {
		t.Errorf("ts:mutation must run in the frontend lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withEnvVariable", `name:"GATE_BASE"`, `value:"abc123"`},
		[]string{"withExec", `args:["bun","--version"]`},
		[]string{"withExec", "expect:ANY", `args:["bun","install","--frozen-lockfile"]`},
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withExec", "expect:ANY", `args:["/src/node_modules/.bin/stryker","run","--mutate","src/gate.ts:2-3","--concurrency","4","--reporters","clear-text,json"]`},
	)
	if hasCall(c, "withExec", `args:["bun","--version"]`, "expect:ANY") {
		t.Errorf("the bun probe is provisioning and must run under the default Expect:\n%s", c)
	}
	for _, relic := range []string{`path:"/stocks"`, `"bash"`, "MUT_", `"cargo"`} {
		if strings.Contains(c, relic) {
			t.Errorf("the ported lane still carries %s:\n%s", relic, c)
		}
	}
	// No declaration: every TypeScript source outside the tests is the scope.
	wantCalls(t, engine.chain(tsDiffNeedle, "stdout"),
		[]string{"withExec", "expect:ANY", `args:["git","diff","--unified=0","abc123","HEAD","--","*.ts","*.tsx",":!*.test.ts",":!*.test.tsx",":!*.spec.ts",":!*.spec.tsx",":!*__tests__/*",":!node_modules/"]`})

	// A declared module is a :(glob) pathspec, and survivors are findings with
	// the honest table and the list.
	scriptTSMutation(map[string]string{
		".copier-answers.yml":                 "critical_modules: src/**/*.ts\n",
		"/src/reports/mutation/mutation.json": tsReport(tsKilled + `,{"status":"Survived","mutatorName":"ConditionalExpression","testsCompleted":1,"location":{"start":{"line":3}}}`),
	})
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 1,
		"scoped to the declared critical modules: src/**/*.ts",
		"1 mutant(s) survived or were never covered",
		"| 1 | 1 | 0 | 0 | 50% of 2 | 50% of 2 |",
		"src/gate.ts:3  Survived   ConditionalExpression")
	wantCalls(t, engine.chain(tsDiffNeedle, "stdout"),
		[]string{"withExec", `args:["git","diff","--unified=0","abc123","HEAD","--",":(glob)src/**/*.ts"]`})
}

// In a monorepo each range is mutated in the package whose Stryker config owns
// it, on the nearest stryker bin, and the packages settle as one verdict.
func TestTSMutationRunsEachPackageThatOwnsTheDiff(t *testing.T) {
	scriptTSMutation(map[string]string{
		"packages/engine/stryker.config.mjs":                  "",
		"/src/packages/engine/reports/mutation/mutation.json": tsReport(`{"status":"NoCoverage","mutatorName":"BlockStatement","location":{"start":{"line":2}}}`),
	})
	engine.stdout(tsDiffNeedle, "+++ b/packages/engine/src/index.ts\n@@ -1,0 +2 @@\n+x\n"+tsDiff)
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 1,
		"1 mutant(s) survived or were never covered", "### packages/engine", "### .")
	// Each package runs in its own directory on the nearest bin, the root's
	// here, with its ranges relative to it.
	wantCalls(t, engine.chain(tsStrykerNeedle, "exitCode", `"src/index.ts:2-2"`),
		[]string{"withWorkdir", `path:"/src/packages/engine"`},
		[]string{"withExec", `args:["/src/node_modules/.bin/stryker","run","--mutate","src/index.ts:2-2"`})
	wantCalls(t, engine.chain(tsStrykerNeedle, "exitCode", `"src/gate.ts:2-3"`),
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withExec", `args:["/src/node_modules/.bin/stryker","run","--mutate","src/gate.ts:2-3"`})

	// The package's own bin is nearer than the root's.
	scriptTSMutation(map[string]string{
		"packages/engine/stryker.config.mjs":                  "",
		"/src/packages/engine/node_modules/.bin/stryker":      "",
		"/src/packages/engine/reports/mutation/mutation.json": tsReport(tsKilled),
	})
	engine.stdout(tsDiffNeedle, "+++ b/packages/engine/src/index.ts\n@@ -1,0 +2 @@\n+x")
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 0)
	wantCalls(t, engine.chain(tsStrykerNeedle, "exitCode"),
		[]string{"withExec", `args:["/src/packages/engine/node_modules/.bin/stryker","run","--mutate","src/index.ts:2-2"`})

	// A change no config owns is a finding, and nothing installs.
	scriptTSMutation(nil)
	delete(engine.tree, "stryker.config.json")
	engine.stdout(tsDiffNeedle, "+++ b/apps/web/src/x.ts\n@@ -1,0 +2 @@\n+x")
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 1, "no Stryker config owns apps/web/src/x.ts")
	if engine.chain(tsInstallNeedle) != "" {
		t.Errorf("an unowned change went on to install")
	}
}

// A frozen install that fails is diagnosed against the registry the lockfile
// names, with curl from the lane: a tarball the metadata lists and the proxy
// will not serve is a finding naming the versions that do.
func TestTSMutationDiagnosesAFailedInstall(t *testing.T) {
	const tgz = "https://nexus.example/npm/left-pad/-/left-pad-1.3.0.tgz"
	scriptTSMutation(nil)
	engine.exitCode(tsInstallNeedle, 1)
	engine.stdout(tsInstallNeedle, "GET "+tgz+" - 404\nerror: failed")
	engine.stdout(`"curl","-fsSL","https://nexus.example/npm/left-pad"`, `{"name":"left-pad","versions":{"1.1.0":{},"1.2.0":{},"1.3.0":{}}}`)
	engine.exitCode(`"curl","-fsSI","-o","/dev/null","https://nexus.example/npm/left-pad/-/left-pad-1.2.0.tgz"`, 22)
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 1,
		"Registry index/tarball mismatch: left-pad@1.3.0 IS listed", "Versions that DO serve: 1.1.0", "running again changes nothing")
	if engine.chain(tsStrykerNeedle) != "" {
		t.Errorf("a failed install went on to stryker")
	}

	// Metadata that does not answer lists nothing: the version is not indexed.
	scriptTSMutation(nil)
	engine.exitCode(tsInstallNeedle, 1)
	engine.stdout(tsInstallNeedle, "GET "+tgz+" - 404")
	engine.exitCode(`"curl","-fsSL"`, 22)
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 1, "Version not in registry: left-pad@1.3.0")

	scriptTSMutation(nil)
	engine.exitCode(tsInstallNeedle, 1)
	engine.stdout(tsInstallNeedle, "error: ECONNRESET reading the lockfile's registry")
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 2, "network fault (rc=1)", "ECONNRESET")
}

// stryker-js#6210: a runner 10.0.0 beside vitest 5 is patched in the layer
// stryker runs in; the files are rewritten, never edited in place.
//
// AND THE ATOM SAYS SO ON EVERY VERDICT, pass included. The patch used to be
// invisible: this test asserted only that withNewFile was CALLED, which is a
// claim about the chain and not about the runner stryker loaded, and a repo
// where the search matched nothing said nothing at all (gijmo-ui#28).
func TestTSMutationPatchesTheBrokenVitestRunnerAndSaysIt(t *testing.T) {
	const runner = "/src/node_modules/@stryker-mutator/vitest-runner/"
	const found = "/src/node_modules/@stryker-mutator/vitest-runner/package.json\n"
	old := "x;\nreturn nameParts.join(' ').trim();\n"
	broken := func(vitest string) map[string]string {
		return map[string]string{
			runner + "package.json":                 `{"version":"10.0.0"}`,
			runner + "dist/src/stryker-setup.js":    old,
			runner + "dist/src/test-helpers.js":     old,
			"/src/node_modules/vitest/package.json": `{"version":"` + vitest + `"}`,
		}
	}

	scriptTSMutation(broken("5.0.0"))
	engine.stdout(tsFindNeedle, found)
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 0,
		"@stryker-mutator/vitest-runner: 1 copy(s) under /src",
		"runner 10.0.0, vitest 5.0.0 — patched for stryker-js#6210, 2 file(s), each read back")
	c := engine.chain(tsStrykerNeedle, "exitCode")
	for _, f := range []string{"stryker-setup.js", "test-helpers.js"} {
		wantCalls(t, c, []string{"withNewFile", `path:"` + runner + "dist/src/" + f + `"`, `return nameParts.filter(Boolean).join(' > ').trim();`})
		// The patch is read back out of the container, at the path node loads
		// it from — a withNewFile in the chain is a call, not an arrival. The
		// whole argv is the needle: a chain carries every earlier call too, so
		// the file name alone matches the NEXT file's grep as well.
		readBack := `args:["grep","-qF","` + checks.VitestRunnerNewJoin + `","` + runner + "dist/src/" + f + `"]`
		wantCalls(t, engine.chain(readBack, "exitCode"), []string{"withExec", readBack})
	}
	if install := lastCall(c, "withExec", tsInstallNeedle); install < 0 || lastCall(c, "withNewFile", "stryker-setup.js") < install {
		t.Errorf("the patch must land after the install that wrote the runner:\n%s", c)
	}
	// The root is in the argv, not inherited from the workdir: the retired bash
	// body searched the package and a hoisted runner is the workspace's.
	wantCalls(t, engine.chain(tsFindNeedle),
		[]string{"withExec", `args:["find","/src","-type","f","-path","*/node_modules/@stryker-mutator/vitest-runner/package.json"]`})

	// Under vitest 4 the runner is left as shipped — and that is said too.
	scriptTSMutation(broken("4.1.11"))
	engine.stdout(tsFindNeedle, found)
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 0, "runner 10.0.0, vitest 4.1.11 — left as shipped")
	if hasCall(engine.chain(tsStrykerNeedle, "exitCode"), "withNewFile") {
		t.Errorf("a runner under vitest 4 was patched")
	}

	// A search that matches nothing is the silence this replaces: nothing to
	// patch is not a red, but it is never again unsaid.
	scriptTSMutation(nil)
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 0,
		"@stryker-mutator/vitest-runner: no installed copy under /src")

	// A patch that does not read back is a measurement that is not taken.
	scriptTSMutation(broken("5.0.0"))
	engine.stdout(tsFindNeedle, found)
	engine.exitCode(`"grep","-qF"`, 1)
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 2,
		"PATCH DID NOT LAND — 0 of 2 file(s)",
		"it joins test names with a space", "it is not measured")
	if engine.chain(tsStrykerNeedle) != "" {
		t.Errorf("stryker ran through a runner the lane could not patch")
	}

	// A search that cannot run is the same silence wearing a different hat.
	scriptTSMutation(broken("5.0.0"))
	engine.exitCode(tsFindNeedle, 1)
	engine.stdout(tsFindNeedle, "find: '/src': Permission denied")
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 2,
		"find could not enumerate /src", "Permission denied")
}

func TestTSMutationStandsDownOrCannotRun(t *testing.T) {
	cases := map[string]struct {
		base   string
		script func()
		state  int
		// reason is checked only off a finding or could-not-run: a pass keeps no
		// output (checks.VerdictOf), so a stand-down is told apart by what ran.
		reason  []string
		reached []string
		never   []string
	}{
		"no base": {"", nil, 0, nil, nil, []string{tsBaseNeedle, `args:["bun","--version"]`}},
		"a base the history lacks": {"abc123", func() { engine.exitCode(tsBaseNeedle, 1) }, 0, nil,
			[]string{tsBaseNeedle}, []string{tsDiffNeedle}},
		"no TypeScript changed": {"abc123", func() { engine.stdout(tsDiffNeedle, "") }, 0, nil,
			[]string{tsDiffNeedle}, []string{tsGlobNeedle, tsInstallNeedle}},
		"lines only removed": {"abc123", func() { engine.stdout(tsDiffNeedle, "+++ b/src/gate.ts\n@@ -2,2 +1,0 @@\n-a\n-b") }, 0, nil,
			[]string{tsDiffNeedle}, []string{tsGlobNeedle, tsInstallNeedle}},
		"git cannot diff": {"abc123", func() { engine.exitCode(tsDiffNeedle, 1) }, 2,
			[]string{"git could not diff the pull against its base abc123"}, nil, []string{tsInstallNeedle}},
		"the configs cannot be listed": {"abc123", func() { engine.fail(tsGlobNeedle, "walk failed") }, 2,
			[]string{"could not enumerate the tree's Stryker configs", "walk failed"}, nil, []string{tsInstallNeedle}},
		"zero mutants instrumented": {"abc123", func() {
			engine.exitCode(tsStrykerNeedle, 1)
			engine.stdout(tsStrykerNeedle, "Instrumented 1 source file(s) with 0 mutant(s)\nNo tests were executed")
			delete(engine.tree, "/src/reports/mutation/mutation.json")
		}, 0, nil, []string{tsStrykerNeedle}, nil},
		"no report": {"abc123", func() {
			engine.exitCode(tsStrykerNeedle, 1)
			engine.stdout(tsStrykerNeedle, "ConfigError: No tests were executed")
			delete(engine.tree, "/src/reports/mutation/mutation.json")
		}, 2, []string{"stryker exited 1 and wrote no reports/mutation/mutation.json", "No tests were executed"}, nil, nil},
		"a broken run with a report": {"abc123", func() { engine.exitCode(tsStrykerNeedle, 1) }, 2,
			[]string{"stryker exited 1 — a broken run"}, nil, nil},
		"a report that does not parse": {"abc123", func() {
			engine.withTree(map[string]string{"/src/reports/mutation/mutation.json": "{"})
		}, 2, []string{"the mutation report could not be read"}, nil, nil},
		"no bun in the image": {"abc123", func() { engine.fail(`args:["bun","--version"]`, `exec: "bun": not found`) }, 2,
			[]string{"never ran", `"bun": not found`}, nil, []string{tsDiffNeedle}},
		"the base check never ran":    {"abc123", func() { engine.fail(tsBaseNeedle, "engine gone") }, 2, []string{"never ran"}, nil, []string{tsDiffNeedle}},
		"the diff never ran":          {"abc123", func() { engine.fail(tsDiffNeedle, "engine gone") }, 2, []string{"never ran"}, nil, []string{tsInstallNeedle}},
		"the install never ran":       {"abc123", func() { engine.failLeaf(tsInstallNeedle, "stderr", "engine gone") }, 2, []string{"never ran"}, nil, nil},
		"the runner search never ran": {"abc123", func() { engine.fail(tsFindNeedle, "engine gone") }, 2, []string{"never ran"}, nil, nil},
		"stryker never ran":           {"abc123", func() { engine.failLeaf(tsStrykerNeedle, "stderr", "engine gone") }, 2, []string{"never ran"}, nil, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			scriptTSMutation(nil)
			if c.script != nil {
				c.script()
			}
			wantState(t, runAtom(t, "ts:mutation", c.base), c.state, c.reason...)
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

// A LINKED WORKTREE'S `.git` IS A FILE, AND IT DANGLES IN THE CONTAINER. The
// mutation lane reads history through git, so gitReady gives the tree a
// throwaway repository and reconstructs origin from the gitdir path — an
// exemption keyed on the repository must not evaporate because the push came
// from a worktree.
func TestTSMutationRebuildsAWorktreesRepository(t *testing.T) {
	scriptTSMutation(map[string]string{
		".git": "gitdir: /home/rob/Forge/Outputs/foundry-tools/.git/worktrees/Tesla19\n",
	})
	delete(engine.tree, ".git/HEAD")

	wantState(t, runAtom(t, "ts:mutation", "abc123"), 0)

	c := engine.chain(tsStrykerNeedle, "exitCode")
	wantCalls(t, c,
		[]string{"withExec", `args:["git","config","--global","--add","safe.directory","*"]`},
		[]string{"withExec", `args:["git","init","-q","."]`},
		[]string{"withExec", `args:["git","add","-A"]`},
		[]string{"withExec", `args:["git","remote","add","origin","/home/rob/Forge/Outputs/foundry-tools.git"]`},
	)

	// A primary checkout's `.git` is a directory: nothing to rebuild, and no
	// origin to reconstruct.
	scriptTSMutation(nil)
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 0)
	c = engine.chain(tsStrykerNeedle, "exitCode")
	if strings.Contains(c, `"git","init"`) || strings.Contains(c, `"git","remote"`) {
		t.Errorf("a primary checkout's history is already there; nothing may rebuild it:\n%s", c)
	}
}

// A repo with no package.json never reaches a runner: the lane is a fact about
// the tree, the PLANNER reads that fact once before anything starts
// (run.plan), and the atom answers absent without a container.
func TestTheTSLaneIsAbsentWithoutAPackageJson(t *testing.T) {
	for _, id := range []string{
		"ts:bun-gate", "ts:bun-audit", "ts:mutation",
	} {
		engine.reset()
		engine.withTree(map[string]string{"Cargo.toml": "{}"})
		vs, err := (&FoundryTools{Source: dag.Directory()}).vector(t.Context(), checks.AtomByID(id).Stage, id, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(vs) != 1 {
			t.Fatalf("%s: one atom asked, %d answered", id, len(vs))
		}
		v := vs[0]
		if v.Atom != id || v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "no package.json") {
			t.Errorf("%s: want an absent 0 naming package.json, got %+v", id, v)
		}
		// One entries read and one module walk for the whole plan — never a
		// container, and never a read per atom.
		if len(engine.chains()) > 2 {
			t.Errorf("%s: an absent lane costs the plan's reads, not a container: %d queries",
				id, len(engine.chains()))
		}
	}
}
