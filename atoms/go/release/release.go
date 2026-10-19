// Package gorelease is the atom go:release: the release binaries the image will
// carry compile, with the image's own flags. The compile is the build lane's
// too, so its logic is internal/releasekit's and both runners call it.
package gorelease

import (
	"context"
	"os"
	"strings"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/releasekit"
)

// Def is the atom. It names the star by its answers file or its record in
// foundry-dies, so it needs the dies. It writes its binaries to the release
// directory (releasekit.OutEnv) and leaves the tree as it found it.
var Def = atomkit.Def{
	ID: "go:release", Stage: checks.StagePrepush, Lane: checks.LaneGo,
	Group:     checks.GroupLanguage,
	Desc:      "The release binaries the image will carry compile, with the image's own flags — for a Dockerfile that copies from release/; one that compiles itself is built by the build lane, and this is absent.",
	Surface:   "any:**/go.mod",
	Toolchain: "go",
	Needs:     []string{"dies"},
	Timeout:   45 * time.Minute,
	Check:     check,
}

const id = "go:release"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneGo}

// check compiles what the star's Dockerfile copies out of release/.
//
// THE RELEASE BUILD IS THE ONE THE IMAGE CARRIES. Rob's four stages: "Build -
// Copy the binary from the previous complex run (if green, which is a
// prerequisite for getting here)". What it builds is derived, not declared
// (checks/release.go carries the measurement): the fleet's 29 Go Dockerfiles
// have one flag set, always ./cmd/<X>, -mod=vendor exactly where vendor/ is.
//
// ROOT MODULE ONLY. The convention is the STAR's binary and a star is one
// module.
//
// A VENDORED MODULE IS PROVISIONED BY ITS vendor/, AND NOTHING IS DOWNLOADED.
// `go mod download` fills the module cache from the proxy whether or not
// vendor/ exists, and -mod=vendor then ignores the cache — so for a vendored
// module the download is pure network reach, and for ourea it is the reach the
// vendoring exists to remove: a release build that downloads needs a running
// ourea to build ourea.
//
// THE EXIT IS READ AFTER EACH BINARY and the first failure stops the chain, so
// the verdict names the binary that did not compile.
//
// -buildvcs=false: a pod's checkout is a repository, and go build stamps
// vcs.revision from one by default; the engine's release tree never had one.
// The binaries the executor carries into an image must not differ by commit.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	tree := releasekit.DiskOf(in)
	sub, stood := releasekit.Resolve(ctx, tree, row, in.Origin, releasekit.Compiles)
	if stood != nil {
		return *stood
	}
	plan, why := releasekit.GoPlan(ctx, tree, sub)
	if why != "" {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - "+why)
	}
	if err := releasekit.GoRoot(ctx, tree); err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - "+err.Error())
	}
	out := releasekit.OutDir()
	if err := os.MkdirAll(out, 0o755); err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the release directory could not be made: "+err.Error())
	}
	if !plan.Vendored {
		if v, ok := atoms.Provision(ctx, in, row, in.Root, "go", "mod", "download"); !ok {
			return v
		}
	}
	goflags := "GOFLAGS=" + strings.TrimSpace(os.Getenv("GOFLAGS")+" -buildvcs=false")
	v := checks.VerdictOf(row, int(checks.StatePass), "")
	for _, b := range plan.Binaries {
		argv := checks.GoReleaseArgsTo(out, b, plan.Vendored, "", "")
		env := []string{"CGO_ENABLED=" + checks.ReleaseCgo(b), goflags}
		o, code, bad := atoms.RunTool(ctx, in, row, atoms.Cmd{Dir: in.Root, Name: argv[0], Args: argv[1:], Env: env, Both: true})
		if bad != nil {
			return *bad
		}
		if v = checks.VerdictOf(row, code, o); v.State != int(checks.StatePass) {
			break
		}
	}
	return releasekit.Scoped(plan, v)
}
