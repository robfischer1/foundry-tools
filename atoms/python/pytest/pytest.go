// Package pythonpytest is the atom python:pytest: pytest passes, and there is
// something for it to pass.
package pythonpytest

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It runs the repository's own suite, which may shell out to
// git, so it runs on a checkout git can read (the Dagger adapter and the pod
// both make one) and needs tool:git: the python image has no git, and the
// Dagger container installs it with apt.
var Def = atomkit.Def{
	ID: "python:pytest", Stage: checks.StagePrecommit, Lane: checks.LanePython,
	Group:     checks.GroupLanguage,
	Desc:      "pytest passes, and there is something for it to pass.",
	Surface:   "any:**/*.py",
	Toolchain: "python",
	Needs:     []string{"tool:git", "tool:uv"},
	Timeout:   30 * time.Minute,
	Check:     check,
}

const id = "python:pytest"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LanePython}

// check runs pytest, and there has to be something for it to run.
//
// NO TESTS IS A FINDING. Rob, 2026-09-11: nothing is built without tests. A
// python lane with nothing to collect is red, and a go star with a stray .py
// file is a repo that should not carry a pyproject. The emptiness is decided
// off the tree, before pytest is asked: no file matches either of pytest's two
// default naming conventions. A root tests/ no longer stands in for having
// tests: a .py-declared lane reaches Go stars whose tests/ holds no python.
// checks.PytestState carries the rest of the mapping, including the fold of
// every non-zero code that is not 5 to one finding.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	entries, stop := atoms.PythonRootEntries(row, in)
	if stop != nil {
		return *stop
	}
	pys, stop := atoms.PythonTree(row, in)
	if stop != nil {
		return *stop
	}
	if len(checks.PythonTestFiles(pys)) == 0 {
		return checks.VerdictOf(row, int(checks.StateFindings), id+": FINDINGS - no test_*.py or *_test.py anywhere; nothing is built without tests")
	}
	if bad := atoms.Probe(ctx, in, row, "uv", "--version"); bad != nil {
		return *bad
	}
	out, code, bad := atoms.RunTool(ctx, in, row, atoms.UvCmd(in, entries, "pytest", "-q"))
	if bad != nil {
		return *bad
	}
	state, reason := checks.PytestState(code)
	if reason != "" {
		return checks.VerdictOf(row, state, id+": FINDINGS - "+reason)
	}
	return checks.VerdictOf(row, state, out)
}
