// Package lawlint is the atom law:lint, one of the law lanes (Nomos F5b):
// blocks, kits, span maps and the dies, consumers and matrix registries are sound.
// The judgement is internal/govlaw's; this hands it the tree. The lane is ABSENT
// on a tree that is not foundry-stocks, decided there from the path list.
package lawlint

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/govlaw"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "law:lint", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "foundry-stocks' blocks are non-empty with slug stems, every kit names only blocks that exist (an unknown include warns, a cycle fails), every render's span map parses and covers its context document, and dies.toml, consumers.toml and matrix.toml parse and agree with kits.toml (a die's own-tree path exists; a source on the door's host must hold its ref and path; a source elsewhere is counted, not fetched).",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "law:lint"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check hands the tree to the lane.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return atoms.RunLaw(row, in, func(t govlaw.Tree) govlaw.Result {
		t.Resolve = atoms.LawResolver(ctx, in.DoorOf())
		return govlaw.Lint(t)
	})
}
