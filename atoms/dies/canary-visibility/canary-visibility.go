// Package diescanaryvisibility is the atom dies:canary-visibility: the BUILT bundle still hides a curated
// verb from a session principal, and shows it to a star principal.
package diescanaryvisibility

import (
	"context"
	"fmt"
	"os"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "dies:canary-visibility", Stage: checks.StagePrepush, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The BUILT bundle still hides a curated verb from a session principal.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"tool:opa"},
	Check:     check,
}

const id = "dies:canary-visibility"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneAny}

// canaryProbe asks the built bundle which of search and the canary verb a
// principal of the given type may CALL (data.authz.callable: the audience tier
// alone, in v1 and authz_v2 alike; since the F5 flip `visible` also filters by
// grant effect, so the grantless subject "s" sees nothing there). The input is a
// file opa is pointed at, exactly as the chain's probe was; a probe that did not
// evaluate is the refusal's reason.
func canaryProbe(ctx context.Context, in atomkit.In, bundle, principal, canary string) ([]string, string) {
	input := bundle + "." + principal + ".json"
	defer func() { _ = os.Remove(input) }()
	body := fmt.Sprintf(`{"principal":{"type":%q,"subject":"s"},"verbs":["search",%q]}`, principal, canary)
	if err := atoms.Place(input, []byte(body)); err != nil {
		return nil, err.Error()
	}
	out, code := in.Runner()(ctx, atoms.Cmd{Dir: in.Root, Name: "opa", Args: []string{
		"eval", "-b", bundle, "-i", input, "--format", "json", "data.authz.callable.allowed",
	}})
	if code != 0 {
		return nil, out
	}
	allowed, err := checks.OpaAllowed(out)
	if err != nil {
		return nil, err.Error()
	}
	return allowed, ""
}

// check builds the bundle and probes it as both principals.
//
// THE CANARY IS READ OFF THE ROSTER, NOT NAMED: the bundle's own data.json is
// asked for chaos's first star_only verb, and THAT one is proved. BOTH
// DIRECTIONS ARE ASSERTED: a curated verb visible to a session principal is the
// fail-open, and one invisible to a star principal is curation that narrowed
// both audiences instead of one.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if stop := dieskit.Shape(in.Tree(), row); stop != nil {
		return *stop
	}
	if stop := dieskit.OpaProvisioned(ctx, row, in); stop != nil {
		return *stop
	}
	bundle := atoms.TempPath("dies-bundle", ".tar.gz")
	defer func() { _ = os.Remove(bundle) }()
	data, stop := dieskit.Bundle(ctx, row, in, bundle)
	if stop != nil {
		return *stop
	}
	canary := checks.DiesCanary(data)
	if canary == "" {
		return checks.VerdictOf(row, int(checks.StateFindings), id+": FINDINGS - chaos has no star_only row in the built bundle - the roster did not survive the build.")
	}
	refuse := func(principal, why string) checks.Verdict {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the %s-principal probe against the built bundle did not evaluate.\n%s", id, principal, why))
	}
	session, why := canaryProbe(ctx, in, bundle, "session", canary)
	if why != "" {
		return refuse("session", why)
	}
	star, why := canaryProbe(ctx, in, bundle, "star", canary)
	if why != "" {
		return refuse("star", why)
	}
	seen := fmt.Sprintf("canary: %s (chaos's first star_only verb, read off the bundle)\nsession: %v\nstar:    %v", canary, session, star)

	if len(session) != 1 || session[0] != "search" {
		return checks.VerdictOf(row, int(checks.StateFindings), id+": FINDINGS - a curated verb is VISIBLE to a session principal in the built bundle - the roster did not survive the build.\n"+seen)
	}
	for _, verb := range star {
		if verb == canary {
			return checks.VerdictOf(row, int(checks.StatePass), id+": the built bundle still hides what it should\n"+seen)
		}
	}
	return checks.VerdictOf(row, int(checks.StateFindings), id+": FINDINGS - a curated verb is INVISIBLE to a star principal - curation narrowed both audiences, not one.\n"+seen)
}
