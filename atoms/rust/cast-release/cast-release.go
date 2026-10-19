// Package rustcastrelease is the atom rust:cast-release: the cast lane's compile
// of a Cargo repo's binaries. Only lanes/cast.toml names it. The compile is the
// Dagger cast lane's too, so its logic is internal/castrelease's.
package rustcastrelease

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/castrelease"
	"dagger/foundry-tools/internal/checks"
)

const id = "rust:cast-release"

// Def is the atom. It builds the workspace's bin targets (the cast ships every
// one, a Dockerfile or none), so it asks for no star record. It dates the tree
// (atoms.RustStamp), which a shared tree would carry to every other atom, so it
// says it writes it.
var Def = atomkit.Def{
	ID: "rust:cast-release", Stage: checks.StageCast, Lane: checks.LaneRust,
	Group:      checks.GroupLanguage,
	Desc:       "The binaries a Cargo repo casts compile under cargo's release profile: every bin target of the workspace's default members, in one build.",
	Surface:    "root:Cargo.toml",
	Toolchain:  "rust",
	Timeout:    45 * time.Minute,
	WritesTree: true,
	Check:      check,
}

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StageCast, Lane: checks.LaneRust}

func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return castrelease.Rust(ctx, in, row)
}
