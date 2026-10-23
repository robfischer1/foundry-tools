// Package gotestrace is the atom go:test-race: go test -race ./... passes
// against the live databases the tree's own tests ask for.
package gotestrace

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. NeedsDies: hephaestus' internal/slag goldens grade every
// record committed in foundry-dies. The servers are the environment's: the
// adapter binds the ones the tree's tags name (atom_env.go), and the pod binds
// them as sidecars.
var Def = atomkit.Def{
	ID: "go:test-race", Stage: checks.StagePrepush, Lane: checks.LaneGo,
	Group:     checks.GroupLanguage,
	Desc:      "go test -race ./... passes against the live databases the record declares.",
	Surface:   "any:**/go.mod",
	Toolchain: "go",
	Needs:     append(append(append([]string{"dies", "service:postgres", "service:pgvector", "service:redpanda", "service:redis", "service:etcd", "service:kube-apiserver", "service:timescale"}, checks.GravityNeeds()...), checks.WitSnapshotNeeds...), checks.SuiteJqNeeds...),
	Timeout:   45 * time.Minute,
	Check:     check,
}

// check runs the race suite in every module the tree carries.
//
// NO TESTS IS A FINDING. `go test ./...` prints "[no test files]" per package
// and exits 0, so a module with no test anywhere read green. Rob, 2026-09-11:
// nothing is built without tests.
//
// THE FLEET RECORD TREE RIDES ALONG. hephaestus' internal/slag goldens grade
// every record committed in foundry-dies; a lane checks out one repo, so
// without the mount they resolved nothing and SKIPPED, which `go test` prints
// as ok (#2453, #8118).
//
// AND THE TREE'S DATABASES RIDE ALONG. A star whose tests sit behind a database
// build tag gets the fleet's test servers bound beside the lane and those
// suites compiled; `-p 1` with them, because every package shares the one
// database and the suites reset its schema per test.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	return atoms.EachGoModule(in, atoms.GoRaceRow, func(dir string) checks.Verdict {
		return atoms.GoSuite(ctx, in, atoms.GoRaceRow, dir, true)
	})
}
