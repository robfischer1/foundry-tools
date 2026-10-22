// Package devgovuln is the atom dev:go-vuln: the vuln verb, which scans every Go
// module the tree carries for a known vulnerability.
package devgovuln

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/vulncheck"
)

// Def is the atom. THE SCAN IS internal/vulncheck's, the one go:govulncheck runs, RUN UNDER THIS ID: the same
// module download first, the same exit remapping (3 is a finding) and the same
// allowance, so `just vuln` and the push stage cannot disagree. govulncheck is
// the lane's pinned tool.
var Def = atomkit.Def{
	ID: id, Stage: "dev", Lane: checks.LaneGo,
	Group:     checks.GroupLanguage,
	Desc:      "govulncheck ./... reports no known vulnerability (go:govulncheck's scan), run by hand from the dev lane.",
	Surface:   "root:go.mod",
	Toolchain: "go",
	Needs:     []string{"tool:govulncheck"},
	Check:     check,
}

const id = "dev:go-vuln"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: "dev", Lane: checks.LaneGo}

// check runs the shared scan under this atom's row.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return vulncheck.Check(ctx, in, row)
}
