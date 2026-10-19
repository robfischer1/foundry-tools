// Package devtsregen is the atom dev:ts-regen: the write twin of ts:regen. It
// regenerates the worlds named of a stellar-core-ts tree (guest, component,
// transpile) under their recorded pins and writes the result into the tree. The
// steps are internal/regenlane's.
package devtsregen

import (
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/regenlane"
)

// Def is the atom. IT IS ts:regen'S SAME WORK, WRITING: the toolchain is the one
// the provenance records (rust-wasm, rustc 1.90.0), node, jco and wasm-tools
// arrive as pinned tarballs placed under ATOM_WORK like ts:regen's, and it needs
// nothing of its own. What differs is the product: the regenerated files of
// src/<world>core-gen and the new guest and component hashes in its
// provenance.json, delivered as the diff of the atom's copy of the tree, and the
// world names it takes from the dev request.
var Def = atomkit.Def{
	ID: "dev:ts-regen", Stage: "dev", Lane: checks.LaneTS,
	Group:      checks.GroupLanguage,
	Desc:       "Regenerates the worlds named (guest, component, transpile) under their recorded pins and writes the generated files and provenance.json's hashes into the tree.",
	Surface:    "root:package.json",
	Toolchain:  "rust-wasm",
	WritesTree: true,
	Timeout:    45 * time.Minute,
	Check:      regenlane.CheckWrite,
}
