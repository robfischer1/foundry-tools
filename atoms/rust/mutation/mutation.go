// Package rustmutation is the atom rust:mutation. Its declaration is this file; the questions it
// asks of the tree and the tools it runs are internal/rustmut's, because the
// atoms lane builds an atom from one file.
package rustmutation

import (
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/rustmut"
)

// Def is the atom. It judges the change, so its environment hands it the base.
// cargo-mutants and the nextest it drives are tools of its own: only this atom
// runs them, so no other rust atom waits on, or pays for, either.
//
// THE DEADLINE IS THE LANE'S, as go:mutation's (75 minutes).
var Def = atomkit.Def{
	ID: "rust:mutation", Stage: checks.StageMutation, Lane: checks.LaneRust,
	Group:      checks.GroupLanguage,
	Desc:       "Every viable mutant cargo-mutants makes of this pull's changes to the declared critical modules is killed by the tests.",
	Surface:    "root:Cargo.toml",
	Toolchain:  "rust-trixie",
	Needs:      []string{"tool:cargo-mutants", "tool:cargo-nextest", "tool:python-standalone"},
	NotOnForks: checks.ForkChangeBar,
	History:    "full",
	Timeout:    75 * time.Minute,
	Check:      rustmut.Check,
}
