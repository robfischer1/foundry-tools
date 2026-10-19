// Package tspack is the atom ts:pack: the npm tarballs the publish lane uploads,
// built and packed in the bun toolchain container so the tree's scripts never
// run where the registry credentials are. Only lanes/publish.toml names it; its
// logic is internal/publishpack's.
package tspack

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/publishpack"
)

// Def is the atom. It installs and builds in the checkout (it writes the tree)
// and stages each package's tarball under the release directory's npm/.
var Def = atomkit.Def{
	ID: "ts:pack", Stage: checks.StagePublish, Lane: checks.LaneTS,
	Group:      checks.GroupLanguage,
	Desc:       "bun builds each package the publish lane uploads and packs its tarball under the release directory's npm/.",
	Surface:    "root:package.json",
	Toolchain:  "bun",
	Timeout:    45 * time.Minute,
	WritesTree: true,
	Check:      check,
}

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: "ts:pack", Stage: checks.StagePublish, Lane: checks.LaneTS}

func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return publishpack.TS(ctx, in, row)
}
