// Package pythonartifacts is the atom python:artifacts: the wheels and sdists the publish
// lane uploads, built in the python toolchain container so the tree's build
// backend never runs where the index credentials are. Only lanes/publish.toml
// names it; its logic is internal/publishpack's.
package pythonartifacts

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/publishpack"
)

// Def is the atom. It writes dist/ under the release directory
// (releasekit.OutEnv), which the publish capability reads as /work/release/dist.
var Def = atomkit.Def{
	ID: "python:artifacts", Stage: checks.StagePublish, Lane: checks.LanePython,
	Group:     checks.GroupLanguage,
	Desc:      "uv build leaves the wheels and sdist the publish lane uploads under the release directory's dist/.",
	Surface:   "root:pyproject.toml",
	Toolchain: "python",
	Needs:     []string{"tool:uv"},
	Timeout:   45 * time.Minute,
	Check:     check,
}

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: "python:artifacts", Stage: checks.StagePublish, Lane: checks.LanePython}

func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return publishpack.Python(ctx, in, row)
}
