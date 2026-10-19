package castrelease

import (
	"context"
	"os"
	"strings"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/castlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/releasekit"
)

// Go is go:cast-release: the binaries are every main package under cmd/ (`go
// list -find`, which resolves no dependencies), one exec each from ./cmd/<name>
// with the flag set every Go image in the fleet is built with
// (checks.ReleaseFlags) and -mod=vendor exactly when the module vendors, with
// CGO_ENABLED=0 throughout (goRelease in cast.go).
//
// THE EXIT IS READ AFTER EACH BINARY and the first failure stops the chain, so
// a first binary that failed to compile is reported with the compiler's own
// words and not by a second that did not.
//
// -buildvcs=false, as go:release has it: a pod's checkout is a repository and
// go build stamps vcs.revision from one. The Dagger cast's tree carries the
// commit's .git as well (source.go sourceAt), so its binaries are probably
// stamped; the executor's are not, so that stamp is the one place the two
// runners' bytes cannot agree by construction (-trimpath removes every path).
func Go(ctx context.Context, in atoms.Input, a checks.AtomDef) checks.Verdict {
	have, stood := decide(ctx, releasekit.DiskOf(in), a, castlane.GoToolchain)
	if stood != nil {
		return *stood
	}
	out := releasekit.OutDir()
	if err := os.MkdirAll(out, 0o755); err != nil {
		return *cannot(a, "could not run: the release directory could not be made: "+err.Error())
	}
	if !have.vendored {
		if v, ok := atoms.Provision(ctx, in, a, in.Root, "go", "mod", "download"); !ok {
			return v
		}
	}
	cgo := "CGO_ENABLED=0"
	listed, code := atoms.RunCmd(ctx, in, atoms.Cmd{Dir: in.Root, Name: "go", Args: []string{"list", "-find", "-f", "{{.Name}} {{.ImportPath}}", "./cmd/..."}, Env: []string{cgo}})
	listed, bad := ran(a, "go list ./cmd/...", "go list", listed, code)
	if bad != nil {
		return *bad
	}
	binaries, err := castlane.BinariesFromGoList(listed)
	if err != nil {
		return *findings(a, "findings: "+err.Error())
	}
	goflags := "GOFLAGS=" + strings.TrimSpace(os.Getenv("GOFLAGS")+" -buildvcs=false")
	for _, name := range binaries {
		b := checks.ReleaseBinary{Name: name, Package: "./cmd/" + name}
		argv := checks.GoReleaseArgsTo(out, b, have.vendored, "", "")
		o, code := atoms.RunCmd(ctx, in, atoms.Cmd{Dir: in.Root, Name: argv[0], Args: argv[1:], Env: []string{cgo, goflags}})
		if _, bad := ran(a, "go build "+b.Package, "the release build of "+b.Package, o, code); bad != nil {
			return *bad
		}
	}
	if bad := carried(a, out, binaries); bad != nil {
		return *bad
	}
	return built(a, "go build with the fleet's flags, one per binary under cmd/", binaries)
}
