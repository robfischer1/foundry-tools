// Package gogofmt is the atom go:gofmt: every Go file is gofmt-clean. The
// question is internal/gofmtcheck's, which dev:go-fmt-check asks too.
package gogofmt

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/gofmtcheck"
)

// Def is the atom. gofmt is the go toolchain's, so it runs in the go image.
var Def = atomkit.Def{
	ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneGo,
	Group:      checks.GroupLanguage,
	Desc:       "Every Go file is gofmt-clean.",
	Surface:    "any:**/go.mod",
	Toolchain:  "go",
	NotOnForks: checks.ForkFleetStyle,
	Check:      check,
}

const id = "go:gofmt"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneGo}

// check lists the files gofmt would change.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return gofmtcheck.Check(ctx, in, row)
}
