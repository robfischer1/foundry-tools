// Package opsspecs is the atom ops:specs: the console read-model specs follow
// the stacks' emits. One file holds the atom and both runners execute it
// (lld.md FN-085).
package opsspecs

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. The tree's own checker is run with the venv's python.
var Def = atomkit.Def{
	ID: "ops:specs", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The repo's own tools/console-specs (infra): the console ConfigMap's read-model specs follow the stacks' emits, or it names which drifted.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"pytools"},
	Check:     check,
}

const id = "ops:specs"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check: the console read-model specs follow nas01-stacks' emits
// (tools/console-specs --check, run through the venv's python where the chain
// ran it under uv with pyyaml). The tool grades itself: 2 is "could not read the
// source", could-not-run whatever its words were.
//
// ITS VERDICT CAN CHANGE WITH NO CHANGE TO THE TREE. With no sibling checkout and
// no CONSOLE_SPECS_SOURCE, the tool falls back to a LIVE
// `git clone --depth 1 http://ourea:8215/nas01-stacks.git` and compares the
// tree's copy to whatever that door holds now. The clone is the tool's own, left
// explicit rather than moved here: the argv is the chain's, nothing pins or
// caches a source, and the binary's exec is keyed afresh on every call
// (CA_REASK), so no earlier answer stands in for this one. An unreachable door is
// the tool's exit 2: a source nobody read agrees with nothing.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return atomlib.RunChecker(ctx, row, in, atomlib.Checker{Phase: "specs", Tool: "console-specs", Python: "python", Args: []string{"--check"}, Two: "\nspecs: could not read the source — did not look"})
}
