package dieskit

import (
	"context"
	"fmt"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// THE DIES ATOMS THAT RUN A PYTHON PROGRAM. A checker is the tree's own
// (tools/check_contracts.py) or the module's (the embedded schema validator),
// so it is run, not reimplemented: what is Go is everything around it, which is
// what decides ABSENT, what the checker may be handed and what its exit means.
//
// THE VENV FIRST, uv ONLY WITHOUT IT. The atoms binary votes for these atoms in
// the tools container, whose prebuilt venv holds the packages: there the program
// is the venv's python3 and nothing is resolved when it runs. The executor's
// python toolchain image carries the interpreter and uv and nothing else, so
// there a package a checker imports is resolved by `uv run --with`, as the chain
// did. What
// each program needs is the caller's: TOML for the contracts checker, jsonschema
// for the schema ones, nothing for the rest.

const (
	// WithTOML is what a checker that reads TOML is run with (the chain's tomlpy).
	WithTOML = "tomli>=2.0"
	// WithJSONSchema is what a checker that validates a schema is run with (the
	// chain's schemapy).
	WithJSONSchema = "jsonschema>=4.20"
)

// pythonEnv keeps python from writing __pycache__ beside the scripts it imports:
// a checker that imports its sibling modules (tools/check_wit_regenerated.py
// imports wit_from_schema) would otherwise leave tools/__pycache__ in the tree
// it grades.
var pythonEnv = []string{"PYTHONDONTWRITEBYTECODE=1"}

// OwnPython is the flag pair that makes uv run a checker on the interpreter the
// pod provides (python3 on PATH), whatever the graded tree pins. A checker is
// foundry-dies' program, not the tree's project: without it `uv run` finds the
// tree's .python-version, asks for an interpreter the runner may not have and,
// since an executor never fetches one, the atom CANNOT RUN. An explicit --python
// outranks the version file, and a bare name is looked up on PATH only.
var OwnPython = []string{"--python", "python3"}

// VenvPresent says whether the tools container's prebuilt venv is there. It is a
// variable so a test says which container it is in.
var VenvPresent = atoms.HasVenv

// UV is the python program, from dir, answering both streams as the chain's
// verdict() does. In the atoms binary's container the prebuilt venv holds the
// packages and the program is its python3 with the arguments as they are. On a
// toolchain image there is no venv, so it is python3 under `uv run`, resolved
// with the one requirement the program needs; "" asks for none, so a gate that
// only reads files cannot be broken by a registry.
func UV(dir, with string, args ...string) atoms.Cmd {
	if VenvPresent() {
		return atoms.Cmd{Dir: dir, Name: "python3", Args: args, Env: pythonEnv, Both: true}
	}
	argv := append([]string{"run", "--no-project", "--quiet"}, OwnPython...)
	if with != "" {
		argv = append(argv, "--with", with)
	}
	argv = append(argv, "python3")
	return atoms.Cmd{Dir: dir, Name: "uv", Args: append(argv, args...), Env: pythonEnv, Both: true}
}

// Probe is the provisioning probe, on its own exec: the venv's `python3
// --version` where the venv is, else the chain's `uv --version`. A container
// without either is a could-not-run, never a finding.
func Probe(ctx context.Context, a checks.AtomDef, in atomkit.In) *checks.Verdict {
	if !VenvPresent() {
		return atoms.Probe(ctx, in, a, "uv", "--version")
	}
	out, code := in.Runner()(ctx, atoms.Cmd{Dir: in.Root, Name: "python3", Args: []string{"--version"}, Env: pythonEnv, Both: true})
	if code == 0 {
		return nil
	}
	v := checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("the atom never ran: python3 --version exited %d: %s", code, out))
	return &v
}

// Imports is the probe that a package is importable by the python the checker
// will run under: in the venv it is in the lock; under uv a resolver that could
// not reach an index is a 2 rather than a finding nobody made. module is the
// import, with the requirement for the uv path.
func Imports(ctx context.Context, a checks.AtomDef, in atomkit.In, with, module string) *checks.Verdict {
	c := UV(in.Root, with, "-c", "import "+module)
	if VenvPresent() {
		out, code := in.Runner()(ctx, c)
		if code == 0 {
			return nil
		}
		v := checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("the atom never ran: python3 -c import %s exited %d: %s", module, code, out))
		return &v
	}
	out, code, bad := atoms.RunTool(ctx, in, a, c)
	if bad != nil {
		return bad
	}
	if code != 0 {
		v := atoms.NeverRan(a, append([]string{c.Name}, c.Args...), code, out)
		return &v
	}
	return nil
}

// Settle runs a python program and settles on its exit, stdout then stderr,
// unmapped: the checkers speak the fleet's ladder (0 holds, 1 a defect, 2 the
// check could not run), so the exit IS the state. A python that would not start
// never ran; on the uv path so does an exit the engine would not have read.
func Settle(ctx context.Context, a checks.AtomDef, in atomkit.In, with string, args ...string) checks.Verdict {
	if VenvPresent() {
		out, code := in.Runner()(ctx, UV(in.Root, with, args...))
		if code < 0 {
			return atoms.NeverRan(a, nil, code, out)
		}
		return checks.VerdictOf(a, code, out)
	}
	return atoms.SettleTool(ctx, in, a, UV(in.Root, with, args...))
}

// Verdict is the probe and then the program's verdict.
func Verdict(ctx context.Context, a checks.AtomDef, in atomkit.In, with string, args ...string) checks.Verdict {
	if stop := Probe(ctx, a, in); stop != nil {
		return *stop
	}
	return Settle(ctx, a, in, with, args...)
}

// Checker is the shape of the dies atoms whose checker is the tree's own: the
// shape, the checker's presence, uv, and the checker's exit as the verdict. with
// is the requirement it is resolved with, "" for a checker that needs no
// package. Its ladder is the fleet's (0 clean, 1 a finding, 2 the check could
// not run, and could-not-run outranks a finding), which is why nothing
// translates it.
func Checker(ctx context.Context, a checks.AtomDef, in atomkit.In, with, checker string) checks.Verdict {
	t := in.Tree()
	if stop := Shape(t, a); stop != nil {
		return *stop
	}
	if stop := atoms.RequirePaths(t, a, [][2]string{{checker, checker + " is absent, so there is no checker to run."}}); stop != nil {
		return *stop
	}
	return Verdict(ctx, a, in, with, checker)
}
