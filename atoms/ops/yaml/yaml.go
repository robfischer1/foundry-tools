// Package opsyaml is the atom ops:yaml: no tracked YAML carries a duplicate
// key. One file holds the atom and both runners execute it (lld.md FN-085).
package opsyaml

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It reads the committable tree and nothing else, so it runs
// in lead.
var Def = atomkit.Def{
	ID: "ops:yaml", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "No tracked YAML carries a duplicate key — the defect a YAML loader resolves last-wins and never reports; a repo with its own tools/yaml-strict (infra) runs that.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "ops:yaml"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check: no tracked YAML carries a duplicate key, the defect every loader the
// fleet runs resolves last-wins and never reports.
//
// THE CHAIN'S PYTHON BRANCH IS NOT PORTED. It runs a repo's own tools/yaml-strict
// instead when the tree carries one, and that tool exists in no repository in
// the fleet (infra, the one repo it was written for, does not have it): the
// branch has never fired, so the binary has the parse alone, checks.OpsStrictYAML.
// If a tree ever does carry one, the shadow will say so as a STATE difference.
func check(_ context.Context, in atomkit.In) checks.Verdict {
	if stop := atomlib.OpsSurface(row, in); stop != nil {
		return *stop
	}
	yamls := checks.OpsYAMLFiles(checks.OpsFiles(in.Committable))
	if len(yamls) == 0 {
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - no yaml in this tree")
	}
	t := in.Tree()
	errs := map[string]error{}
	for _, f := range yamls {
		src, err := t.Read(f)
		if err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), "the atom never ran: "+err.Error())
		}
		errs[f] = checks.OpsStrictYAML(src)
	}
	report, bad := checks.OpsYAMLReport(yamls, errs)
	rc := 0
	if bad {
		rc = 1
	}
	state, line := checks.OpsSettle("yaml", rc, report)
	if line != "" {
		report += "\n" + line
	}
	return checks.VerdictOf(row, state, report)
}
