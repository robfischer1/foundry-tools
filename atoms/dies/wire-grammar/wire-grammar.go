// Package dieswiregrammar is the atom dies:wire-grammar: the wire grammar's two rules about answers
// (stellar-core F17): every string enum in a tool-output schema folds onto the six
// findings words or is declared kept, and a required non-null measure is attested
// or fixed.
package dieswiregrammar

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "dies:wire-grammar", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every string enum in an answer schema folds onto the six findings words or is declared kept, and no required measure can only say 'not measured' as 0 - each section warns until its count is 0, then fails.",
	Surface:   "any:**",
	Toolchain: "python",
	Needs:     []string{"tool:uv"},
	Check:     check,
}

const id = "dies:wire-grammar"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check runs the tree's checker. THE CHECKER AND ITS MODES ARE THE TREE'S: each
// section of contracts/wire-grammar.toml says warn or fail, so the flip from
// reporting to gating is a dies edit and this atom passes the exit code straight
// through: 0 holds (or warn only), 1 a fail-mode violation or a malformed
// registry, 2 the check could not run.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return dieskit.Checker(ctx, row, in, "", "tools/check_wire_grammar.py")
}
