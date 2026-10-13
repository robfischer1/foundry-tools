package main

import (
	"context"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// noRulesSast answers fleet:opengrep-sast for a tree with no rules/sast: a
// fork is scanned with the fleet's built-in rulesets (forkOpengrep), anything
// else is absentRuleset's. forkRecord fails open, so any doubt about the
// fork's status lands on the star's own answer.
func noRulesSast(ctx context.Context, r *run, a checks.AtomDef) checks.Verdict {
	if fork := r.forkRecord(ctx); fork != "" {
		return forkOpengrep(ctx, r, a, fork)
	}
	return absentRuleset(ctx, r, a)
}

// forkOpengrep scans a fork with the built-in rulesets for the languages its
// files hold. Rob, 2026-10-13: nothing is stamped into the fork's tree (fork-only
// files stay out of upstream PRs and syncs), and findings are advisory
// (checks.ForkSastVerdict). The binary's twin is internal/atoms
// forkOpengrep; the same checks function judges both.
func forkOpengrep(ctx context.Context, r *run, a checks.AtomDef, fork string) checks.Verdict {
	files, err := r.population(ctx, checks.ForkSastGlobs()...)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	sets := checks.ForkSastRulesets(files)
	if len(sets) == 0 {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - this fork holds no file in a language the fleet's built-in SAST rulesets cover")
	}
	ctr := r.lane(checks.ImageFleet).
		WithEnvVariable("LANG", "C.UTF-8").
		WithEnvVariable("LC_ALL", "C.UTF-8")
	for _, s := range sets {
		ctr = ctr.WithNewFile(checks.ForkSastDir+"/"+s.File, s.Body)
	}
	scan := ctr.WithExec([]string{"opengrep", "scan", "--config", checks.ForkSastDir, "--error", "."}, anyExit)
	out, code, err := output(ctx, scan)
	if err != nil {
		return neverRan(a, err)
	}
	// Both streams on a clean exit, as fleetOpengrepSast reads them.
	if code == 0 {
		if errOut, e := scan.Stderr(ctx); e == nil && errOut != "" {
			out = strings.TrimSpace(out + "\n" + errOut)
		}
	}
	return checks.ForkSastVerdict(a, fork, sets, out, code)
}
