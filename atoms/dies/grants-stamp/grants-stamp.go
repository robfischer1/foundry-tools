// Package diesgrantsstamp is the atom dies:grants-stamp: policy/grants and policy/principals are
// renders, never hand edits (F15 W-E, 338; the lane is foundry-tools #17714).
// `themis grants render` is their only writer and stamps them rendered_from, tx
// and contracts_tx; CI holds no chaos credentials, so it asks whether a change is
// SHAPED like a render.
package diesgrantsstamp

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "dies:grants-stamp", Stage: checks.StagePrepush, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "policy/grants and policy/principals are renders, not hand edits: both carry rendered_from graph:policy and one tx, and a document changed against the pull's merge base carries a strictly higher tx and a contracts_tx that did not fall.",
	Surface:   "any:**",
	Toolchain: "python",
	Needs:     []string{"tool:git", "tool:uv"},
	Check:     check,
}

const id = "dies:grants-stamp"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneAny}

// checker is the tree's own.
const checker = "tools/check_grants_stamp.py"

// check runs the tree's checker, and its exit is the verdict, unmapped (0 holds,
// 1 refused, 2 could not run). The tree half (stamp present, one tx in both
// documents) always runs. The base half runs when the run has a base: the
// documents are compared with the PULL'S MERGE BASE's, not main's tip, so a pull
// that never touched them is not refused because main rendered since it
// branched. A base the history does not reach is a could-not-run: a comparison
// nobody made is not one that passed.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	if stop := dieskit.Shape(t, row); stop != nil {
		return *stop
	}
	if stop := atoms.RequirePaths(t, row, [][2]string{{checker, checker + " is absent, so there is no checker to run."}}); stop != nil {
		return *stop
	}
	args := []string{checker}
	if in.Base != "" {
		since, err := atoms.MergeBase(ctx, in.Root, in.Base)
		if err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - "+err.Error())
		}
		args = append(args, "--base", since)
	}
	return dieskit.Verdict(ctx, row, in, "", args...)
}
