// Package orbitsidecars is the atom orbit:sidecars: what the contracts compose
// to is what flux carries, and it parses with the reader a star refuses to boot
// without. One file holds the atom and both runners execute it (lld.md FN-085).
package orbitsidecars

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/orbitcompose"
	"dagger/foundry-tools/internal/orbitkit"
)

// Def is the atom. It asks the two gate atoms' questions (orbitkit.OrbitComposed
// and orbitkit.OrbitSidecars), so it reads foundry-dies and execs orbitparse.
var Def = atomkit.Def{
	ID: "orbit:sidecars", Stage: checks.StageOrbit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "flux prime/orbits is what the contracts compose to and every sidecar parses with policy.ParseACL.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"tool:orbitparse", "dies"},
	Check:     check,
}

const id = "orbit:sidecars"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StageOrbit, Lane: checks.LaneAny}

// The two gate atoms this one folds into findings. Their rows are the identity
// their verdicts carry, as checks.AtomByID gave it.
var (
	composedRow = checks.AtomDef{ID: "ops:orbit-composed", Stage: checks.StagePrepush, Lane: checks.LaneAny}
	sidecarsRow = checks.AtomDef{ID: "ops:orbit-sidecars", Stage: checks.StagePrepush, Lane: checks.LaneAny}
)

// check: what the contracts compose to is what flux carries, and it parses with
// the reader a star refuses to boot without. On a flux tree it is the two gate
// atoms that already say so (ops:orbit-composed's -check and ops:orbit-sidecars'
// policy.ParseACL), run here as findings.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if len(in.Tree().Glob("prime/orbits/*"+orbitcompose.SidecarSuffix)) == 0 {
		return orbitkit.OrbitAbsent(row, "this tree renders no orbit sidecars (no prime/orbits/*.orbit.toml)")
	}
	var found []checks.Finding
	for _, sub := range []struct {
		row checks.AtomDef
		run func() checks.Verdict
	}{
		{composedRow, func() checks.Verdict { return orbitkit.OrbitComposed(composedRow, in) }},
		{sidecarsRow, func() checks.Verdict { return orbitkit.OrbitSidecars(ctx, sidecarsRow, in) }},
	} {
		v := sub.run()
		switch v.State {
		case int(checks.StateCannotRun):
			return orbitkit.OrbitCannot(row, fmt.Errorf("%s could not run: %s", sub.row.ID, v.Reason))
		case int(checks.StateFindings):
			found = append(found, checks.Finding{Verdict: checks.VerdictViolated, Subject: "prime/orbits", Cause: sub.row.ID, Detail: oneLine(v.Reason)})
		default:
			found = append(found, checks.Finding{Verdict: checks.VerdictHolds, Subject: "prime/orbits", Cause: sub.row.ID, Detail: oneLine(v.Reason)})
		}
	}
	return orbitkit.OrbitVerdict(row, found)
}

// oneLine is a reason folded onto one line, for a finding's detail.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
