// Package fleetnorejfiles is the atom fleet:no-rej-files: no `.rej` reject file
// was committed. copier writes one beside a file whose 3-way update conflicted
// when it runs with `--conflict rej`; the hunks in it were NOT applied, so a
// tree that tracks one is a tree whose template update silently half-landed.
// fleet:check-merge-conflict guards the other mode (`--conflict inline`, the
// markers); this guards the one with nothing in the file itself to find.
package fleetnorejfiles

import (
	"context"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It reads the tracked file list and nothing else, so it runs
// in lead.
var Def = atomkit.Def{
	ID: "fleet:no-rej-files", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "No .rej reject file (an unapplied merge hunk) was committed.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "fleet:no-rej-files"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check names every tracked path ending in ".rej". The name is the whole test:
// a reject file's content is a diff, and nothing in a diff tells it apart from
// a patch kept on purpose, so a repo that needs one names it something else.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	var hits []string
	for _, f := range in.Files {
		if err := ctx.Err(); err != nil {
			return checks.Interrupted(row, err)
		}
		if strings.HasSuffix(f, ".rej") {
			hits = append(hits, f+": a reject file — its hunks were not applied; resolve them into the file it sits beside and delete it")
		}
	}
	if len(hits) > 0 {
		return checks.VerdictOf(row, int(checks.StateFindings), strings.Join(hits, "\n"))
	}
	return checks.VerdictOf(row, int(checks.StatePass), id+": no reject files")
}
