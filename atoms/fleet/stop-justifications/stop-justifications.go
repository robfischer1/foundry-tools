// Package fleetstopjustifications is the atom fleet:stop-justifications: no silent suppression
// of any gate. One file holds the atom, and both runners execute it (lld.md FN-085).
package fleetstopjustifications

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It runs in lead: the tree it reads is the pod's, and
// nothing else needs a toolchain.
var Def = atomkit.Def{
	ID: "fleet:stop-justifications", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:      checks.GroupBasic,
	Desc:       "No silent suppression of any gate — a suppression carries a tool-conflict line.",
	Surface:    "any:**",
	Toolchain:  "none",
	NotOnForks: checks.ForkFleetStyle,
	Check:      check,
}

const id = "fleet:stop-justifications"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// stopJustifications: no silent suppression of any gate.
//
// THE CHAIN'S OWN CALL. checks.StopJustifications(SJInput{...}) is the scan;
// what moves is who answers its two questions. The chain asked `git ls-files`
// and `src.File(p).Contents`; here Collect asked the same git and os.ReadFile
// answers the reads. The scan asks only for what checks.SJReads names (a test
// holds it to that), so reading lazily reads exactly what the chain fetched
// up front.
//
// A TREE THAT WILL NOT LIST IS A CANNOT RUN, not an empty scan: pre-commit
// hides a passing hook's output, so a silent skip is indistinguishable from a
// clean scan. A file that will not read reaches the scan as its read error,
// which the scan files itself.
//
// No origin is a repository that names no exemption, not a refusal: the
// checkout's directory is not a name (DirectoryExempt is keyed on the remote,
// the one coordinate a checkout cannot rename).
func check(_ context.Context, in atomkit.In) checks.Verdict {
	if in.TrackedErr != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), "stop-justifications: CANNOT RUN — "+in.TrackedErr.Error()+
			"\nstop-justifications: refusing to report success without scanning.")
	}
	repo := ""
	if in.Origin != "" {
		repo = checks.RepoFromOrigin(in.Origin)
	}
	state, out := checks.StopJustifications(checks.SJInput{
		Tracked: in.Tracked,
		Read: func(rel string) (string, error) {
			b, err := os.ReadFile(filepath.Join(in.Root, rel))
			return string(b), err
		},
		Repo: repo,
		// The UTC date: a justification expires on one, which is why this atom
		// is whole-tree scope even though it only reads tracked files.
		Today: in.Now.UTC().Format(time.DateOnly),
	})
	return checks.VerdictOf(row, state, out)
}
