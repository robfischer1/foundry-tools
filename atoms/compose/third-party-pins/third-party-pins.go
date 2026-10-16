// Package composethirdpartypins is the atom compose:third-party-pins: zero
// ${PIN_} image interpolations; the BP6b ratchet stays closed. One file holds
// the atom and both runners execute it (lld.md FN-085).
package composethirdpartypins

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It reads the compose specs and nothing else, so it runs in
// lead.
var Def = atomkit.Def{
	ID: "compose:third-party-pins", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Zero ${PIN_} image interpolations — the BP6b ratchet stays closed.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "compose:third-party-pins"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check: zero ${PIN_} image interpolations, the BP6b ratchet
// stays closed. A count gate only stays closed if the count happened: a body
// that could not be read is a 2.
func check(_ context.Context, in atomkit.In) checks.Verdict {
	files, stop := atomlib.ComposeSurface(row, in)
	if stop != nil {
		return *stop
	}
	t := in.Tree()
	bodies := map[string]string{}
	for _, f := range checks.PinScanFiles(files) {
		body, err := t.Read(f)
		if err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the ${PIN_} scan itself failed (could not read %s: %v). Refusing to report a closed ratchet on a scan that did not run.", id, f, err))
		}
		bodies[f] = body
	}
	if hits := checks.PinInterpolations(bodies); len(hits) != 0 {
		return checks.VerdictOf(row, int(checks.StateFindings), fmt.Sprintf("%s: FINDINGS - %d ${PIN_} interpolation(s) found - the pin era does not reopen:\n%s",
			id, len(hits), strings.Join(hits, "\n")))
	}
	return checks.VerdictOf(row, int(checks.StatePass), id+": zero ${PIN_} interpolations; the pin era stays closed")
}
