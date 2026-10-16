// Package gotest is the atom go:test: go test ./... passes — the commit's unit
// suite, no race detector and no database.
package gotest

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. NeedsDies: the suite runs a repo's armed goldens, which read
// foundry-dies. It is SUBSUMED BY go:test-race: a caller that asks for both
// stages at once gets the race run and an omission line saying so, never the
// suite twice.
var Def = atomkit.Def{
	ID: "go:test", Stage: checks.StagePrecommit, Lane: checks.LaneGo,
	Group:      checks.GroupLanguage,
	Desc:       "go test ./... passes — the unit suite, no race detector and no database.",
	Surface:    "any:**/go.mod",
	Toolchain:  "go",
	Needs:      []string{"dies"},
	SubsumedBy: "go:test-race",
	Timeout:    45 * time.Minute,
	Check:      check,
}

const id = "go:test"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneGo}

// check runs the unit suite in every module the tree carries.
//
// go test ./... passes: the SAME packages, at the commit's cadence — no race
// detector, no database, no build tags, so the DB-gated suites do not even
// compile in. Rob's four stages put the unit tests at the commit and the race
// + live-DB run at the push; a commit that waits minutes for a suite it will
// wait for again at the push is the loop this plan exists to shorten.
//
// NO TESTS IS A FINDING: `go test ./...` prints "[no test files]" per package
// and exits 0, so a module with no test anywhere read green. The count is go's
// own, and a module where every package answers 0 is red before the suite runs.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return atoms.EachGoModule(in, row, func(dir string) checks.Verdict {
		return atoms.GoSuite(ctx, in, row, dir, false)
	})
}
