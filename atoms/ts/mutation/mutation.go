// Package tsmutation is the atom ts:mutation. Its declaration is this file; the questions it
// asks of the tree and the tools it runs are internal/tsmut's, because the
// atoms lane builds an atom from one file.
package tsmutation

import (
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/tsmut"
)

// Def is the atom. It judges the change, so its environment hands it the base.
// bun installs and node runs Stryker: the Dagger lane image carries both, git,
// curl and procps (Stryker tears its test-runner processes down with tree-kill,
// which spawns `ps`; with none on PATH the unhandled ENOENT killed Stryker right
// after the dry run, theia #57, 2026-09-13).
var Def = atomkit.Def{
	ID: "ts:mutation", Stage: checks.StageMutation, Lane: checks.LaneTS,
	Group:      checks.GroupLanguage,
	Desc:       "Every mutant StrykerJS makes of this pull's changes to the declared critical modules is killed by the tests.",
	Surface:    "root:package.json",
	Toolchain:  "bun",
	Needs:      []string{"tool:git", "tool:node", "tool:curl"},
	NotOnForks: checks.ForkChangeBar,
	History:    "full",
	Timeout:    75 * time.Minute,
	Check:      tsmut.Check,
}
