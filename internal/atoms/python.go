package atoms

import (
	"context"
	"fmt"

	"dagger/foundry-tools/internal/checks"
)

// THE ATOMS THAT RUN A PYTHON PROGRAM. A repository's own checker
// (tools/check_contracts.py, infra's tools/dup-check, flux's tools/wit-topics)
// is the tree's, so it is run, not reimplemented: what is Go here is everything
// around it, which is what decides ABSENT, what the checker may be handed, and
// what its exit means.
//
// THE CHAINS PROVISIONED ITS PACKAGES AT RUN TIME, per atom, per gate: `uv run
// --with jsonschema`, `--with tomli`, `--with pyyaml`, `uvx --from copier`. The
// binary runs in a container whose venv already holds every one of them, pinned
// (the module's atoms_tools_python.go builds it from pytools/requirements.txt),
// so an atom execs `python3` and nothing resolves a package when it runs. What
// the chain's `uv run --with X` was for is the venv; the checker's own argv is
// unchanged.

const (
	// PythonVenvDir is the venv the tools container installs the packages in.
	PythonVenvDir = "/opt/atoms-py"
	// PythonBinDir is its bin, first on the container's PATH, so `python3` and the
	// entry points (copier, ansible-playbook, ansible-lint) are the venv's.
	PythonBinDir = "/opt/atoms-py/bin"
	// AnsibleCollectionsDir is where the container installed the ansible
	// collections, outside any tree, so a tree's own collections_path cannot place
	// them where lint then reads them as source.
	AnsibleCollectionsDir = "/opt/ansible-collections"
)

// pythonEnv keeps python from writing __pycache__ beside the scripts it imports:
// a checker that imports its sibling modules (tools/check_wit_regenerated.py
// imports wit_from_schema) would otherwise leave tools/__pycache__ in the tree
// it grades. In the chain that was the engine's copy of the tree; the binary
// runs on the tree.
var pythonEnv = []string{"PYTHONDONTWRITEBYTECODE=1"}

// pyCmd is a python3 run from dir, answering both streams as the chain's
// verdict() does.
func pyCmd(dir string, args ...string) Cmd {
	return Cmd{Dir: dir, Name: "python3", Args: args, Env: pythonEnv, Both: true}
}

// pyProbe is the chain's `uv --version`: the provisioning probe, on its own
// exec. A container without the venv is a could-not-run, never a finding.
func pyProbe(ctx context.Context, a checks.AtomDef, in Input) *checks.Verdict {
	out, code := in.run(ctx, pyCmd(in.Root, "--version"))
	if code == 0 {
		return nil
	}
	v := checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("the atom never ran: python3 --version exited %d: %s", code, out))
	return &v
}

// pyImports is the probe that the packages a checker imports are in the venv:
// dies:schema's `-c "import jsonschema"`, kept as the chain had it.
func pyImports(ctx context.Context, a checks.AtomDef, in Input, module string) *checks.Verdict {
	out, code := in.run(ctx, pyCmd(in.Root, "-c", "import "+module))
	if code == 0 {
		return nil
	}
	v := checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("the atom never ran: python3 -c import %s exited %d: %s", module, code, out))
	return &v
}

// pySettle runs a python program and settles on its exit, stdout then stderr,
// unmapped: the tree's checkers speak the fleet's ladder (0 holds, 1 a defect, 2
// the check could not run), so the exit IS the state. A python3 that would not
// start never ran.
func pySettle(ctx context.Context, a checks.AtomDef, in Input, args ...string) checks.Verdict {
	out, code := in.run(ctx, pyCmd(in.Root, args...))
	if code < 0 {
		return neverRan(a, out)
	}
	return checks.VerdictOf(a, code, out)
}

// pyVerdict is the probe and then the checker's verdict.
func pyVerdict(ctx context.Context, a checks.AtomDef, in Input, args ...string) checks.Verdict {
	if stop := pyProbe(ctx, a, in); stop != nil {
		return *stop
	}
	return pySettle(ctx, a, in, args...)
}

// witTopicsChecker is flux's own gate for redpanda/wit (stellar-core F2).
const witTopicsChecker = "tools/wit-topics"

// fleetWitTopics: flux's redpanda/wit is the WIT of redpanda/schemas.
//
// THE CHECKER IS THE TREE'S, NOT EMBEDDED. flux holds neither the generator nor
// a way to run it, so tools/wit-topics compares the digests the generator
// recorded in aiws-topics.report.json: a schema edited and not regenerated, a
// topic added or removed and a hand edit of aiws-topics.wit are one failure each.
// Its exit code is the verdict, unmapped: 0 current, 1 stale, 2 could not run.
//
// ABSENT ONLY ON A TREE THAT IS NOT FLUX's, decided before python is touched:
// prime/ is the fleet's flux tree and nothing else's. On that tree a missing
// checker is a CANNOT RUN naming it, never an absence: a gate that goes quiet
// when its script moves is how this one graded nothing.
func fleetWitTopics(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	t := in.tree()
	roots, err := t.entries(".")
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(roots, checks.OureaConfigDir) {
		return checks.VerdictOf(a, int(checks.StatePass), a.ID+
			": ABSENT - this tree carries no "+checks.OureaConfigDir+"/, so it is not the fleet's flux tree and holds no topic schemas to render")
	}
	if stop := requirePaths(t, a, [][2]string{
		{witTopicsChecker, witTopicsChecker + " is absent, so there is no checker to run and the topics' WIT is ungraded."},
	}); stop != nil {
		return *stop
	}
	return pyVerdict(ctx, a, in, witTopicsChecker)
}
