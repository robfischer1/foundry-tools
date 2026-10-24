// Package devpythontypecheck is the atom dev:python-typecheck: mypy is clean,
// and pyright too where the tree configures it, run by hand from the dev lane.
package devpythontypecheck

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/devpython"
)

// Def is the atom. THE GATE TWIN IS python:mypy: the same toolchain, surface
// and tool:uv pin. The check is devpython.Typecheck: mypy, then pyright where
// the tree configures it, as the stars' `just types` runs them. It was a
// command manifest, which runs one command and so could not run the second.
var Def = atomkit.Def{
	ID: id, Stage: "dev", Lane: checks.LanePython,
	Group:         checks.GroupLanguage,
	Desc:          "mypy is clean over src and tests, and pyright where the tree configures it, run by hand from the dev lane.",
	Surface:       "root:pyproject.toml",
	Toolchain:     "python",
	Needs:         []string{"tool:uv"},
	Timeout:       30 * time.Minute,
	NeedsManifest: true,
	Check:         check,
}

const id = "dev:python-typecheck"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: "dev", Lane: checks.LanePython}

func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return devpython.Typecheck(ctx, in, row, "dev-python-typecheck")
}
