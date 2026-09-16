package main

import (
	"context"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE RUST LANE, AS TYPED CHAINS. Read runtime.go's eight rules and
// atoms_go.go's exemplar first; this file follows both.
//
// THE DEPENDENCIES ARE FETCHED ONCE, ON THEIR OWN (cargoDeps), and every
// atom that compiles branches from that layer. The first cut had no
// provisioning step — cargo resolves its registry as part of the command —
// and it was wrong in a way only the engine could show: MEASURED 2026-09-12 on
// tongs, rust:cargo-clippy and rust:cargo-test running concurrently against
// the shared registry volume, cargo-test answered "failed to download
// `deranged v0.5.8`" while clippy fetched the same crate. cargo's
// package-cache lock lives in CARGO_HOME, which the mount does not cover, so
// two containers do not see each other's lock. One fetch, cached as a layer
// keyed on the tree, and the parallel atoms only read.
//
// The fetch is also rule 1 of runtime.go: a registry outage is a could-not-run
// the door re-asks, not a finding the committer is told to fix.

func init() {
	register("rust:cargo-fmt", rustCargoFmt)
	register("rust:cargo-clippy", rustCargoClippy)
	register("rust:cargo-test", rustCargoTest)
	register("rust:cargo-audit", rustCargoAudit)
	register("rust:mutation", rustMutation)
}

// cargoVerdict is verdict() with CARGO'S EXIT VOCABULARY TRANSLATED FIRST
// (checks.CargoExit says why: cargo answers 101, not 1, when the command
// failed, and StateFor reads a 101 as could-not-run). The shell bodies this
// ports all ended `|| exit 1`, mapping every non-zero cargo code to FINDINGS;
// this is that mapping narrowed to the one code it existed for, in a pure
// function with a table under it, which is runtime.go's rule 2.
func cargoVerdict(ctx context.Context, a checks.AtomDef, ctr *dagger.Container) checks.Verdict {
	out, code, err := output(ctx, ctr)
	if err != nil {
		return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
	}
	return checks.VerdictOf(a, checks.CargoExit(code), out)
}

// cargo fmt --all --check is clean.
//
// --check IS THE WHOLE ATOM: rustfmt rewrites nothing and exits 1 for a file it
// would have rewritten, so there is nothing to provision, nothing to count and
// nothing to parse. --all is the workspace, not the crate the manifest happens
// to point at first.
// cargoDeps is the rust lane's provisioned base: the lane container with the
// dependency graph fetched. See the file header for the measurement behind it.
func (r *run) cargoDeps() *dagger.Container {
	return r.lane(checks.ImageRust).WithExec([]string{"cargo", "fetch"})
}

func rustCargoFmt(ctx context.Context, r *run) checks.Verdict {
	return cargoVerdict(ctx, checks.AtomByID("rust:cargo-fmt"),
		r.lane(checks.ImageRust).WithExec([]string{"cargo", "fmt", "--all", "--check"}, anyExit))
}

// cargo clippy is clean under the fleet's lint set, warnings denied.
//
// THE LINT SET IS NAMED ON THE COMMAND LINE. The rust template pours
// `[workspace.lints.clippy] all = "warn"` into Cargo.toml; a crate can edit
// that table, and `#![allow]` in source is stop-justifications' to catch.
// Flags after `--` reach rustc last and win over the table, so the fleet's set
// is the one enforced whatever the manifest says (Rob, 2026-09-11).
//
// --all-targets is the other half: without it clippy grades the library and
// leaves tests, benches and examples ungraded, which is a partial scan
// reporting a clean one.
func rustCargoClippy(ctx context.Context, r *run) checks.Verdict {
	return cargoVerdict(ctx, checks.AtomByID("rust:cargo-clippy"),
		r.cargoDeps().WithExec([]string{
			"cargo", "clippy", "--workspace", "--all-targets", "--",
			"-W", "clippy::all", "-D", "warnings",
		}, anyExit))
}

// cargo test --workspace passes, and there is something for it to pass.
//
// NO TESTS IS A FINDING. A workspace with no #[test] prints "running 0 tests"
// and exits 0, so a crate with no test anywhere read green. Rob, 2026-09-11:
// nothing is built without tests. libtest's own --list names every test as
// `<path>: test`; a workspace that lists none is red before the suite runs
// (checks.CargoListsTests).
//
// THE LIST BUILD IS THE SUITE'S BUILD, SO NOTHING COMPILES TWICE. `cargo test
// -- --list` builds every test binary and then asks each to enumerate itself;
// the `cargo test` that follows finds that build in CARGO_TARGET_DIR — a cache
// volume — and runs it. The listing is therefore free, which is why the count
// is asked for with the real toolchain rather than by grepping for `#[test]`.
//
// A LISTING THAT WOULD NOT BUILD IS A FINDING, NOT A COULD-NOT-RUN. Code that
// does not compile is the committer's to fix, and the old body said so; what
// it named is the first rustc diagnostic (checks.FirstCargoError), because the
// rest of the output is that error's fallout.
//
// THE SUITE RUNS IN A TREE GIT CAN READ. A test may probe the repository it
// runs in, and cerberus's porosity probe does (`rev-parse --is-inside-work-tree`
// on its own crate directory). From a linked worktree, /src keeps a `.git` FILE
// whose gitdir is a host path that does not exist in here, so that probe read
// "not a repository" and the pre-push refused a suite the door's whole clone
// passes. gitReady is the fix the mutation atom already carries.
func rustCargoTest(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("rust:cargo-test")
	lane := r.gitReady(ctx, r.cargoDeps())

	listed, code, err := output(ctx, lane.WithExec(
		[]string{"cargo", "test", "--workspace", "--", "--list"}, anyExit))
	if err != nil {
		return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
	}
	if code != 0 {
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - the tests did not build: "+checks.FirstCargoError(listed))
	}
	if !checks.CargoListsTests(listed) {
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - no test in the workspace; nothing is built without tests")
	}
	return cargoVerdict(ctx, a, lane.WithExec([]string{"cargo", "test", "--workspace"}, anyExit))
}

// cargo audit reports no known vulnerability.
//
// CARGO-AUDIT IS BAKED INTO rust-ci. The body this ports ran `cargo install
// cargo-audit --locked` first — a no-op for the binary, which the image
// already carries, that still resolved the whole registry index before
// deciding so. The provisioning step is now the PROBE: `cargo audit --version`
// under the default Expect, so an image that lost the binary is state 2 with
// the exec's own error rather than a green from a tool that never ran.
//
// The tool's exit code reaches the verdict unmapped, unlike the three cargo
// subcommands above: `cargo audit` is an external subcommand cargo execs and
// whose status it passes through, so a 1 here is cargo-audit's own "there is
// an advisory" and a 101 is cargo failing to run it at all — which is a
// could-not-run and should read as one.
func rustCargoAudit(ctx context.Context, r *run) checks.Verdict {
	return verdict(ctx, checks.AtomByID("rust:cargo-audit"),
		r.cargoDeps().
			WithExec([]string{"cargo", "audit", "--version"}).
			WithExec([]string{"cargo", "audit"}, anyExit))
}

// Every viable mutant cargo-mutants makes of this pull's changes to the
// declared critical modules is killed by the tests.
//
// THE COPIES BUILD IN THEIR OWN TARGET DIRECTORIES, NOT THE GATE'S CACHE.
// cargo-mutants copies the tree once per job and mutates each copy under
// /tmp/mutation/tmp/cargo-mutants-src-*; with CARGO_TARGET_DIR exported by
// the lane (checks.CachesFor) every copy AND the gate's cargo-test built into
// one /cache/cargo-target. cargo's artifact hash is the package id RELATIVE
// TO THE WORKSPACE ROOT, so two copies of one crate at two paths name the
// same deps/<crate>-<hash> — measured 2026-09-13 with cargo 1.97.1: two
// copies, one target dir, one artifact `oracle-3bf3bdcccbbc7eb8` — and the
// dep-info that decides freshness lists the OTHER copy's absolute source
// paths, so a copy whose own source changed still reads fresh and runs the
// sibling's binary (the same oracle: a copy's f() changed, no Compiling
// line, the sibling's test passed for it). That is foundry-tools#8869 as
// seen across gavel #21, furnace #33, bellows #25/#26: `String + &str`
// mutants reported VIABLE, a same-file unit test reported MISSED, the gate
// red on NotFound with a mutants copy baked into CARGO_MANIFEST_DIR.
//
// WITHOUT THE VARIABLE cargo builds each copy under its own `target`, which
// cargo-mutants creates per job ("one build directory per job"). Correct
// and slower: the dependency graph compiles once per run instead of once
// per volume; the registry volume still serves the sources. The mount goes
// too, so nothing in this container can reach the gate's artifacts by
// accident. The job count is two, not rust.sh's four: two copies is two
// concurrent cargo builds, and the engine's exec tree is bounded at 6G with
// two steps in flight (infra#8830) — four copies at a rustc each is the
// shape that gets a compile killed and the atom filed as could-not-run.
//
// THE MEASUREMENT IS PLAIN EXECS, SETTLED IN GO. foundry-stocks'
// ci/lib/mutation/rust.sh ran here as three bash phases that wrote a verdict
// file; git, cargo metadata and cargo mutants now run as their own execs and
// checks.RustMutationVerdict reads what they left.
//
// THE HISTORY IS THERE IN THE LANE THAT MATTERS. The mutation Job clones the
// repository whole, so the base resolves and the diff is real. A local
// pre-push hands the engine a linked worktree, which gitReady turns into a
// throwaway repository with no history: the base does not resolve, the atom
// stands down 0 saying so, and the door's Job is the one that measures.
func rustMutation(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("rust:mutation")
	// critical_modules is read in Go off the tree, before any container runs.
	// A missing answers file is an empty declaration.
	answers, _ := r.src.File(".copier-answers.yml").Contents(ctx)
	mods := checks.CriticalModules(answers)
	scope := checks.MutationScope(a.ID, mods)
	settle := func(state int, reason string) checks.Verdict {
		v := checks.VerdictOf(a, state, a.ID+": "+reason)
		v.Reason = scope + "\n" + v.Reason
		return v
	}
	neverRan := func(err error) checks.Verdict { return settle(2, "CANNOT RUN - the atom never ran: "+err.Error()) }

	const noBase = "no usable PR base sha — the diff-scoped mutation gate did not run"
	if r.base == "" {
		return settle(0, noBase)
	}
	ctr := r.gitReady(ctx, r.withBase(r.cargoDeps().
		WithoutEnvVariable("CARGO_TARGET_DIR").
		WithoutMount("/cache/cargo-target"))).
		// Provisioning, under the default Expect: an image without cargo-mutants
		// is a Dagger error, and the first exec read below files it as never ran.
		WithExec([]string{"cargo", "mutants", "--version"})

	// rev-parse --verify --quiet, not cat-file -e: cat-file answers a missing
	// object with 128, which the engine reports as its own error even under
	// Expect ANY (foundry-tools#63).
	_, code, err := output(ctx, ctr.WithExec([]string{"git", "rev-parse", "--verify", "--quiet", r.base + "^{commit}"}, anyExit))
	if err != nil {
		return neverRan(err)
	}
	if code != 0 {
		return settle(0, noBase)
	}

	// HEAD AS CHECKED OUT: cargo-mutants verifies the diff's `+` lines against
	// the files on disk and hard-errors when they disagree (exit 5), so the only
	// safe head side is the tree this run holds. --relative, because the paths
	// are matched against the tree cargo runs in.
	specs := append([]string{"--"}, checks.RustMutationSpecs(mods)...)
	diff, code, err := output(ctx, ctr.WithExec(append([]string{"git", "diff", "--relative", r.base, "HEAD"}, specs...), anyExit))
	if err != nil {
		return neverRan(err)
	}
	if code != 0 {
		return settle(2, "CANNOT RUN - git could not diff the pull against its base "+r.base+": "+diff)
	}
	if diff == "" {
		return settle(0, "this pull touched none of the critical modules — nothing to mutate")
	}
	if !checks.DiffAddsLines(diff) {
		return settle(0, "this pull only REMOVED lines from the critical modules — nothing to mutate")
	}

	// EVERY WORKSPACE MEMBER THE PULL TOUCHED, each passed as -p
	// (checks.RustTouchedMembers). A metadata read that fails is could-not-run,
	// never a run over the root package alone.
	files, code, err := output(ctx, ctr.WithExec(append([]string{"git", "diff", "--relative", "--name-only", "-z", "--diff-filter=d", r.base, "HEAD"}, specs...), anyExit))
	if err != nil {
		return neverRan(err)
	}
	if code != 0 {
		return settle(2, "CANNOT RUN - git could not list the files the pull changed: "+files)
	}
	meta, code, err := output(ctx, ctr.WithExec([]string{"cargo", "metadata", "--no-deps", "--format-version", "1"}, anyExit))
	if err != nil {
		return neverRan(err)
	}
	if code != 0 {
		return settle(2, "CANNOT RUN - cargo metadata failed, so which workspace members the pull touched is unknown: "+meta)
	}
	packages, err := checks.RustTouchedMembers([]byte(meta), "/src", strings.Split(files, "\x00"))
	if err != nil {
		return settle(2, "CANNOT RUN - could not map the diff onto the workspace members: "+err.Error())
	}

	// MUTATE. The copies go under TMPDIR, outside the tree being mutated.
	args := []string{"cargo", "mutants", "--colors", "never", "-j", strconv.Itoa(rustMutationJobs),
		"--build-timeout", "900", "--minimum-test-timeout", "60"}
	for _, m := range strings.Fields(mods) {
		args = append(args, "-f", m)
	}
	for _, p := range packages {
		args = append(args, "-p", p)
	}
	mutated := ctr.
		WithNewFile(rustMutationDiff, diff+"\n").
		WithDirectory(mutationDir+"/tmp", dag.Directory()).
		WithEnvVariable("TMPDIR", mutationDir+"/tmp").
		WithExec(append(args, "-D", rustMutationDiff), anyExit)
	log, status, err := outputBoth(ctx, mutated)
	if err != nil {
		return neverRan(err)
	}
	// cargo mutants writes no list for an outcome it never reached; a list that
	// does not read is an empty one, and the exit decides the verdict.
	list := func(name string) string {
		s, _ := mutated.File("/src/mutants.out/" + name + ".txt").Contents(ctx)
		return s
	}
	return settle(checks.RustMutationVerdict(checks.RustMutationRun{
		Status: status, Log: log,
		Missed: list("missed"), Caught: list("caught"), Unviable: list("unviable"), Timeout: list("timeout"),
	}))
}

const (
	// rustMutationDiff is the pull's diff as cargo mutants -D reads it.
	rustMutationDiff = mutationDir + "/pr.diff"
	rustMutationJobs = 2
)
