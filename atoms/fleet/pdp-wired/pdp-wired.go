// Package fleetpdpwired is the atom fleet:pdp-wired: every container in the flux tree's
// prime/star-*.yaml that arms the policy PEP also sets its decision URL. One file holds the atom,
// and both runners execute it (lld.md FN-085).
package fleetpdpwired

import (
	"context"
	"fmt"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It runs in lead: the tree it reads is the pod's, and
// nothing else needs a toolchain.
var Def = atomkit.Def{
	ID: "fleet:pdp-wired", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "Every container in flux's prime/star-*.yaml that arms the stellar-core-go policy PEP (STELLAR_PEP_MODE not off) also sets STELLAR_PDP_URL - unset, the PEP consults nothing and, in witness mode, refuses nothing.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "fleet:pdp-wired"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// pdpWired: every container in the flux tree's prime/star-*.yaml that arms the
// policy PEP also sets its decision URL. A tree with no prime/ is not the flux
// tree (ABSENT); a prime/ with no star manifest is NOT absence - the manifests
// moved, and "0 files, 0 findings" is the zero-file scan.
func check(_ context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	entries, err := t.Entries(".")
	if err != nil {
		return checks.CannotEnumerate(row, err)
	}
	if !checks.HasEntry(entries, checks.PDPWiredDir) {
		return checks.VerdictOf(row, int(checks.StatePass), id+
			": ABSENT - this tree carries no "+checks.PDPWiredDir+"/, so it is not the fleet's flux tree and holds no star manifest")
	}
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	files := map[string]string{}
	for _, p := range in.Files {
		if !checks.IsPDPWiredFile(p) {
			continue
		}
		body, err := t.Read(p)
		if err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf(
				"%s: CANNOT RUN - %s would not read: %v", id, p, err))
		}
		files[p] = body
	}
	if len(files) == 0 {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+
			": CANNOT RUN - this is the fleet's flux tree ("+checks.PDPWiredDir+"/ is here) and holds no "+
			checks.PDPWiredDir+"/"+checks.PDPWiredPrefix+"*"+checks.PDPWiredSuffix+": the star manifests moved, and this atom grades nothing until PDPWiredDir says where")
	}
	state, report := checks.PDPWired(files)
	return checks.VerdictOf(row, state, report)
}
