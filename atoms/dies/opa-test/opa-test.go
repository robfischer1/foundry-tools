// Package diesopatest is the atom dies:opa-test: the rego unit and invariant suite passes.
package diesopatest

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "dies:opa-test", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The rego unit and invariant suite passes.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"tool:opa"},
	Check:     check,
}

const id = "dies:opa-test"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check runs `opa test` over the policy.
//
// A ZERO-TEST RUN IS REFUSED: `opa test` over a policy tree containing no test
// at all exits 0, which renders as a clean suite and is not one. opa's own codes
// do not line up with ours (a failing assertion is 2 and a parse error is 1), so
// checks.OpaTestState folds both to findings and only a code opa does not use
// becomes a 2.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	if stop := dieskit.Shape(t, row); stop != nil {
		return *stop
	}
	if stop := dieskit.OpaProvisioned(ctx, row, in); stop != nil {
		return *stop
	}
	args := checks.OpaTestArgs(atoms.Present(t, checks.FleetDerive))
	out, code := in.Runner()(ctx, atoms.Cmd{Dir: in.Root, Name: "opa", Args: args})
	if code < 0 {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the atom never ran: "+out)
	}
	state, reason := checks.OpaTestState(code, out)
	switch state {
	case int(checks.StatePass):
		return checks.VerdictOf(row, state, id+": "+reason)
	case int(checks.StateFindings):
		return checks.VerdictOf(row, state, id+": FINDINGS - "+reason+"\n"+out)
	default:
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - "+reason+"\n"+out)
	}
}
