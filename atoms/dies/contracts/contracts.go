// Package diescontracts is the atom dies:contracts: every copy of every shared closed set
// agrees, and the checker is proved to detect first. The checker is the tree's
// own (tools/check_contracts.py), run under uv.
package diescontracts

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "dies:contracts", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every copy of every shared closed set agrees — and the checker is proved to detect first.",
	Surface:   "any:**",
	Toolchain: "python",
	Needs:     []string{"tool:uv"},
	Check:     check,
}

const id = "dies:contracts"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// checker and fixtures are the files this atom needs in the tree beside its
// manifest (checks.DoorManifest).
const (
	checker  = "tools/check_contracts.py"
	fixtures = "tests/contracts/fixtures.toml"
)

// check proves the checker detects, probes the door, then lets the live check
// be the verdict.
//
// THE DOOR IS PROBED BEFORE THE LIVE CHECK, the target read out of the manifest.
// check_contracts.py raises ContractError on an unreachable copy and exits 1 for
// it, which is right for a gate whose runner sat on the same network as the door
// and wrong for an atom: a copy that could not be FETCHED is not a copy that
// DISAGREES. So an unreachable door is a 2 here and every other answer stays the
// checker's own.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	if stop := dieskit.Shape(t, row); stop != nil {
		return *stop
	}
	if stop := atoms.RequirePaths(t, row, [][2]string{
		{checker, checker + " is absent, so there is no checker to run."},
		{fixtures, fixtures + " is absent, and a gate that cannot prove it detects is a gate that is not there."},
		{checks.DoorManifest, checks.DoorManifest + " is absent, so there is no live manifest to check."},
	}); stop != nil {
		return *stop
	}
	if stop := dieskit.Probe(ctx, row, in); stop != nil {
		return *stop
	}
	lines := []string{}
	bad := false
	for _, f := range checks.ContractFixtures {
		out, code := in.Runner()(ctx, dieskit.UV(in.Root, dieskit.WithTOML, checker, "--manifest", fixtures, "--contract", f.Name))
		if code < 0 {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the %s fixture never ran: %s", id, f.Name, out))
		}
		line, isBad, toolFailed := checks.ContractFixtureVerdict(f.Name, f.ExpectFail, code)
		if toolFailed {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the %s fixture's checker never reached a verdict (exit %d): %s", id, f.Name, code, out))
		}
		lines = append(lines, line)
		bad = bad || isBad
	}
	if bad {
		return checks.VerdictOf(row, int(checks.StateFindings), id+": FINDINGS - the fixtures no longer prove the gate detects:\n"+strings.Join(lines, "\n"))
	}
	manifest, err := t.Read(checks.DoorManifest)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the door reachability probe never ran: "+err.Error())
	}
	if code, out := checks.DoorProbe(ctx, manifest, in.DoorOf()); code != 0 {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the door's archive read is unreachable, so the remote copies cannot be read. A copy that could not be fetched is not a copy that agrees.\n"+out)
	}
	return dieskit.Settle(ctx, row, in, dieskit.WithTOML, checker)
}
