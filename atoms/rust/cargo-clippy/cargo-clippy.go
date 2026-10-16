// Package rustcargoclippy is the atom rust:cargo-clippy: cargo clippy is clean
// under the fleet's lint set, warnings denied. It is a logic atom, not a
// command: it lints once per argument set the repo's scope script names.
package rustcargoclippy

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
	ID: "rust:cargo-clippy", Stage: checks.StagePrecommit, Lane: checks.LaneRust,
	Group:      checks.GroupLanguage,
	Desc:       "cargo clippy is clean under the fleet's lint set, warnings denied.",
	Surface:    "root:Cargo.toml",
	Toolchain:  "rust",
	NotOnForks: checks.ForkFleetStyle,
	WritesTree: true,
	Timeout:    45 * time.Minute,
	Check:      check,
}

const id = "rust:cargo-clippy"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneRust}

// env is cargo's bound and its lean debuginfo: the compile is the gate's, and a
// gate runs up to four of them at once.
var env = slices.Concat(checks.CargoBoundedEnv(), checks.CargoLineTablesEnv())

// check lints the workspace, or the crates the pull can break when the repo says
// how to find them.
//
// THE LINT SET IS NAMED ON THE COMMAND LINE. The rust template pours
// `[workspace.lints.clippy] all = "warn"` into Cargo.toml; a crate can edit that
// table, and `#![allow]` in source is stop-justifications' to catch. Flags after
// `--` reach rustc last and win over the table, so the fleet's set is the one
// enforced whatever the manifest says (Rob, 2026-09-11).
//
// --all-targets is the other half: without it clippy grades the library and
// leaves tests, benches and examples ungraded, which is a partial scan reporting
// a clean one.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	// THE CRATES THE PULL CAN BREAK, when the repo says how to find them.
	sets, scoped := atoms.RustScope(ctx, in, "clippy")
	sets = slices.Clone(sets)
	if !scoped {
		sets = [][]string{{"--workspace"}}
	} else if len(sets) == 0 {
		return checks.VerdictOf(row, 0, id+": ABSENT - this pull changed nothing a crate compiles, so there is no crate to lint")
	}
	if v, ok := atoms.RustStamp(ctx, in, row); !ok {
		return v
	}
	// EVERY SET THE SCRIPT NAMED: a second line must not go unlinted.
	var last checks.Verdict
	for _, pkgs := range sets {
		argv := slices.Concat([]string{"cargo", "clippy"}, pkgs, []string{"--all-targets", "--", "-W", "clippy::all", "-D", "warnings"})
		out, code := atoms.CargoExec(ctx, in, env, argv...)
		last = atoms.CargoVerdict(row, out, code)
		if last.State != int(checks.StatePass) {
			return last
		}
	}
	return last
}
