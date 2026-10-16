package dieskit

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// THE DIES ATOMS THAT RUN A PYTHON PROGRAM. A checker is the tree's own
// (tools/check_contracts.py) or the module's (the embedded schema validator),
// so it is run, not reimplemented: what is Go is everything around it, which is
// what decides ABSENT, what the checker may be handed and what its exit means.
//
// IT RUNS UNDER uv, AS THE CHAIN DID. The python toolchain image carries the
// interpreter and uv and nothing else, so a package a checker imports is
// resolved by `uv run --with` and the program is the image's own python3. What
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

// UV is python3 under `uv run`, from dir, answering both streams as the chain's
// verdict() does. with is the one requirement the program is resolved with; ""
// asks for none, so a gate that only reads files cannot be broken by a registry.
func UV(dir, with string, args ...string) atoms.Cmd {
	argv := []string{"run", "--no-project", "--quiet"}
	if with != "" {
		argv = append(argv, "--with", with)
	}
	argv = append(argv, "python3")
	return atoms.Cmd{Dir: dir, Name: "uv", Args: append(argv, args...), Env: pythonEnv, Both: true}
}

// Probe is the chain's `uv --version`: the provisioning probe, on its own exec.
// An image without uv is a could-not-run, never a finding.
func Probe(ctx context.Context, a checks.AtomDef, in atomkit.In) *checks.Verdict {
	return atoms.Probe(ctx, in, a, "uv", "--version")
}

// Imports is the probe that a package is resolvable under the same resolver the
// checker will run with: a resolver that could not reach an index is a 2 rather
// than a finding nobody made. module is the import, with the requirement.
func Imports(ctx context.Context, a checks.AtomDef, in atomkit.In, with, module string) *checks.Verdict {
	c := UV(in.Root, with, "-c", "import "+module)
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
// check could not run), so the exit IS the state. A uv that would not start, or
// whose exit the engine would not have read, never ran.
func Settle(ctx context.Context, a checks.AtomDef, in atomkit.In, with string, args ...string) checks.Verdict {
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
