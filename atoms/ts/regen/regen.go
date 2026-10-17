// Package tsregen is the atom ts:regen: each core the star's justfile names
// regenerates byte-identical from its pinned WIT, its pinned stellar-core-rust
// commit and its pinned tools. The steps are internal/regenlane's.
package tsregen

import (
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/regenlane"
)

// Def is the atom. THE ONE TOOLCHAIN IS THE RUST ONE the provenance records
// (rust-wasm, rustc 1.90.0): the guest's bytes are a function of the compiler.
// The transpile needs node and is not a second atom, because an atom is one row
// of the record and the record's ts:regen is one row; node arrives as a pinned
// tarball (checks.NodeRegenURL), as wasm-tools does, and is placed under
// ATOM_WORK. Every step is an exec of a program with its arguments; the star's
// own inner scripts are programs the star ships and are exec'd, never wrapped.
var Def = atomkit.Def{
	ID: "ts:regen", Stage: checks.StagePrepush, Lane: checks.LaneTS,
	Group: checks.GroupLanguage,
	Desc:  "Each core the justfile's regen-check names regenerates byte-identical from its pinned WIT, core commit and tool versions (the star's own build and transpile scripts, run on the engine); absent where the justfile calls no scripts/regen-check.sh.",
	// The lane's surface; the justfile line is read in Check, as the chain read it.
	Surface:       "root:package.json",
	Toolchain:     "rust-wasm",
	Timeout:       45 * time.Minute,
	NeedsManifest: true,
	Check:         regenlane.Check,
}
