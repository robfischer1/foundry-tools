// Package pythonforgetestkitassertionfree is the atom python:forge-testkit-assertion-free: No assertion-free test bodies (forge-testkit).
// It is one of the three forge-testkit lint hooks, which differ by mode and by the files the mode reads (atoms.ForgeTestkitLint).
package pythonforgetestkitassertionfree

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "python:forge-testkit-assertion-free", Stage: checks.StagePrecommit, Lane: checks.LanePython,
	Group:      checks.GroupLanguage,
	Desc:       "No assertion-free test bodies (forge-testkit).",
	Surface:    "any:**/*.py",
	Toolchain:  "python",
	Needs:      []string{"tool:uv"},
	NotOnForks: checks.ForkFleetStyle,
	WritesTree: true,
	Check:      check,
}

const (
	id      = "python:forge-testkit-assertion-free"
	mode    = "assertion-free"
	pattern = "tests/**/*.py"
)

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LanePython}

// check lints the files pattern matches, in mode.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return atoms.ForgeTestkitLint(ctx, in, row, mode, pattern)
}
