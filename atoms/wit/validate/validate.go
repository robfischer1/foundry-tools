// Package witvalidate is the atom wit:validate: the repository's own `just
// validate` passes. One file holds the atom and both runners execute it (lld.md
// FN-085).
package witvalidate

import (
	"context"
	"fmt"
	"slices"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. wasm-tools and just are the pinned binaries the tools
// container carries.
var Def = atomkit.Def{
	ID: "wit:validate", Stage: checks.StagePrepush, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The repository's own `just validate` passes — wasm-tools resolves every WIT package the tree carries.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"tool:wasm-tools", "tool:just"},
	Check:     check,
}

const id = "wit:validate"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneAny}

// check: the repository's own `just validate` passes.
//
// ABSENT UNLESS THE TREE TRACKS WIT AND A VALIDATE RECIPE, decided before
// wasm-tools or just is touched. wit/*.wit says there is an interface to
// resolve; a root justfile defining `validate` says the repository has its own
// answer for what resolving means. The recipe's own exit is the verdict: 0
// pass, 1 findings (just passes the failing command's code through), anything
// else could-not-run.
//
// THE RECIPE RUNS IN A PRIVATE TREE, not the shared one. It is the repository's
// own program, and a recipe that writes (a generated file, a lock, a cache) is
// allowed to: in the chain that was the engine's copy of the tree, and the binary
// runs its atoms at once over one tree, so a write there is a file the atoms
// reading beside it may or may not see.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	if !checks.HasWitFiles(in.Files) {
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - this repository tracks no wit/*.wit, so it declares no interface for wasm-tools to resolve")
	}
	if !slices.Contains(in.Files, "justfile") {
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - the repository tracks WIT but no root justfile, so it declares no validate recipe to run")
	}
	text, err := in.Tree().Read("justfile")
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the justfile would not read: %v", id, err))
	}
	if !checks.HasJustValidate(text) {
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - the justfile defines no validate recipe, so the repository declares no check of its WIT for the gate to run")
	}
	for _, tool := range []string{"wasm-tools", "just"} {
		if out, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: tool, Args: []string{"--version"}}); code != 0 {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %s --version exited %d: %s. WIT that was never resolved is not WIT that passed.", id, tool, code, out))
		}
	}
	dir, cleanup, err := atomlib.PrivateTree(ctx, in, "wit")
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the private tree the recipe runs in could not be made (%v).", id, err))
	}
	defer cleanup()
	out, code := in.Run(ctx, atoms.Cmd{Dir: dir, Name: "just", Args: []string{"validate"}, Both: true})
	if code < 0 {
		return atomlib.NeverRan(row, out)
	}
	return checks.VerdictOf(row, code, out)
}
