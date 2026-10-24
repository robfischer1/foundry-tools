// Package devrustlint is the atom dev:rust-lint, the dev verb `lint`: cargo clippy
// over the crates the change reaches (the gate's scope), else the whole
// workspace. The body is internal/devrust's.
package devrustlint

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/devrust"
)

// Verb is the cargo verb this atom runs.
var Verb = devrust.Verb{ID: "dev:rust-lint", Cargo: "clippy", Scope: "clippy", Link: false}

// Def is the atom. The scope script runs under the fleet's python3, as the
// gate's rust:cargo-test has it (tool:python-standalone, tool:uv).
var Def = atomkit.Def{
	ID: Verb.ID, Stage: devrust.Stage, Lane: checks.LaneRust,
	Group:         checks.GroupLanguage,
	Desc:          "cargo clippy is clean under the fleet's lint set over the crates the change reaches (else the whole workspace), run by hand from the dev lane under its other name, `lint`.",
	Surface:       "root:Cargo.toml",
	Toolchain:     "rust",
	Needs:         []string{"tool:python-standalone", "tool:uv"},
	NeedsManifest: true,
	Timeout:       10 * time.Minute,
	Check:         func(ctx context.Context, in atomkit.In) checks.Verdict { return devrust.Check(ctx, in, Verb) },
}
