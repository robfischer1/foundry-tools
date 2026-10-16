// Package fleetsastrulesetlanes is the atom fleet:sast-ruleset-lanes: the SAST ruleset
// declares every lane this repository builds. One file holds the atom, and both runners execute it (lld.md FN-085).
package fleetsastrulesetlanes

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It runs in lead: the tree it reads is the pod's, and
// nothing else needs a toolchain.
var Def = atomkit.Def{
	ID: "fleet:sast-ruleset-lanes", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "The SAST ruleset declares every lane this repository actually builds.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "fleet:sast-ruleset-lanes"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// sastRulesetLanes: the SAST ruleset declares every lane this repository builds.
// A lane the ruleset never names is a lane nothing examined: exit 2 by design,
// not a finding.
func check(_ context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	entries, err := t.Entries(".")
	if err != nil {
		return checks.CannotEnumerate(row, err)
	}
	if !checks.HasEntry(entries, "rules") {
		return t.AbsentRuleset(row)
	}
	rulesEntries, err := t.Entries("rules")
	if err != nil {
		return checks.CannotEnumerate(row, err)
	}
	if !checks.HasEntry(rulesEntries, "sast") {
		return t.AbsentRuleset(row)
	}
	var bodies []string
	for _, p := range t.Glob("rules/sast/*.yml") {
		body, err := t.Read(p)
		if err != nil {
			// A ruleset file the atom could not read is a ruleset it did not
			// examine, and guessing its languages is the partial scan this
			// atom exists to catch.
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %s would not read: %v", id, p, err))
		}
		bodies = append(bodies, body)
	}
	declared, missing := checks.SastLanesMissing(bodies, entries)
	if len(missing) == 0 {
		return checks.VerdictOf(row, int(checks.StatePass), "fleet:sast-ruleset-lanes: ruleset declares every lane this repo builds")
	}
	return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf(
		"sast-ruleset-lanes: rules/sast declares [%s] but this repo also builds: %s\n"+
			"A ruleset that never names a lane never examines it, and opengrep still exits 0 whenever some OTHER language matched - the partial-scan case the zero-file refusal cannot see (foundry-stocks#4949).",
		strings.Join(declared, " "), strings.Join(missing, " ")))
}
