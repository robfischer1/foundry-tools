// Package rustwitcompose is the atom rust:wit-compose: every wit-guest world
// composes into one component, and the tapes the crate carries replay through it
// with the answers the native core gave. It is a logic atom: the composer builds
// the guests it composes, so it needs no other atom's output.
package rustwitcompose

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/witreplay"
)

// Def is the atom. It builds in a target dir and dates the tree
// (atoms.RustStamp), so it says it writes. wasm-tools and wac are tools.toml
// url tools; the wasm32 target it adds itself.
//
// IT RUNS ON THE rust TOOLCHAIN, 1.97, as rust:wit-guest does, for the reason
// that atom's Def gives (DEF-B4-wit-2).
var Def = atomkit.Def{
	ID: "rust:wit-compose", Stage: checks.StagePrepush, Lane: checks.LaneRust,
	Group:         checks.GroupLanguage,
	Desc:          "Every wit-guest world composes into one valid component through tools/compose/compose.sh, and every tests/tapes/<world>.json replays through it with the native core's answers, and every tests/red/*.json replays as red (the answer is its `today`, never its prior-art response); absent where the tree carries no composer or replay host.",
	Surface:       "root:Cargo.toml",
	Toolchain:     "rust",
	Needs:         []string{"tool:wasm-tools", "tool:wac"},
	WritesTree:    true,
	NeedsManifest: true,
	Timeout:       45 * time.Minute,
	Check:         check,
}

const id = "rust:wit-compose"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneRust}

// check is the shared body, answering as this atom's row (internal/witreplay).
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return witreplay.Run(ctx, in, row)
}
