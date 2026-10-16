package atoms

import (
	"context"

	"dagger/foundry-tools/internal/checks"
)

// WHAT THE PYTHON LANE'S ATOMS SHARE, in the words the chains had. Each atom is
// its own file under atoms/python/<name>/; the three questions they ask of the
// tree, and the one linter three of them run, are asked once here.

// PythonRootEntries is the repository root, or the CANNOT RUN that not being
// able to read it is. A tree that cannot be listed is a fact about the
// repository, not a finding about it.
func PythonRootEntries(a checks.AtomDef, in Input) ([]string, *checks.Verdict) {
	entries, err := RootEntries(in.Root)
	if err != nil {
		v := checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - could not read the repository root: "+err.Error())
		return nil, &v
	}
	return entries, nil
}

// PythonTree is the tree's own python (checks.PythonFiles), read off the gate's
// population, or the CANNOT RUN that a population that would not enumerate is.
func PythonTree(a checks.AtomDef, in Input) ([]string, *checks.Verdict) {
	if in.FilesErr != nil {
		v := enumerationFailed(a, in.FilesErr)
		return nil, &v
	}
	return checks.PythonFiles(Matching(in.Files, "**/*.py")), nil
}

// enumerationFailed is the CANNOT RUN for a population that would not list.
func enumerationFailed(a checks.AtomDef, err error) checks.Verdict {
	return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - could not enumerate the repository: "+err.Error())
}

// ForgeTestkitLint runs one forge-testkit-lint pre-commit hook: mode names the
// linter's subcommand and pattern what the template hook's `files:` matched,
// read over the gate's own population so the fleet exclude applies.
//
// THE HOOK TAKES PATHS. pre-commit appends the files its `files:` pattern
// matched, and the CLI refuses to run without them ("the following arguments are
// required: paths"). The patterns are Dagger globs over the population:
// `tests/**/*.py`, `**/*.py` and `src/**/*.py`.
//
// TWO ABSENCES, BOTH ANNOUNCED. No forge-testkit DEPENDENCY ENTRY in
// pyproject.toml is ABSENT (checks.DeclaresForgeTestkit says why a mention is
// not an entry). No matching file is ABSENT too: a linter handed nothing would
// be a pass over nothing.
//
// THE FILE LIST IS ARGUMENTS, NOT xargs: xargs answers 123 for the command's own
// exit 1, which would read a finding as a could-not-run. The tool is the process.
func ForgeTestkitLint(ctx context.Context, in Input, a checks.AtomDef, mode, pattern string) checks.Verdict {
	pyproject, present := ReadIfPresent(in.Root, "pyproject.toml")
	if !present || !checks.DeclaresForgeTestkit(pyproject) {
		return checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - forge-testkit is not a dependency of this project")
	}
	if in.FilesErr != nil {
		return enumerationFailed(a, in.FilesErr)
	}
	files := Matching(in.Files, pattern)
	if len(files) == 0 {
		return checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - no files match "+pattern)
	}
	if bad := Probe(ctx, in, a, "uv", "--version"); bad != nil {
		return *bad
	}
	args := append([]string{"run", "--extra", "dev", "forge-testkit-lint", mode}, files...)
	return SettleTool(ctx, in, a, Cmd{Dir: in.Root, Name: "uv", Args: args})
}

// UvCmd is `uv run` of tool over the tree (checks.UvRun: against the project's
// extras when it is one, fetched with --with when it is not), as a Cmd in the
// tree's root. The tool's arguments follow.
func UvCmd(in Input, entries []string, tool string, args ...string) Cmd {
	argv := checks.UvRun(entries, tool, args...)
	return Cmd{Dir: in.Root, Name: argv[0], Args: argv[1:]}
}
