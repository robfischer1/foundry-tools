// Package gomutation is the atom go:mutation. Its declaration is this file; the questions it
// asks of the tree and the tools it runs are internal/gomut's, because the
// atoms lane builds an atom from one file.
package gomutation

import (
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/gomut"
)

// Def is the atom. NeedsDies FOR THE SAME REASON go:test-race CARRIES IT:
// gomutants gathers coverage by running `go test`, and an atom that runs a
// repo's `go test` runs its armed goldens (hephaestus' internal/slag refuse in a
// container with no /dies: "failed to gather coverage: exit status 1", measured
// 2026-09-11, hephaestus #53). It judges the change, so its environment hands it
// the base, and it needs the servers the tree's suites name (the union over the
// modules, DEF-B3-046) and the two tools it drives.
//
// GO NEEDS NO critical_modules. The python, rust and ts mutation atoms read that
// declaration off .copier-answers.yml; gomutants scopes to the diff itself.
//
// THE DEADLINE IS THE LANE'S: the mutation Job runs against 75 minutes and ends
// the pod there; the atom's own is the same, so the Job's cause (deadline, which
// the door's cap counts) is the one that is read.
var Def = atomkit.Def{
	ID: "go:mutation", Stage: checks.StageMutation, Lane: checks.LaneGo,
	Group:      checks.GroupLanguage,
	Desc:       "Every mutant gremlins makes of this pull's changed Go is killed by the tests.",
	Surface:    "any:**/go.mod",
	Toolchain:  "go",
	Needs:      []string{"dies", "service:postgres", "service:pgvector", "service:redpanda", "service:redis", "tool:gomutants", "tool:mutation-gate"},
	NotOnForks: checks.ForkChangeBar,
	History:    "full",
	ReasksOwn:  true,
	Timeout:    75 * time.Minute,
	Check:      gomut.Check,
}
