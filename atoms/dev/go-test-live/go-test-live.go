// Package devgotestlive is the atom dev:go-test-live: the push's race run, asked
// for by hand from the dev lane.
package devgotestlive

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. IT RUNS WHAT go:test-race RUNS (atoms.GoSuiteFor): the tags
// the tree's own `//go:build` lines name in the fleet's vocabulary, -race, -p 1,
// against the sidecars the pod binds for the Needs below — the SAME Needs as the
// race atom's, because the pod binds a server per need and the suites read the
// DSN it exports (TEST_DATABASE_URL for service:pgvector, TEST_NOVECTOR_DATABASE_URL
// for service:postgres, KAFKA_BOOTSTRAP for service:redpanda).
//
// It is its own verb and not dev:go-test's default because six sidecars are
// minutes of pod start-up that a unit run must not pay.
//
// THE CALLER'S ARGUMENTS fold into the computed run (atoms.ParseSuiteArgs):
// package patterns replace ./..., -run, -count, -v and the rest pass through, a
// caller's -tags is unioned with the tree's and -race is implied.
var Def = atomkit.Def{
	ID: id, Stage: "dev", Lane: checks.LaneGo,
	Group:     checks.GroupLanguage,
	Desc:      "go test -race against the live servers the tree's build tags name, run by hand from the dev lane.",
	Surface:   "root:go.mod",
	Toolchain: "go",
	Needs:     append([]string{"dies", "service:postgres", "service:pgvector", "service:redpanda", "service:redis", "service:etcd", "service:kube-apiserver"}, checks.GravityNeeds()...),
	Timeout:   45 * time.Minute,
	Check:     check,
}

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: "dev", Lane: checks.LaneGo}

const id = "dev:go-test-live"

// check runs the suite in every module the tree carries; a caller who names
// packages is naming them in the root module, the one `go test ./...` would run
// from, so only it runs.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	caller := atoms.ParseSuiteArgs(in.Args)
	if caller.Packages {
		return atoms.GoSuiteFor(ctx, in, row, ".", caller)
	}
	return atoms.EachGoModule(in, row, func(dir string) checks.Verdict {
		return atoms.GoSuiteFor(ctx, in, row, dir, caller)
	})
}
