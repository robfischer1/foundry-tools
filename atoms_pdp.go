// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"

	"dagger/foundry-tools/internal/checks"
)

func init() {
	register("fleet:pdp-wired", fleetPDPWired)
}

// Every container in the flux tree's prime/star-*.yaml that arms the policy PEP
// also sets STELLAR_PDP_URL. The judgement is checks.PDPWired; this reads the
// tree. A tree with no prime/ is not flux (ABSENT); a prime/ with no star
// manifest is not absence, the manifests moved.
func fleetPDPWired(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("fleet:pdp-wired")

	entries, err := r.src.Entries(ctx)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(entries, checks.PDPWiredDir) {
		return checks.VerdictOf(a, 0, string(a.ID)+
			": ABSENT - this tree carries no "+checks.PDPWiredDir+"/, so it is not the fleet's flux tree and holds no star manifest")
	}
	present, err := r.population(ctx, checks.PDPWiredDir+"/"+checks.PDPWiredPrefix+"*"+checks.PDPWiredSuffix)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	files := map[string]string{}
	for _, p := range present {
		if !checks.IsPDPWiredFile(p) {
			continue
		}
		body, err := r.src.File(p).Contents(ctx)
		if err != nil {
			return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - %s would not read: %v", a.ID, p, err))
		}
		files[p] = body
	}
	if len(files) == 0 {
		return checks.VerdictOf(a, 2, string(a.ID)+
			": CANNOT RUN - this is the fleet's flux tree ("+checks.PDPWiredDir+"/ is here) and holds no "+
			checks.PDPWiredDir+"/"+checks.PDPWiredPrefix+"*"+checks.PDPWiredSuffix+": the star manifests moved, and this atom grades nothing until PDPWiredDir says where")
	}
	state, report := checks.PDPWired(files)
	return checks.VerdictOf(a, state, report)
}
