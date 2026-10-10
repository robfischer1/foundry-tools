// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/govlaw"
)

func init() {
	register("law:lint", lawLint)
	register("law:verb-liveness", lawVerbLiveness)
	register("law:budget", lawBudget)
}

// THE LAW LANES (Nomos F5b): the first governance CI on foundry-stocks law
// commits. The judgement is internal/govlaw, shared with the in-process atoms;
// this reads the tree through the chain's Directory and hands it over.
//
// ABSENT OFF FOUNDRY-STOCKS, decided in govlaw from the path list (a root
// kits.toml beside a retired-verbs.toml), so no container is built for any of
// the three and a tree that is not foundry-stocks costs one glob.

func lawLane(ctx context.Context, r *run, id string, lane func(govlaw.Tree) govlaw.Result) checks.Verdict {
	a := checks.AtomByID(id)
	paths, err := r.src.Glob(ctx, "**")
	if err != nil {
		return cannotEnumerate(a, err)
	}
	res := lane(govlaw.Tree{Paths: paths, Read: func(p string) (string, error) {
		return r.src.File(p).Contents(ctx)
	}})
	return checks.VerdictOf(a, res.State, res.Report)
}

// lawLint: blocks are sound, kits name what exists, every span map covers its document.
func lawLint(ctx context.Context, r *run) checks.Verdict {
	return lawLane(ctx, r, "law:lint", govlaw.Lint)
}

// lawVerbLiveness: every verb the law names is served by hades and none is retired.
func lawVerbLiveness(ctx context.Context, r *run) checks.Verdict {
	return lawLane(ctx, r, "law:verb-liveness", govlaw.VerbLiveness)
}

// lawBudget: no render's always-on context outgrows its ceiling.
func lawBudget(ctx context.Context, r *run) checks.Verdict {
	return lawLane(ctx, r, "law:budget", govlaw.Budget)
}
