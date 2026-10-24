// Package devpythontest is the atom dev:python-test: pytest passes, run by hand
// from the dev lane, in a python project or in a tree that is not one.
package devpythontest

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. THE GATE TWIN IS python:pytest: the same toolchain, needs and
// surface, and the same `uv run` (checks.UvRun): `--all-extras pytest` in a
// project, `--no-project --with pytest==<pin> pytest` in a tree with no
// pyproject.toml (paneless' tests/, foundry-stocks' hooks/), plus the image's
// own interpreter (checks.DevUvRun). It was a command manifest, which runs one
// fixed argv and so could not take the no-project form; the gate planner plans
// python:pytest for such a tree, and the dev lane now scopes as the gate does.
//
// /src IS READ-ONLY to an atom that does not write the tree, and uv, python and
// pytest would each put something beside the sources: the project's .venv, the
// bytecode, .pytest_cache. They go to /tmp instead (env).
var Def = atomkit.Def{
	ID: id, Stage: "dev", Lane: checks.LanePython,
	Group:     checks.GroupLanguage,
	Desc:      "pytest passes, run by hand from the dev lane.",
	Surface:   "any:**/*.py",
	Toolchain: "python",
	Needs:     []string{"tool:git", "tool:uv", "tool:jq", "tool:curl", "tool:just"},
	Timeout:   30 * time.Minute,
	Check:     check,
}

const id = "dev:python-test"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: "dev", Lane: checks.LanePython}

// env keeps what the run writes off the read-only /src.
var env = []string{
	"PYTHONDONTWRITEBYTECODE=1",
	"PYTEST_ADDOPTS=-o cache_dir=/tmp/dev-python-test/pytest-cache",
	"UV_PROJECT_ENVIRONMENT=/tmp/dev-python-test/venv",
}

// quiet is -q, the fleet's (the gate twin's and every star's `just test`); a
// caller who asks for their own verbosity replaces it. It composes as the
// manifest's default did (atomkit.Default).
var quiet = atomkit.Default{Args: []string{"-q"}, Unless: []string{"-q", "--quiet", "-v", "--verbose"}}

// check runs pytest over the tree with the caller's arguments.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	entries, stop := atoms.PythonRootEntries(row, in)
	if stop != nil {
		return *stop
	}
	head := checks.DevUvRun(entries, "pytest")
	argv := atomkit.Manifest{Argv: head, Args: "append", Default: []atomkit.Default{quiet}}.Compose(in.Args)
	return atoms.SettleTool(ctx, in, row, atoms.Cmd{Dir: in.Root, Name: argv[0], Args: argv[1:], Env: env})
}
