// Package rustcargotest is the atom rust:cargo-test: cargo test --workspace
// passes, and there is something for it to pass.
package rustcargotest

import (
	"context"
	"slices"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It dates the tree (atoms.RustStamp), which a shared tree
// would carry to every other atom, so it says it writes it.
var Def = atomkit.Def{
	ID: "rust:cargo-test", Stage: checks.StagePrecommit, Lane: checks.LaneRust,
	Group:      checks.GroupLanguage,
	Desc:       "cargo test --workspace passes, and there is something for it to pass.",
	Surface:    "root:Cargo.toml",
	Toolchain:  "rust",
	WritesTree: true,
	Timeout:    45 * time.Minute,
	Check:      check,
}

const id = "rust:cargo-test"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneRust}

// env is cargo's bound, its lean debuginfo and the cap on mold's link threads:
// the test binaries are the biggest links the gate makes.
var env = slices.Concat(checks.CargoBoundedEnv(), checks.CargoLineTablesEnv(), checks.CargoMoldLinkEnv())

// check lists the suite and runs it.
//
// NO TESTS IS A FINDING. A workspace with no #[test] prints "running 0 tests" and
// exits 0, so a crate with no test anywhere read green. Rob, 2026-09-11: nothing
// is built without tests. libtest's own --list names every test as
// `<path>: test`; a workspace that lists none is red before the suite runs
// (checks.CargoListsTests).
//
// THE LIST BUILD IS THE SUITE'S BUILD. `cargo test -- --list` builds every test
// binary and then asks each to enumerate itself; the `cargo test` that follows
// finds that build in CARGO_TARGET_DIR and runs it.
//
// A LISTING THAT WOULD NOT BUILD IS A FINDING, NOT A COULD-NOT-RUN. Code that does
// not compile is the committer's to fix; what it names is the first rustc
// diagnostic (checks.FirstCargoError), because the rest of the output is that
// error's fallout.
//
// THE SUITE RUNS IN A TREE GIT CAN READ (the adapter's gitReady): a test may probe
// the repository it runs in, and cerberus's porosity probe does.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	// ONE ARGUMENT SET PER CARGO RUN. The whole workspace is one; a repo that
	// scopes per crate names the crates the pull reaches and, for a tests-only
	// suite crate, the test files that belong to them, which are separate runs
	// because cargo cannot select both in one.
	sets := [][]string{{"--workspace"}}
	if scoped, ok := atoms.RustScope(ctx, in, "test"); ok {
		if len(scoped) == 0 {
			return checks.VerdictOf(row, 0, id+": ABSENT - this pull changed nothing a crate tests, so there is no test to run")
		}
		sets = scoped
	}
	if v, ok := atoms.RustStamp(ctx, in, row); !ok {
		return v
	}

	var listed string
	for _, set := range sets {
		out, code := atoms.CargoExec(ctx, in, env, checks.MoldRun(slices.Concat([]string{"cargo", "test"}, set, []string{"--", "--list"}))...)
		if code < 0 {
			return atoms.CouldNotStart(row, out)
		}
		if code != 0 {
			return checks.VerdictOf(row, 1, id+": FINDINGS - the tests did not build: "+checks.FirstCargoError(out))
		}
		listed += out
	}
	if !checks.CargoListsTests(listed) {
		return checks.VerdictOf(row, 1, id+": FINDINGS - no test in the workspace; nothing is built without tests")
	}
	var last checks.Verdict
	for _, set := range sets {
		out, code := atoms.CargoExec(ctx, in, env, checks.MoldRun(slices.Concat([]string{"cargo", "test"}, set))...)
		last = atoms.CargoVerdict(row, out, code)
		if last.State != int(checks.StatePass) {
			return last
		}
	}
	return last
}
