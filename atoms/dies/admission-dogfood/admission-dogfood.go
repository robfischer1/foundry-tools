// Package diesadmissiondogfood is the atom dies:admission-dogfood: the admission domain admits this repo's own
// star shape. The domain that judges every star's slag is asked about the one
// star whose shape lives in the same repo as the rule; a deny means the policy
// has drifted from the fleet it governs.
package diesadmissiondogfood

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
	ID: "dies:admission-dogfood", Stage: checks.StagePrepush, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The admission domain admits this repo's own star shape.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"tool:opa"},
	Check:     check,
}

const id = "dies:admission-dogfood"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneAny}

// check evaluates the domain's deny set over the own-star fixture.
//
// A MISSING FIXTURE IS A 2: the atom's whole content is "the domain admitted
// THIS input", and with no input there is no claim to make. Go reads the result
// (checks.OpaDenySet), and a shape it cannot read is a 2 rather than a deny
// count nobody computed.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	if stop := dieskit.Shape(t, row); stop != nil {
		return *stop
	}
	// policy/ is proved present by the shape; what has to be in it is the domain.
	if !atoms.Present(t, "policy/admission") {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - policy/admission is absent, so there is no admission domain to ask.")
	}
	if stop := atoms.RequirePaths(t, row, [][2]string{
		{"tests/fixtures/ouranos-self.json", "tests/fixtures/ouranos-self.json is absent, so there is no own-star shape to submit."},
	}); stop != nil {
		return *stop
	}
	if stop := dieskit.OpaProvisioned(ctx, row, in); stop != nil {
		return *stop
	}
	out, code := in.Runner()(ctx, atoms.Cmd{Dir: in.Root, Name: "opa", Args: []string{
		"eval", "-d", "policy/admission", "-i", "tests/fixtures/ouranos-self.json",
		"data.admission.deny", "--format", "json",
	}})
	if code < 0 {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the atom never ran: "+out)
	}
	if code != 0 {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - opa eval did not complete.\n"+out)
	}
	deny, err := checks.OpaDenySet(out)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - opa eval answered a shape this atom cannot read (%v), so there is no deny set to count.", id, err))
	}
	if len(deny) > 0 {
		return checks.VerdictOf(row, int(checks.StateFindings), fmt.Sprintf("%s: FINDINGS - deny count = %d\n%s",
			id, len(deny), strings.Join(atoms.Indent(strings.Join(deny, "\n"), "  "), "\n")))
	}
	return checks.VerdictOf(row, int(checks.StatePass), id+": deny count = 0; the admission domain admits our own star shape")
}
