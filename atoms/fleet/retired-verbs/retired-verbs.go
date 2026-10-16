// Package fleetretiredverbs is the atom fleet:retired-verbs: no unit in the composition surface
// names a retired MCP verb. One file holds the atom, and both runners execute it (lld.md FN-085).
package fleetretiredverbs

import (
	"context"
	"fmt"
	"sort"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/retiredverbs"
)

// Def is the atom. It runs in lead: the tree it reads is the pod's, and
// nothing else needs a toolchain.
var Def = atomkit.Def{
	ID: "fleet:retired-verbs", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "No skill, agent, command, hook, setting, governance block or rule names a retired MCP verb.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "fleet:retired-verbs"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// retiredVerbsLedger is the ledger's one home: the root of foundry-stocks.
const retiredVerbsLedger = "retired-verbs.toml"

// retiredVerbs: no unit in the composition surface names a retired MCP verb.
// One repository's business (a tree without the ledger is ABSENT), and a ledger
// with no unit beside it is not a pass: the surface moved, and "0 files, 0
// findings" is the zero-file scan.
func check(_ context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	entries, err := t.Entries(".")
	if err != nil {
		return checks.CannotEnumerate(row, err)
	}
	if !checks.HasEntry(entries, retiredVerbsLedger) {
		return checks.VerdictOf(row, int(checks.StatePass), id+
			": ABSENT - this tree carries no "+retiredVerbsLedger+", so it is not the fleet's composition source and holds no unit this ledger grades")
	}
	body, err := t.Read(retiredVerbsLedger)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - %s would not read: %v", id, retiredVerbsLedger, err))
	}
	ledger, err := retiredverbs.Parse(body)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - %s is not a ledger this atom can grade against: %v", id, retiredVerbsLedger, err))
	}
	paths, err := t.Files()
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - the composition surface would not enumerate: %v", id, err))
	}
	var units []string
	for _, p := range paths {
		if retiredverbs.Scanned(p) {
			units = append(units, p)
		}
	}
	if len(units) == 0 {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+
			": CANNOT RUN - "+retiredVerbsLedger+" is here and no unit is: nothing under skills/, agents/, commands/, hooks/, settings/, governance/ or rules/ was found to grade, so the composition surface moved and this atom grades nothing until it knows where")
	}
	sort.Strings(units)
	var hits []retiredverbs.Hit
	for _, p := range units {
		text, err := t.Read(p)
		if err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf(
				"%s: CANNOT RUN - %s would not read: %v", id, p, err))
		}
		hits = append(hits, retiredverbs.Find(p, text, ledger)...)
	}
	if len(hits) == 0 {
		return checks.VerdictOf(row, int(checks.StatePass), fmt.Sprintf(
			"%s: %d unit(s) name none of the %d retired verbs", id, len(units), len(ledger)))
	}
	return checks.VerdictOf(row, int(checks.StateFindings), retiredverbs.Report(id, hits, ledger))
}
