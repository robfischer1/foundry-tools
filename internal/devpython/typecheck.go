// Package devpython is the logic the python dev verbs share: the type check
// that dev:python-typecheck runs, and dev:python-check runs under its other
// name.
package devpython

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// targets is mypy's default target, which a caller's own replaces. "@package"
// is a path-shaped argument (./src/pkg): a bare directory name is not told from
// an option's value, so name it with its ./.
var targets = atomkit.Default{
	Args: []string{"src", "tests"}, Tail: true,
	Unless: []string{atomkit.PackageToken, "-m", "--module", "-p", "--package", "-c", "--command"},
}

// pyrightTable is a [tool.pyright] table, or one of its sub-tables, opened at
// the start of a line.
var pyrightTable = regexp.MustCompile(`(?m)^[ \t]*\[\[?[ \t]*tool\.pyright[ \t]*[\].]`)

// Typecheck runs mypy over the tree, then pyright where the tree configures it
// ([tool.pyright] or a sub-table in pyproject.toml, or a pyrightconfig.json),
// and answers the worse, with both outputs. It is the stars' `just types`:
// `mypy src tests` then `pyright`.
//
// mypy is `uv run --all-extras mypy` (checks.DevUvRun: python:mypy's form plus
// the image's own interpreter); it reads the tree's own [tool.mypy], not the
// gate twin's fleet mypy.ini, and takes the caller's arguments. pyright is the
// project's own (its dev dependency) and reads its own configuration, so it
// takes none of mypy's arguments. No gate atom runs pyright.
//
// cache names the atom's directories under /tmp: the venv and .mypy_cache stay
// off the read-only /src, and two verbs never share them.
func Typecheck(ctx context.Context, in atomkit.In, row checks.AtomDef, cache string) checks.Verdict {
	entries, stop := atoms.PythonRootEntries(row, in)
	if stop != nil {
		return *stop
	}
	pyright, err := pyrightConfigured(in.Root, entries)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), row.ID+": CANNOT RUN - could not read pyproject.toml: "+err.Error())
	}
	env := []string{
		"MYPY_CACHE_DIR=/tmp/" + cache + "/mypy-cache",
		"PYTHONDONTWRITEBYTECODE=1",
		"UV_PROJECT_ENVIRONMENT=/tmp/" + cache + "/venv",
	}
	mypy := atomkit.Manifest{Argv: checks.DevUvRun(entries, "mypy"), Args: "append", Default: []atomkit.Default{targets}}.Compose(in.Args)
	out, code, bad := run(ctx, in, row, env, mypy)
	if bad != nil {
		return *bad
	}
	if !pyright {
		return checks.VerdictOf(row, code, out)
	}
	pout, pcode, bad := run(ctx, in, row, env, checks.DevUvRun(entries, "pyright"))
	if bad != nil {
		return *bad
	}
	if checks.StateFor(pcode) > checks.StateFor(code) {
		code = pcode
	}
	return checks.VerdictOf(row, code, "mypy:\n"+out+"\npyright:\n"+pout)
}

// run is one checker over the tree, both streams answered.
func run(ctx context.Context, in atomkit.In, row checks.AtomDef, env, argv []string) (string, int, *checks.Verdict) {
	return atoms.RunTool(ctx, in, row, atoms.Cmd{Dir: in.Root, Name: argv[0], Args: argv[1:], Env: env, Both: true})
}

// pyrightConfigured says whether the tree configures pyright.
func pyrightConfigured(root string, entries []string) (bool, error) {
	if slices.Contains(entries, "pyrightconfig.json") {
		return true, nil
	}
	if !slices.Contains(entries, "pyproject.toml") {
		return false, nil
	}
	b, err := os.ReadFile(filepath.Join(root, "pyproject.toml"))
	if err != nil {
		return false, err
	}
	return pyrightTable.Match(b), nil
}
