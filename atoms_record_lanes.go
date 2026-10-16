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
	register("ops:record-lanes", opsRecordLanes)
}

// ops:record-lanes - flux's RECORD_LANES (prime/daedalus-jobs.yaml) and
// record_lanes (the TOML in prime/ourea-config.yaml) are one set, and each holds
// every EXECUTOR_LANES lane plus atoms and dev. The judgement is
// checks.RecordLanes; this reads the tree. Neither file is not the flux tree
// (ABSENT); one of them is not absence, the other moved.
func opsRecordLanes(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("ops:record-lanes")

	present, err := r.population(ctx, checks.RecordLanesJobsFile, checks.RecordLanesOureaFile)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	files := map[string]string{}
	for _, p := range present {
		if !checks.IsRecordLanesFile(p) {
			continue
		}
		body, err := r.src.File(p).Contents(ctx)
		if err != nil {
			return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - %s would not read: %v", a.ID, p, err))
		}
		files[p] = body
	}
	if len(files) == 0 {
		return checks.VerdictOf(a, 0, string(a.ID)+": ABSENT - this tree carries neither "+
			checks.RecordLanesJobsFile+" nor "+checks.RecordLanesOureaFile+", so it is not the fleet's flux tree")
	}
	state, report := checks.RecordLanes(files)
	return checks.VerdictOf(a, state, report)
}
