package main

import (
	"context"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE RUST LANE, AS TYPED CHAINS. Read runtime.go's eight rules and
// atoms_go.go's exemplar first; this file follows both.
//
// THERE IS NO PROVISIONING STEP HERE, and that is a decision rather than an
// omission. The go lane needs one (`go mod download` on its own, so a proxy's
// 502 is a could-not-run instead of a finding — atoms_go.go carries the
// measurement); cargo resolves its own registry as part of the command, and
// the registry, git and target caches are already volumes the runtime mounts
// (checks.CachesFor: /usr/local/cargo/registry seeded from the image, the
// config.toml routing crates through Nexus left uncovered so the route
// survives, and CARGO_TARGET_DIR pointed at a volume of its own). A tree whose
// Cargo.lock did not move compiles nothing it compiled last run.

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
		r.lane(checks.ImageRust).WithExec([]string{
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
func rustCargoTest(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("rust:cargo-test")
	lane := r.lane(checks.ImageRust)

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
		r.lane(checks.ImageRust).
			WithExec([]string{"cargo", "audit", "--version"}).
			WithExec([]string{"cargo", "audit"}, anyExit))
}

// Every viable mutant cargo-mutants makes of this pull's changes to the
// declared critical modules is killed by the tests.
func rustMutation(ctx context.Context, r *run) checks.Verdict {
	return rustTSMutation(ctx, r, mutationSpec{
		id:     "rust:mutation",
		image:  checks.ImageRust,
		script: "ci/lib/mutation/rust.sh",
		probes: [][]string{{"bash", "--version"}, {"cargo", "mutants", "--version"}},
		phases: []string{"resolve", "mutate", "score"},
	})
}

// mutationSpec is the only thing that differs between two lanes' mutation
// atoms: the image, the canonical script, what has to be on the PATH for it,
// and the phases that script defines.
type mutationSpec struct {
	id     string
	image  string
	script string // relative to the foundry-stocks tree
	probes [][]string
	phases []string
}

// rustTSMutation is ONE SHAPE, TWO LANGUAGES — rust:mutation and ts:mutation,
// which differ only by mutationSpec. (go: and python: carry the same shape on
// the other port branches; the name is group-scoped so three branches can
// declare it without colliding, and Tesla19 unifies the copies.)
//
// Each runs the canonical script at its one home (/stocks/ci/lib/mutation/<lang>.sh)
// PHASE BY PHASE, in DIFF mode against GATE_BASE — the pull's merge base as the
// door names it — and answers with the verdict the score phase wrote: 0 clean,
// 1 survivors, 2 could not measure. The phases themselves never exit non-zero
// (reaching a verdict is the score phase's job), so a phase that does is a
// broken script, said as CANNOT RUN and naming the phase. That is why each
// phase is its own evaluated exec rather than one chain read at the end: a
// chain would say only that something failed.
//
// THE HISTORY IS THERE IN THE LANE THAT MATTERS. The mutation Job clones the
// repository whole and checks the head out, so `git cat-file -e <base>`
// answers and the diff is real. A local pre-push run hands the engine a linked
// worktree, which gitReady turns into a throwaway repository with no history:
// the resolve phase then stands down 0 with "no usable PR base sha", printed,
// and the door's Job is the one that measures.
//
// A REPO HAS NO SAY. The first cut of these atoms sourced a repo-root
// ci/mutation.env — MUT_* knobs standing in for the retired workflow's inputs —
// and Rob asked why a repo should have a say in anything (2026-09-11). It
// should not: the scripts honour MUT_GATE=false, so that file was a one-line
// switch to turn a fleet gate off, the exact shape stop-justifications exists
// to refuse. It is gone. The one repo fact the lane reads is critical_modules,
// in the answers file the template question put it in — a declaration of WHAT
// matters, not a dial on HOW hard to look. Everything else is the fleet's
// default, here.
//
// GATE_BASE AND MUT_BASE ARE BOTH SET, and this is one of the two atoms
// runtime.go's rule 8 allows to read the base at all: every other atom's cache
// key stays a function of the tree rather than of the pull.
func rustTSMutation(ctx context.Context, r *run, s mutationSpec) checks.Verdict {
	a := checks.AtomByID(s.id)

	// The script IS the tool, read at its one home. Absent means foundry-stocks
	// did not mount, which is a could-not-run about the engine, not the repo.
	if _, err := r.stocks.File(s.script).Contents(ctx); err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - /stocks/"+s.script+" is absent; foundry-stocks did not mount at its one home.\n"+err.Error())
	}

	// critical_modules is read in Go off the tree, before any container runs.
	// A missing answers file is an empty declaration, exactly as the old
	// body's `2>/dev/null` made it.
	answers, _ := r.src.File(".copier-answers.yml").Contents(ctx)
	mods := checks.RustCriticalModules(answers)
	scope := a.ID + ": no critical modules declared - the whole diff is the scope; an empty list is not an opt-out"
	if checks.ModulesDeclared(mods) {
		scope = a.ID + ": scoped to the declared critical modules: " + mods
	}

	ctr := r.gitReady(ctx, r.withBase(r.withStocks(r.lane(s.image))))
	for _, probe := range s.probes {
		// Provisioning, under the default Expect: a missing toolchain is a
		// Dagger error and verdict() files it as state 2.
		ctr = ctr.WithExec(probe)
	}
	ctr = ctr.
		WithEnvVariable("MUT_DIR", mutDir).
		WithEnvVariable("MUT_MODE", "diff").
		WithEnvVariable("MUT_BASE", r.base).
		WithEnvVariable("MUT_MODULES", mods)

	for _, phase := range s.phases {
		next := ctr.WithExec([]string{"bash", "/stocks/" + s.script, phase}, anyExit)
		out, code, err := output(ctx, next)
		if err != nil {
			return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
		}
		if code != 0 {
			return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - phase "+phase+" exited non-zero; the phases never do on their own\n"+out)
		}
		ctr = next
	}

	raw, err := ctr.File(mutDir + "/verdict").Contents(ctx)
	if err != nil {
		raw = ""
	}
	state, ok := checks.MutationScore(raw)
	if !ok {
		return checks.VerdictOf(a, 2, scope+"\n"+a.ID+": CANNOT RUN - the score phase wrote no verdict")
	}
	reason, _ := ctr.File(mutDir + "/reason").Contents(ctx)
	return checks.VerdictOf(a, state, scope+"\n"+a.ID+": "+strings.TrimSpace(reason))
}

// mutDir is where the canonical scripts are told to keep their working state
// and to write the verdict and the reason the atom answers with.
const mutDir = "/tmp/mutation"
