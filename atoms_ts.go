package main

import (
	"context"
	"path"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/rulesets"
	"dagger/foundry-tools/internal/dagger"
)

// THE TS LANE, AS TYPED CHAINS. Read runtime.go's eight rules and atoms_go.go's
// exemplar first.
//
// lint-staged is deliberately absent, for the reason checks_ts.go states: it is
// defined over the git INDEX and the engine receives a directory.

func init() {
	register("ts:bun-gate", tsBunGate)
	register("ts:bun-audit", tsBunAudit)
	register("ts:mutation", tsMutation)
}

// bun run gate passes against a frozen lockfile under the fleet's eslint
// config, and the tree carries tests for it to run.
func tsBunGate(ctx context.Context, r *run) checks.Verdict {
	return tsGate(ctx, r, "ts:bun-gate")
}

// tsGate is the bun gate. It was two atoms — a commit-stage twin that skipped
// the test-file check — and they ran the same `bun run gate` on the same trees:
// over 5,295 gated trees (2026-09-09 → 17) one reported findings on 72 and the
// other on 70. Rob, 2026-09-17: merge them.
//
// THE GATE'S CHECKOUT HAS NO node_modules. The pre-commit hook this ports runs
// in a working tree that already installed; the engine mounts a bare tree.
// MEASURED 2026-09-10T02:38Z gate-calliope-9ed7599: `bun run format:check` →
// "prettier: command not found", exit 127, a red about the runner and not
// about the repo. So the install is part of the atom, and a lockfile that will
// not install frozen is a CANNOT RUN — it is a fact about the pull's
// reproducibility, and reading it as a finding would put it in the wrong
// queue. (A 127 that survives the install now reaches the verdict as a
// could-not-run on its own, where the shell body's `|| exit 1` flattened it
// into FINDINGS. That is the same story's correct ending.)
//
// THE ESLINT CONFIG IS THE FLEET'S, WRITTEN OVER THE REPOSITORY'S. eslint
// resolves its plugins relative to the config file, so a config outside the
// tree cannot load them; the fleet's file is placed at the root before the
// gate runs, and every package's `eslint .` finds it there (the template pours
// one root config and no per-package one). What the repo's copy said decides
// nothing (Rob, 2026-09-11) — overwriting it is the point, not a side effect.
//
// It is written into the DIRECTORY and the tree re-mounted, rather than laid
// over the existing mount with Container.WithFile: /src is a mount, and
// gitReady already establishes that re-mounting is how this runtime edits it.
func tsGate(ctx context.Context, r *run, id string) checks.Verdict {
	a := checks.AtomByID(id)

	// The fleet's ruleset, laid over whatever the repository carries. It is
	// embedded in this module now (CA F18), so the "cannot read the ruleset"
	// could-not-run that used to guard the mount is unreachable rather than
	// handled: an absent embed does not compile.
	ctr := r.lane(checks.ImageTS).
		WithMountedDirectory("/src", r.src.WithNewFile("eslint.config.mjs", rulesets.ESLint))

	installed := ctr.WithExec([]string{"bun", "install", "--frozen-lockfile"}, anyExit)
	out, code, err := output(ctx, installed)
	if err != nil {
		return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
	}
	if code != 0 {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - frozen lockfile install failed\n"+out)
	}

	// NO TESTS IS A FINDING, and the presence check is the atom's own rather
	// than bun's: `bun test` refuses a tree with no test file, but this atom
	// runs the REPO'S `gate` script, which may never reach `bun test` at all.
	// Rob, 2026-09-11: nothing is built without tests. The population is the
	// gate's own — the engine's gitignore filter and the fleet exclude, which
	// is what the shell body's `-path ./node_modules -prune` was doing by hand.
	//
	// The order is the old body's: the install runs first, so a tree that
	// cannot install frozen answers CANNOT RUN rather than being graded on its
	// test files. A population that cannot be enumerated is now a could-not-run
	// too, where `find ... 2>/dev/null` read a broken scan as an empty tree.
	tests, err := r.population(ctx, checks.TSTestPatterns...)
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the tree could not be enumerated, so its test surface is unknown rather than empty: "+err.Error())
	}
	if len(tests) == 0 {
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - no test file in the tree (bun's pattern: {.test,.spec,_test_,_spec_}.{js,ts,jsx,tsx}); nothing is built without tests")
	}

	out, code, err = output(ctx, installed.WithExec([]string{"bun", "run", "gate"}, anyExit))
	if err != nil {
		return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
	}
	// The script's exit is its failing tool's: tsc's 2 is a finding, not a
	// could-not-run (checks.BunGateExit says why).
	return checks.VerdictOf(a, checks.BunGateExit(code), out)
}

// bun audit reports nothing at high or above.
//
// THE REGISTRY IS NAMED, AND IT IS npmjs.org. The override was in this atom's
// body from the day it was written and it carried no comment (checked back to
// d15427c, the commit that introduced the 28 atoms) — so the REASONING here is
// reconstructed from the mechanism, not quoted from a measurement. `bun audit`
// scans nothing locally: it posts the lockfile's package set to the registry's
// bulk advisory endpoint and reports what comes back. A proxy mirror serves
// packages, not that API, and the fleet mirrors its package managers through
// Nexus wherever it can (measured for cargo — checks.CachesFor leaves
// /usr/local/cargo/config.toml uncovered precisely so the Nexus route
// survives). Against a mirror this atom would ask a host that cannot answer
// and report a clean audit it never performed, which is the zero-file scan
// this module exists to delete. Naming the registry for this one command
// points the question at the host that can answer it; nothing is installed
// here, so no mirror's job is affected.
//
// --audit-level=high is the fleet's threshold: bun exits 1 when something at or
// above it is reported, and that 1 is the finding.
func tsBunAudit(ctx context.Context, r *run) checks.Verdict {
	return audit(ctx, checks.AtomByID("ts:bun-audit"),
		r.lane(checks.ImageTS).
			WithEnvVariable("BUN_CONFIG_REGISTRY", "https://registry.npmjs.org/").
			WithExec([]string{"bun", "audit", "--audit-level=high"}, anyExit))
}

// Every mutant StrykerJS makes of this pull's changes to the declared critical
// modules is killed by the tests.
//
// THE MEASUREMENT IS PLAIN EXECS, SETTLED IN GO. foundry-stocks'
// ci/lib/mutation/ts.sh ran here as five bash phases with four node helpers
// beside it; git, bun, curl and stryker now run as their own execs and
// checks.TSMutationVerdict reads what they left.
//
// STRYKER RUNS UNDER NODE, NOT BUN. The package's own node_modules/.bin/stryker
// is exec'd, and its shebang picks node: `bunx --bun stryker` forces bun as the
// runtime, and Stryker's plugin loader does not survive it (the vitest runner
// fails to register after instrumenting every mutant). bun installs; node runs.
//
// THE LOCKFILE IS THE PIN. `bun install --frozen-lockfile`, never retried
// without the flag: an install that quietly resolved something else is the
// failure this gate exists to prevent. A failed install is diagnosed against
// the registry the lockfile names (checks.DiagnoseInstall).
func tsMutation(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("ts:mutation")
	// critical_modules is read in Go off the tree, before any container runs.
	answers, _, _ := fileIfPresent(ctx, r.src, ".copier-answers.yml")
	mods := checks.CriticalModules(answers)
	scope := checks.MutationScope(a.ID, mods)
	// WHAT THE RUNNER PATCH DID RIDES ON EVERY VERDICT THIS ATOM FILES, beside
	// the scope, for the reason checks.ReportVitestRunners gives: a score is
	// worth what the runner under it is, and this lane used to say nothing at
	// all about it. Empty until the patcher has run, so a stand-down before the
	// install reads exactly as it did.
	runner := ""
	settle := func(state int, reason string) checks.Verdict {
		v := checks.VerdictOf(a, state, a.ID+": "+reason)
		v.Reason = scope + runner + "\n" + v.Reason
		return v
	}
	neverRan := func(err error) checks.Verdict { return settle(2, "CANNOT RUN - the atom never ran: "+err.Error()) }

	const noBase = "no usable PR base sha — the diff-scoped mutation gate did not run"
	if r.base == "" {
		return settle(0, noBase)
	}
	ctr := r.gitReady(ctx, r.withBase(r.lane(checks.ImageTS))).
		// Provisioning, under the default Expect: an image without bun is a
		// Dagger error, and the first exec read below files it as never ran.
		WithExec([]string{"bun", "--version"})

	// The change set starts at the merge base, not at the base the door named
	// (run.changeBase): main's tip moves under an open pull.
	since, err := r.changeBase(ctx, ctr)
	if err != nil {
		return settle(2, "CANNOT RUN - "+err.Error())
	}
	if since == "" {
		return settle(r.missingBase(noBase))
	}

	// The diff is taken at the root and each range handed to the package that
	// owns it (checks.PlanStryker).
	diff, code, err := output(ctx, ctr.WithExec(append([]string{"git", "diff", "--unified=0", since, "HEAD", "--"}, checks.TSMutationSpecs(mods)...), anyExit))
	if err != nil {
		return neverRan(err)
	}
	if code != 0 {
		return settle(2, "CANNOT RUN - git could not diff the pull against its base "+since+": "+diff)
	}
	if diff == "" {
		return settle(0, "this pull touched none of the critical modules — nothing to mutate")
	}
	ranges := checks.StrykerRanges(diff)
	if len(ranges) == 0 {
		return settle(0, "this pull only REMOVED lines from the critical modules — nothing to mutate")
	}
	configs, err := r.src.Glob(ctx, "**/*stryker.con*")
	if err != nil {
		return settle(2, "CANNOT RUN - could not enumerate the tree's Stryker configs: "+err.Error())
	}
	plan, orphans := checks.PlanStryker(ranges, checks.StrykerConfigDirs(configs))
	// A range no config owns is a FINDING, not a could-not-run: a package this
	// pull changes that declares no mutation run is the committer's to set up,
	// and nothing with tests goes un-mutation-tested (Rob, 2026-09-11).
	if len(orphans) > 0 {
		return settle(1, "no Stryker config owns "+strings.Join(orphans[:min(len(orphans), 5)], ",")+
			" — every package this pull changes must carry its own stryker config and devDependencies; nothing with tests goes un-mutation-tested")
	}

	installed := ctr.WithExec([]string{"bun", "install", "--frozen-lockfile"}, anyExit)
	log, status, err := outputBoth(ctx, installed)
	if err != nil {
		return neverRan(err)
	}
	if status != 0 {
		return settle(checks.DiagnoseInstall(log, status, npmRegistry{ctx, ctr}))
	}
	installed, patch, err := patchVitestRunners(ctx, installed)
	if err != nil {
		return neverRan(err)
	}
	runner = "\n" + patch.Note
	if patch.Blocked != "" {
		return settle(2, "CANNOT RUN - "+patch.Blocked)
	}

	runs := make([]checks.StrykerRun, 0, len(plan))
	for _, pkg := range plan {
		dir := path.Join("/src", pkg.Dir)
		bin := "/src/node_modules/.bin/stryker"
		for _, c := range checks.StrykerBinCandidates(pkg.Dir) {
			if names, err := installed.Directory(path.Join("/src", path.Dir(c))).Entries(ctx); err == nil && slices.Contains(names, "stryker") {
				bin = path.Join("/src", c)
				break
			}
		}
		mutated := installed.WithWorkdir(dir).
			WithExec([]string{bin, "run", "--mutate", pkg.Mutate(), "--concurrency", "4", "--reporters", "clear-text,json"}, anyExit)
		log, status, err := outputBoth(ctx, mutated)
		if err != nil {
			return neverRan(err)
		}
		// A file that does not read is absent: no report, no exemptions.
		report, _, _ := ctrFileIfPresent(ctx, mutated, dir+"/reports/mutation/mutation.json")
		exemptions, _, _ := ctrFileIfPresent(ctx, mutated, dir+"/stryker-honest.json")
		runs = append(runs, checks.StrykerRun{Dir: pkg.Dir, Status: status, Log: log, Report: report, Exemptions: exemptions})
	}
	state, reason, found := checks.TSMutationVerdict(runs)
	v := settle(state, reason)
	// Built from the structured score, like go:mutation's — never parsed back
	// out of the rendered report (see checks.FindingsOf).
	v.Findings = found
	return v
}

// npmRegistry answers checks.DiagnoseInstall's questions with curl, from the lane
// the install failed in — the registry the lockfile names is the one that lane
// reaches.
type npmRegistry struct {
	ctx context.Context
	ctr *dagger.Container
}

func (g npmRegistry) Versions(reg, name string) []string {
	out, code, err := output(g.ctx, g.ctr.WithExec([]string{"curl", "-fsSL", reg + "/" + name}, anyExit))
	if err != nil || code != 0 {
		return nil
	}
	versions, _ := checks.RegistryVersions([]byte(out))
	return versions
}

func (g npmRegistry) Serves(url string) bool {
	_, code, err := output(g.ctx, g.ctr.WithExec([]string{"curl", "-fsSI", "-o", "/dev/null", url}, anyExit))
	return err == nil && code == 0
}

// patchVitestRunners applies checks.PatchVitestRunner to every installed copy
// of @stryker-mutator/vitest-runner (stryker-js#6210). find does not follow
// symlinks, so a copy bun links into a package is patched once, at its real
// path, and the patch is a new file in the layer, never a write through bun's
// cache hardlinks.
//
// THE ROOT IS IN THE ARGV, not inherited from the workdir. `find .` said the
// same thing here only because this lane's workdir happens to be /src; the
// retired bash body searched $MUT_INSTALL_ROOT, which defaulted to the PACKAGE,
// and found nothing in a workspace that hoists. Naming the root spells the
// claim the search is making, and checks.ReportVitestRunners repeats it back.
//
// AND THE PATCH IS READ BACK WITH grep, IN THE CONTAINER. Whether the bytes
// arrive is not something this lane should take on faith: /src is a mount, and
// the existing test for this patch asserts only that withNewFile was CALLED.
// MEASURED against a real engine 2026-09-16 (dagger v0.21.9): a withNewFile
// over a path an earlier exec created inside a mounted directory is what the
// next exec's `cat` reads, and Container.File does follow a symlinked directory
// component — so the mechanism is sound and both of those were dead ends for
// gijmo-ui#28. The grep stays anyway, because it reads the file from the place
// node will load it and costs one exec, and because the answer being obvious in
// hindsight is exactly what the silence hid.
func patchVitestRunners(ctx context.Context, ctr *dagger.Container) (*dagger.Container, checks.VitestRunnerReport, error) {
	const root = "/src"
	found, code, err := output(ctx, ctr.WithExec([]string{"find", root, "-type", "f", "-path", "*/node_modules/@stryker-mutator/vitest-runner/package.json"}, anyExit))
	if err != nil {
		return ctr, checks.VitestRunnerReport{}, err
	}
	if code != 0 {
		// A search that fails patches nothing, and now says so: the survivors it
		// produces are a fact about this search, not about the pull's tests.
		return ctr, checks.VitestRunnerReport{
			Note: "@stryker-mutator/vitest-runner: the search of " + root + " failed",
			Blocked: "find could not enumerate " + root +
				" for installed copies of @stryker-mutator/vitest-runner, so no copy was inspected and none patched:\n" + found,
		}, nil
	}
	read := func(p string) string {
		s, _ := ctr.File(p).Contents(ctx)
		return s
	}
	var copies []checks.VitestRunnerCopy
	for _, pkg := range strings.Fields(found) {
		dir := path.Dir(pkg)
		files := make([]string, len(checks.VitestRunnerFiles))
		for i, f := range checks.VitestRunnerFiles {
			files[i] = read(path.Join(dir, f))
		}
		c := checks.VitestRunnerCopy{
			Dir:    dir,
			Runner: checks.PackageVersion(read(pkg)),
			Vitest: checks.PackageVersion(read(path.Join(dir, "../../vitest/package.json"))),
		}
		for i, src := range checks.PatchVitestRunner(c.Runner, c.Vitest, files) {
			target := path.Join(dir, checks.VitestRunnerFiles[i])
			ctr = ctr.WithNewFile(target, src)
			c.Patched++
			_, grep, err := output(ctx, ctr.WithExec([]string{"grep", "-qF", checks.VitestRunnerNewJoin, target}, anyExit))
			if err != nil {
				return ctr, checks.VitestRunnerReport{}, err
			}
			if grep == 0 {
				c.Verified++
			}
		}
		copies = append(copies, c)
	}
	return ctr, checks.ReportVitestRunners(root, copies), nil
}
