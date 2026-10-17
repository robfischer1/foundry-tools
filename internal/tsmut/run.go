package tsmut

import (
	"context"
	"os"
	"path/filepath"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// runPackages runs Stryker in each planned package, in the order the diff first
// names them, and settles them as one verdict.
func (g *grading) runPackages(ctx context.Context, plan []checks.StrykerPackage, owned map[string]string) checks.Verdict {
	runs := make([]checks.StrykerRun, 0, len(plan))
	for _, pkg := range plan {
		dir := filepath.Join(g.tree, pkg.Dir)
		bin := filepath.Join(g.tree, "node_modules/.bin/stryker")
		for _, c := range checks.StrykerBinCandidates(pkg.Dir) {
			if _, err := os.Lstat(filepath.Join(g.tree, c)); err == nil {
				bin = filepath.Join(g.tree, c)
				break
			}
		}
		// The package's own config, wrapped with the fleet's skipped mutators
		// (checks.StrykerOverride): the list is the table's, never the package's.
		override := checks.StrykerOverride(owned[pkg.Dir], checks.SkippedMutators(checks.MutatorTS))
		if err := writeFile(filepath.Join(dir, checks.StrykerOverrideFile), []byte(override), 0o644); err != nil {
			return g.settle(2, "CANNOT RUN - the atom never ran: "+err.Error())
		}
		s := g.do(ctx, dir, bin, "run", checks.StrykerOverrideFile, "--mutate", pkg.Mutate(), "--concurrency", "4", "--reporters", "clear-text,json")
		if !atoms.ToolRan(s.code) {
			return g.neverRan(s)
		}
		// A file that does not read is absent: no report, no exemptions.
		report, _ := readFile(filepath.Join(dir, "reports/mutation/mutation.json"))
		exemptions, _ := readFile(filepath.Join(dir, "stryker-honest.json"))
		runs = append(runs, checks.StrykerRun{Dir: pkg.Dir, Status: s.code, Log: s.out, Report: string(report), Exemptions: string(exemptions)})
	}
	state, reason, found := checks.TSMutationVerdict(runs)
	v := g.settle(state, reason)
	// Built from the structured score, like go:mutation's — never parsed back out of
	// the rendered report (see checks.FindingsOf).
	v.Findings = found
	return v
}
