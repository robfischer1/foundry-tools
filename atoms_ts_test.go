package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE TS LANE'S CHAINS against the paper engine (engine_fake_test.go): what is
// left here is ts:mutation. ts:bun-gate lives in atoms/ts/bun-gate and is
// tested beside its file.

// ts:mutation's needles: each exec the atom runs, by the words only it has.
const (
	tsBaseNeedle    = `"git","rev-parse","--verify","--quiet","abc123^{commit}"`
	tsDiffNeedle    = `"git","diff","--unified=0","since0","HEAD"`
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
	engine.stdout(mergeBaseNeedle, sinceSha+"\n")
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
		// The package's own config, wrapped with the fleet's skipped mutators.
		[]string{"withNewFile", `path:"/src/.stryker-forge.mjs"`, `ArithmeticOperator`, `excludedMutations`},
		[]string{"withExec", "expect:ANY", `args:["/src/node_modules/.bin/stryker","run",".stryker-forge.mjs","--mutate","src/gate.ts:2-3","--concurrency","4","--reporters","clear-text,json"]`},
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
		[]string{"withExec", "expect:ANY", `args:["git","diff","--unified=0","since0","HEAD","--","*.ts","*.tsx",":!*.test.ts",":!*.test.tsx",":!*.spec.ts",":!*.spec.tsx",":!*__tests__/*",":!node_modules/"]`})

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
		[]string{"withExec", `args:["git","diff","--unified=0","since0","HEAD","--",":(glob)src/**/*.ts"]`})
}

// In a monorepo each range is mutated in the package whose Stryker config owns
// it, on the nearest stryker bin, and the packages settle as one verdict.
func TestTSMutationRunsEachPackageThatOwnsTheDiff(t *testing.T) {
	scriptTSMutation(map[string]string{
		"packages/engine/stryker.config.mjs":                  "",
		"/src/packages/engine/reports/mutation/mutation.json": tsReport(`{"status":"NoCoverage","mutatorName":"BlockStatement","location":{"start":{"line":2}}}`),
	})
	engine.stdout(tsDiffNeedle, "+++ b/packages/engine/src/index.ts\n@@ -1,0 +2 @@\n+x\n"+tsDiff)
	v := runAtom(t, "ts:mutation", "abc123")
	wantState(t, v, 1,
		"1 mutant(s) survived or were never covered", "### packages/engine", "### .")
	// The verdict carries the mutant as a finding, rooted at the repository,
	// the way go:mutation's do — the door's record reads it, not the prose.
	if len(v.Findings) != 1 || v.Findings[0].Verdict != "violated" || v.Findings[0].Cause != "BlockStatement" ||
		!strings.HasPrefix(v.Findings[0].Subject, "packages/engine/") || v.Findings[0].Probe != "ts:mutation" {
		t.Errorf("findings = %+v", v.Findings)
	}
	// Each package runs in its own directory on the nearest bin, the root's
	// here, with its ranges relative to it.
	wantCalls(t, engine.chain(tsStrykerNeedle, "exitCode", `"src/index.ts:2-2"`),
		[]string{"withWorkdir", `path:"/src/packages/engine"`},
		[]string{"withExec", `args:["/src/node_modules/.bin/stryker","run",".stryker-forge.mjs","--mutate","src/index.ts:2-2"`})
	wantCalls(t, engine.chain(tsStrykerNeedle, "exitCode", `"src/gate.ts:2-3"`),
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withExec", `args:["/src/node_modules/.bin/stryker","run",".stryker-forge.mjs","--mutate","src/gate.ts:2-3"`})

	// The package's own bin is nearer than the root's.
	scriptTSMutation(map[string]string{
		"packages/engine/stryker.config.mjs":                  "",
		"/src/packages/engine/node_modules/.bin/stryker":      "",
		"/src/packages/engine/reports/mutation/mutation.json": tsReport(tsKilled),
	})
	engine.stdout(tsDiffNeedle, "+++ b/packages/engine/src/index.ts\n@@ -1,0 +2 @@\n+x")
	wantState(t, runAtom(t, "ts:mutation", "abc123"), 0)
	wantCalls(t, engine.chain(tsStrykerNeedle, "exitCode"),
		[]string{"withExec", `args:["/src/packages/engine/node_modules/.bin/stryker","run",".stryker-forge.mjs","--mutate","src/index.ts:2-2"`})

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
	if hasCall(engine.chain(tsStrykerNeedle, "exitCode"), "withNewFile", `path:"`+runner) {
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
			[]string{tsBaseNeedle}, []string{mergeBaseNeedle, tsDiffNeedle}},
		"no merge base": {"abc123", func() { engine.exitCode(mergeBaseNeedle, 1) }, 2,
			[]string{"no merge base between the base abc123 and HEAD (exit 1)"}, []string{mergeBaseNeedle}, []string{tsDiffNeedle}},
		"no TypeScript changed": {"abc123", func() { engine.stdout(tsDiffNeedle, "") }, 0, nil,
			[]string{tsDiffNeedle}, []string{tsGlobNeedle, tsInstallNeedle}},
		"lines only removed": {"abc123", func() { engine.stdout(tsDiffNeedle, "+++ b/src/gate.ts\n@@ -2,2 +1,0 @@\n-a\n-b") }, 0, nil,
			[]string{tsDiffNeedle}, []string{tsGlobNeedle, tsInstallNeedle}},
		"git cannot diff": {"abc123", func() { engine.exitCode(tsDiffNeedle, 1) }, 2,
			[]string{"git could not diff the pull against its base since0"}, nil, []string{tsInstallNeedle}},
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
		"the base check never ran":    {"abc123", func() { engine.fail(tsBaseNeedle, "engine gone") }, 2, []string{"never ran"}, nil, []string{mergeBaseNeedle}},
		"the merge base never ran":    {"abc123", func() { engine.fail(mergeBaseNeedle, "engine gone") }, 2, []string{"never ran"}, nil, []string{tsDiffNeedle}},
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
		[]string{"withNewFile", `path:"/etc/gitconfig"`, `directory = *`},
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
		// THREE READS FOR THE WHOLE PLAN — one entries read, one go.mod walk
		// and one .py walk (2026-09-25: the python lane is declared by its
		// files too, so the planner asks about them the way it asks about Go
		// modules). The number this pins is CONSTANT PER RUN: never a
		// container, and never a read per atom.
		if len(engine.chains()) > 3 {
			t.Errorf("%s: an absent lane costs the plan's reads, not a container: %d queries",
				id, len(engine.chains()))
		}
	}
}
