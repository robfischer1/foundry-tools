// Package fleetnodekindsdeclared is the atom fleet:node-kinds-declared: every node kind the tree's
// Go captures is declared in chaos. One file holds the atom, and both runners execute it (lld.md FN-085).
package fleetnodekindsdeclared

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It runs in lead: the tree it reads is the pod's, and
// nothing else needs a toolchain.
var Def = atomkit.Def{
	ID: "fleet:node-kinds-declared", Stage: checks.StagePrepush, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "Every node kind this repo's Go code captures is declared in chaos's node_kinds, so the foreign key never refuses it at runtime.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "fleet:node-kinds-declared"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneAny}

// nodeKindsDeclared: every node kind the tree's Go captures is declared in
// chaos. A tree that captures nothing never asks the door; a door that would
// not answer is CANNOT RUN, never a finding.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	files, bad := checks.ReadAll(in.Tree().Read, checks.WithSuffix(in.Files, ".go", ".py", ".ts", ".tsx", ".rs"))
	if bad != "" {
		return checks.VerdictOf(row, int(checks.StateCannotRun), "fleet:node-kinds-declared: CANNOT RUN - "+bad)
	}
	note := func(state int) string {
		// The pass's honesty: this atom reads Go only, so a PASS names the
		// non-Go files that look like captures and were not read.
		if state != 0 {
			return ""
		}
		return checks.NonGoNote(checks.NonGoCaptureFiles(files))
	}
	if uses, _ := checks.CapturedKinds(files); len(uses) == 0 {
		state, report := checks.NodeKindsDeclared(files, "")
		return checks.VerdictOf(row, state, report+note(state))
	}
	schema, err := checks.FetchNodeKindsSchema(ctx, in.DoorOf())
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), "fleet:node-kinds-declared: CANNOT RUN - "+err.Error()+". A vocabulary that was not read declares nothing, and an undeclared kind is not a finding about this tree.")
	}
	state, report := checks.NodeKindsDeclared(files, schema)
	return checks.VerdictOf(row, state, report+note(state))
}
