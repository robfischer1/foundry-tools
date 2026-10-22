// Package devgofmtcheck is the atom dev:go-fmt-check: the check form of the fmt
// verb. It lists the Go files gofmt would change and fails when there are any;
// it never rewrites one (dev:go-fmt is the rewriting half).
package devgofmtcheck

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/gofmtcheck"
)

// Def is the atom. THE QUESTION IS internal/gofmtcheck's, the one go:gofmt asks, ASKED UNDER THIS ID: the same
// population (the files the repository would commit, less the fleet exclude and
// vendor/) and the same judgement (the listing is the verdict, a parse failure
// is a could-not-run), so `just fmt-check` and the commit stage cannot disagree.
// It writes nothing, so a run delivers no tree diff.
var Def = atomkit.Def{
	ID: id, Stage: "dev", Lane: checks.LaneGo,
	Group:     checks.GroupLanguage,
	Desc:      "Every Go file is gofmt-clean (the shared question); lists, never rewrites.",
	Surface:   "root:go.mod",
	Toolchain: "go",
	Check:     check,
}

const id = "dev:go-fmt-check"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: "dev", Lane: checks.LaneGo}

// check asks the shared question under this atom's row.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return gofmtcheck.Check(ctx, in, row)
}
