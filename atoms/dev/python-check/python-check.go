// Package devpythoncheck is the atom dev:python-check: dev:python-typecheck
// under the name `check`.
package devpythoncheck

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/devpython"
)

// Def is the atom. `check` IS A LANGUAGE'S COMPILE-BEARING VERB (dev:go-check is
// go vet, dev:rust-check is cargo check). Python has no compile step, so it is
// the type check: dev:python-typecheck's devpython.Typecheck under the name the
// refusal shim routes, with its own cache directories so two asks never share
// a venv. tool:bun runs the npm pyright (devpython.pyrightArgv).
var Def = atomkit.Def{
	ID: id, Stage: "dev", Lane: checks.LanePython,
	Group:         checks.GroupLanguage,
	Desc:          "mypy is clean over src and tests, and pyright where the tree configures it, run by hand from the dev lane under its other name, `check` (dev:python-typecheck's twin).",
	Surface:       "root:pyproject.toml",
	Toolchain:     "python",
	Needs:         []string{"tool:uv", "tool:bun"},
	Timeout:       30 * time.Minute,
	NeedsManifest: true,
	Check:         check,
}

const id = "dev:python-check"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: "dev", Lane: checks.LanePython}

func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return devpython.Typecheck(ctx, in, row, "dev-python-check")
}
