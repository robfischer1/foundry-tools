// Package devpython is the logic the python dev verbs share: the type check
// that dev:python-typecheck runs, and dev:python-check runs under its other
// name.
package devpython

import (
	"context"
	"fmt"
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
// npm package at the fleet's pin (checks.PyrightVersion) under bun, inside the
// project's environment (pyrightArgv); it reads its own configuration, so it
// takes none of mypy's arguments. A pyright that cannot run is a finding of its
// own beside mypy's answer. No gate atom runs pyright.
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
	pout, pcode, bad := run(ctx, in, row, env, pyrightArgv(entries))
	switch {
	case bad != nil:
		pcode, pout = 2, bad.Reason
	case pcode > 1:
		pout = fmt.Sprintf("exit %d\n%s", pcode, pout)
	}
	if pcode > 1 {
		// A PYRIGHT THAT CANNOT RUN IS ITS OWN FINDING, never the verb's
		// could-not-run: mypy's answer stands beside it (DEVSCOPE, run
		// forge/dev-vault-mcp-12816db-g5l6g, a fetched node that wanted libatomic).
		// mypy clean or finding: the verb has findings. mypy itself unable to
		// run: the verb could not run, as mypy alone says.
		if checks.StateFor(code) == checks.StatePass {
			code = int(checks.StateFindings)
		}
		return checks.VerdictOf(row, code, "mypy:\n"+out+"\npyright could not run:\n"+pout)
	}
	if checks.StateFor(pcode) > checks.StateFor(code) {
		code = pcode
	}
	return checks.VerdictOf(row, code, "mypy:\n"+out+"\npyright:\n"+pout)
}

// pyrightArgv is the npm pyright at the fleet's pin, run by bun (tool:bun)
// inside the project's environment: `uv run` puts the venv's python first on
// PATH, which pyright resolves imports through. Bun runs it (`--bun`), so no
// node is fetched: the pypi wrapper's runtime node needed a libatomic.so.1 the
// python image does not carry.
// A tree with a pyrightconfig.json and no project has no environment to enter.
func pyrightArgv(entries []string) []string {
	bunx := []string{"bun", "x", "--bun", "pyright@" + checks.PyrightVersion}
	if !slices.Contains(entries, "pyproject.toml") {
		return bunx
	}
	return append([]string{"uv", "run", "--python", "python3", "--all-extras"}, bunx...)
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
