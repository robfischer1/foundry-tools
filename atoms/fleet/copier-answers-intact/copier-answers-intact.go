// Package fleetcopieranswersintact is the atom fleet:copier-answers-intact: .copier-answers.yml
// carries no renovate crash receipt. One file holds the atom, and both runners execute it (lld.md FN-085).
package fleetcopieranswersintact

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
	ID: "fleet:copier-answers-intact", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "No crashed copier render is recorded in .copier-answers.yml.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "fleet:copier-answers-intact"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// copierAnswersIntact: .copier-answers.yml carries no renovate crash receipt.
// EXIT 1, NOT 2: a file carrying what it should not is a finding about the
// tree, not a check that could not run.
func check(_ context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	entries, err := t.Entries(".")
	if err != nil {
		return checks.CannotEnumerate(row, err)
	}
	if !checks.HasEntry(entries, checks.CopierAnswersFile) {
		return checks.VerdictOf(row, int(checks.StatePass),
			"fleet:copier-answers-intact: ABSENT - this tree carries no "+checks.CopierAnswersFile+
				", so it is not copier-stamped and has no render to crash")
	}
	body, err := t.Read(checks.CopierAnswersFile)
	if err != nil {
		// A file the atom could not read is a file it did not examine, and
		// guessing it is clean is how the marker got six weeks of cover.
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - %s would not read: %v", id, checks.CopierAnswersFile, err))
	}
	if !strings.Contains(body, checks.CopierSentinel) {
		return checks.VerdictOf(row, int(checks.StatePass), "fleet:copier-answers-intact: no crash receipt in "+checks.CopierAnswersFile)
	}
	return checks.VerdictOf(row, int(checks.StateFindings), checks.CopierMarkerReport())
}
