// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"

	"dagger/foundry-tools/internal/checks"
)

func init() {
	register("fleet:wit-topics", fleetWitTopics)
}

// witTopicsChecker is flux's own gate for redpanda/wit (stellar-core F2).
const witTopicsChecker = "tools/wit-topics"

// flux's redpanda/wit is the WIT of redpanda/schemas (stellar-core F2).
//
// THE CHECKER IS THE TREE'S, NOT EMBEDDED, for the dies:wit-regenerated reason.
// flux holds neither the generator nor a way to run it, so tools/wit-topics
// compares the digests the generator recorded in aiws-topics.report.json: a
// schema edited and not regenerated, a topic added or removed, and a hand edit
// of aiws-topics.wit are one failure each. It was red locally on every
// mutation and ran in no lane, so "a schema change without regeneration fails
// CI on the first push" was a sentence, not a fact.
//
// foundry-dies is NOT graded here. Its dies:wit-regenerated atom regenerates
// from its own schema/ and reads no flux tree; the topic schemas live in flux,
// so flux is the repo that goes red when they part from their WIT.
//
// THE EXIT CODE IS THE VERDICT, unmapped: 0 current, 1 stale, 2 could not run.
//
// ABSENT ONLY ON A TREE THAT IS NOT FLUX's, decided in Go from the Directory.
// prime/ is the fleet's flux tree and nothing else's (fleet:ourea-config-retired-keys
// reads the same marker). On that tree a missing checker is a CANNOT RUN naming
// it, never an absence: a gate that goes quiet when its script moves is how
// this one graded nothing.
func fleetWitTopics(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("fleet:wit-topics")

	roots, err := r.src.Entries(ctx)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(roots, oureaConfigDir) {
		return checks.VerdictOf(a, 0, a.ID+
			": ABSENT - this tree carries no "+oureaConfigDir+"/, so it is not the fleet's flux tree and holds no topic schemas to render")
	}
	if stop := requirePaths(ctx, r, a, [][2]string{
		{witTopicsChecker, witTopicsChecker + " is absent, so there is no checker to run and the topics' WIT is ungraded."},
	}); stop != nil {
		return *stop
	}
	return verdict(ctx, a, r.lane(checks.ImageFleet).
		WithExec([]string{"uv", "--version"}).
		WithExec(stdlibpy(witTopicsChecker), anyExit))
}
