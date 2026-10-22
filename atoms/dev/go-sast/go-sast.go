// Package devgosast is the atom dev:go-sast: the sast verb, which runs the
// opengrep scan over rules/sast as the gate runs it.
package devgosast

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/sastcheck"
)

// Def is the atom. THE SCAN IS internal/sastcheck's, the one fleet:opengrep-sast runs, RUN UNDER THIS ID: the same
// ruleset, flags and zero-file refusal, so `just sast` and the commit stage
// cannot disagree. opengrep is a pinned tool the pod lays beside the go
// toolchain.
var Def = atomkit.Def{
	ID: id, Stage: "dev", Lane: checks.LaneGo,
	Group:     checks.GroupLanguage,
	Desc:      "opengrep over rules/sast, refusing a zero-file scan (fleet:opengrep-sast's scan), run by hand from the dev lane.",
	Surface:   "root:go.mod",
	Toolchain: "go",
	Needs:     []string{"tool:opengrep"},
	Check:     check,
}

const id = "dev:go-sast"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: "dev", Lane: checks.LaneGo}

// check runs the shared scan under this atom's row.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return sastcheck.Check(ctx, in, row)
}
