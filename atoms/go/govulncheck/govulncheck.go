// Package gogovulncheck is the atom go:govulncheck: govulncheck ./... reports no
// known vulnerability. The scan is internal/vulncheck's, which dev:go-vuln runs too.
package gogovulncheck

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/vulncheck"
)

// Def is the atom. govulncheck is the lane's tool, baked in at its pin.
var Def = atomkit.Def{
	ID: id, Stage: checks.StagePrepush, Lane: checks.LaneGo,
	Group:     checks.GroupLanguage,
	Desc:      "govulncheck ./... reports no known vulnerability.",
	Surface:   "any:**/go.mod",
	Toolchain: "go",
	Needs:     []string{"tool:govulncheck"},
	Check:     check,
}

const id = "go:govulncheck"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneGo}

// check scans every Go module the tree carries.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return vulncheck.Check(ctx, in, row)
}
