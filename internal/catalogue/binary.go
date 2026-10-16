package catalogue

import "dagger/foundry-tools/internal/atomkit"

// THE ATOMS THE ATOMS BINARY VOTES FOR. Every atom of toolchain none, and the
// ones below: atoms that were in-process (internal/atoms) before they moved to a
// file of their own, run in the tools container with its prebuilt python venv,
// and declare toolchain python only for the executor, whose image has uv and no
// venv. On the Dagger path they keep voting in the binary's one process; moving
// them to a container each would add a network dependency (uv resolving a
// package at run time) and a container on every repository's gate for atoms that
// settle absent inside their own check. The adapter stays their re-ask.
//
// The set is declared, not guessed: an atom is added here by the commit that
// ports it from internal/atoms and for no other reason.
var votesInBinaryByHistory = map[string]bool{
	"fleet:wit-topics":        true,
	"dies:contracts":          true,
	"dies:contract-copies":    true,
	"dies:schema":             true,
	"dies:findings":           true,
	"dies:schemas":            true,
	"dies:wit-regenerated":    true,
	"dies:schema-rendered":    true,
	"dies:wire-grammar":       true,
	"dies:resource-referents": true,
	"dies:grants-stamp":       true,
	"dies:refusal-codes":      true,
	"ops:ansible":             true,
	"ops:declaration":         true,
	"ops:dup":                 true,
	"ops:metrics":             true,
	"ops:specs":               true,
	"ops:star-sa":             true,
	"ops:world-patches":       true,
	"template:render-matrix":  true,
}

// votesInBinary says whether the atoms binary carries d.
func votesInBinary(d *atomkit.Def) bool {
	return d.Toolchain == "none" || votesInBinaryByHistory[d.ID]
}
