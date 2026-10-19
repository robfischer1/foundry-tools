// Package devgoregen is the atom dev:go-regen: it regenerates the gravity cores
// of a stellar-core-go tree (guest, bindings and embedded modules, with
// provenance.json's hashes) on the cluster. The steps are internal/goregen's.
package devgoregen

import (
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/goregen"
)

// Def is the atom. IT IS ONE ATOM ON THE rust TOOLCHAIN, not a rust phase and a
// go phase: the guest is built by cargo, and the only Go the generator needs is
// gofmt, which arrives as a pinned url tool (tool:gofmt) and is handed to
// gravity through $GOFMT. The rust container's compiler is 1.97.0; a core whose
// provenance.json pins another is built by the atom with that one, laid from
// pinned dist tarballs (checks.RustDists), so each guest is built by exactly the
// rustc it records.
//
// It WRITES THE TREE: the regenerated files and provenance.json are the product,
// delivered as the diff of the atom's copy, and it takes the dev request's
// arguments (goregen.ParseArgs): bare cores to regenerate, and
// `--new <core> --rev <tag-or-commit> --world <aiws:pkg/world@ver>` to
// bootstrap one. The tools: wasm-tools and wac compose a guest, gravity binds it
// (one tool per revision a tree pins, as go:test has them) and gofmt formats it.
var Def = atomkit.Def{
	ID: "dev:go-regen", Stage: "dev", Lane: checks.LaneGo,
	Group:      checks.GroupLanguage,
	Desc:       "Regenerates the gravity cores named (or bootstraps new ones): rebuilds each guest at its recorded stellar-core-rust commit under its recorded rustc, runs its recorded gravity over it, and rewrites provenance.json's hashes.",
	Surface:    "root:go.mod",
	Toolchain:  "rust",
	Needs:      append([]string{"tool:wasm-tools", "tool:wac", "tool:gofmt"}, checks.GravityNeeds()...),
	WritesTree: true,
	Timeout:    45 * time.Minute,
	Check:      goregen.Check,
}
