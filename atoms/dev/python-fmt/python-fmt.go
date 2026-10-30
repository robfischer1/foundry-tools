// Package devpythonfmt is the atom dev:python-fmt: ruff format rewrites the
// tree's python in place under the fleet's ruleset.
package devpythonfmt

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/devpython"
)

// Def is the atom. THE GATE TWIN IS python:ruff-format, which runs `--check`;
// this is the rewrite the twin refuses to be, on the atom's own copy of the
// tree, under the same fleet ruleset (devpython.Ruff passes --config), so what
// it writes is what the twin then accepts. The twin's src/ + tests/ scope
// (checks.RuffFormatTargets) is not this verb's: with no path ruff formats the
// working directory, and a caller who wants the twin's scope passes `src tests`.
// It was a command manifest. The Def's fields are literals: daedalus reads
// them statically.
var Def = atomkit.Def{
	ID: id, Stage: "dev", Lane: checks.LanePython,
	Group:      checks.GroupLanguage,
	Desc:       "ruff format rewrites the tree's python in place under the fleet's ruleset.",
	Surface:    "any:**/*.py",
	Toolchain:  "python",
	Needs:      []string{"tool:ruff"},
	WritesTree: true,
	Check:      check,
}

const id = "dev:python-fmt"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: "dev", Lane: checks.LanePython}

func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return devpython.Ruff(ctx, in, row, "format", "")
}
