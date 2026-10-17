// Package pythonmutation is the atom python:mutation. Its declaration is this file; the questions it
// asks of the tree and the tools it runs are internal/pymut's, because the
// atoms lane builds an atom from one file.
package pythonmutation

import (
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/pymut"
)

// Def is the atom. It judges the change, so its environment hands it the base.
// Its scope is critical_modules off .copier-answers.yml and it runs cosmic-ray
// against the project — neither of which a tree with no manifest has. Rob,
// 2026-09-25: "Leave mutation off for now." Off is the manifest surface; the
// lane's lint and test still run.
var Def = atomkit.Def{
	ID: "python:mutation", Stage: checks.StageMutation, Lane: checks.LanePython,
	Group:         checks.GroupLanguage,
	Desc:          "Every mutant cosmic-ray makes of this pull's changes to the declared critical modules is killed by the tests.",
	Surface:       "root:pyproject.toml",
	Toolchain:     "python",
	Needs:         []string{"tool:git", "tool:uv"},
	NotOnForks:    checks.ForkChangeBar,
	NeedsManifest: true,
	History:       "full",
	Timeout:       75 * time.Minute,
	Check:         pymut.Check,
}
