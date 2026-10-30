// Package devpythonlint is the atom dev:python-lint: ruff check is clean over
// the tree's python under the fleet's ruleset, run by hand from the dev lane.
package devpythonlint

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/devpython"
)

// Def is the atom. THE GATE TWIN IS python:ruff-check: the same ruff from the
// same tool:ruff pin, and the same fleet ruleset (devpython.Ruff lays it and
// passes --config), so the dev answer is the gate's. It was a command manifest,
// which runs one fixed argv and has no step that lays a file, so it read the
// tree's own ruff config and could drift from the gate. The Def's fields are
// literals: daedalus reads them statically.
var Def = atomkit.Def{
	ID: id, Stage: "dev", Lane: checks.LanePython,
	Group:     checks.GroupLanguage,
	Desc:      "ruff check is clean over the tree's python under the fleet's ruleset, run by hand from the dev lane.",
	Surface:   "any:**/*.py",
	Toolchain: "python",
	Needs:     []string{"tool:ruff"},
	Check:     check,
}

const id = "dev:python-lint"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: "dev", Lane: checks.LanePython}

// ruffCache keeps ruff's cache off the read-only /src.
const ruffCache = "/tmp/dev-python-lint/ruff-cache"

func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return devpython.Ruff(ctx, in, row, "check", ruffCache)
}
