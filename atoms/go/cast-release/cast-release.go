// Package gocastrelease is the atom go:cast-release: the cast lane's compile of
// a Go repo's binaries. Only lanes/cast.toml names it. The compile is the Dagger
// cast lane's too, so its logic is internal/castrelease's.
package gocastrelease

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/castrelease"
	"dagger/foundry-tools/internal/checks"
)

const id = "go:cast-release"

// Def is the atom. It builds every main package under cmd/, so it asks for no
// star record.
var Def = atomkit.Def{
	ID: "go:cast-release", Stage: checks.StageCast, Lane: checks.LaneGo,
	Group:     checks.GroupLanguage,
	Desc:      "The binaries a Go repo casts compile with the fleet's release flags: every main package under cmd/, one build each.",
	Surface:   "root:go.mod",
	Toolchain: "go",
	Needs:     []string{"dies"}, // layer-cast reads the star's record in the pod's dies (DEF-B6-012)
	Timeout:   45 * time.Minute,
	Check:     check,
}

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StageCast, Lane: checks.LaneGo}

func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return castrelease.Go(ctx, in, row)
}
