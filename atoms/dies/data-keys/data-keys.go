// Package diesdatakeys is the atom dies:data-keys: the BUILT bundle carries every data root
// the policy reads, non-empty. The source tree is not a proxy for the artifact:
// `opa test policy/` passes on a tree whose built bundle is empty, so this builds
// the bundle and reads its data document.
package diesdatakeys

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
	ID: "dies:data-keys", Stage: checks.StagePrepush, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "The BUILT bundle carries every data root the policy reads, non-empty.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"tool:opa"},
	Check:     check,
}

const id = "dies:data-keys"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneAny}

// check builds the bundle and grades the roots in its data.json. A data.json
// that will not parse is a 2: a shape the atom cannot read is a could-not-run,
// not a count nobody computed.
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
	missing, stars, err := checks.DiesDataKeys(data)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the built bundle's data.json is a shape this atom cannot read (%v), so there is no data document to grade.", id, err))
	}
	if len(missing) > 0 {
		return checks.VerdictOf(row, int(checks.StateFindings), fmt.Sprintf("%s: FINDINGS - ::error::bundle data.json is missing/empty: %v. Bundle mode reads ONLY files named data.json - check the policy/<root>/data.json layout.", id, missing))
	}
	return checks.VerdictOf(row, int(checks.StatePass), fmt.Sprintf("%s: data roots ok; star_only carries %d stars", id, stars))
}
