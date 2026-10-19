// Package pythonforgetestkitschemabudget is the atom python:forge-testkit-schema-budget: MCP verb descriptions stay inside the schema budget (forge-testkit).
// It is one of the three forge-testkit lint hooks, which differ by mode and by the files the mode reads (atoms.ForgeTestkitLint).
package pythonforgetestkitschemabudget

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "python:forge-testkit-schema-budget", Stage: checks.StagePrecommit, Lane: checks.LanePython,
	Group:      checks.GroupLanguage,
	Desc:       "MCP verb descriptions stay inside the schema budget (forge-testkit).",
	Surface:    "any:**/*.py",
	Toolchain:  "python",
	Needs:      []string{"tool:uv"},
	NotOnForks: checks.ForkFleetStyle,
	WritesTree: true,
	Check:      check,
}

const (
	id      = "python:forge-testkit-schema-budget"
	mode    = "schema-budget"
	pattern = "src/**/*.py"
)

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LanePython}

// check lints the files pattern matches, in mode.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return atoms.ForgeTestkitLint(ctx, in, row, mode, pattern)
}
