// Package basesgorelease is the atom bases:go-release: the Go analogue of
// bases:release. A base of a repository of bases whose artifact is a Go binary
// (chairman's chairman-svid) carries bases/<b>/release.toml naming the binary and,
// when go.mod is not in the base's own directory, the module's directory; this
// atom compiles it there with the fleet's release flags and stages the binary at
// <release out>/<b>/, where the base's Dockerfile COPYs it from release/<b>/.
// Only lanes/build.toml names it (stage bases is in no lane's stages); the
// decisions are internal/releasekit's, the compile is go:release's.
package basesgorelease

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/releasekit"
)

// Def is the atom. It builds in the fleet's Go container, as go:release does.
var Def = atomkit.Def{
	ID: "bases:go-release", Stage: checks.StageBases, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Each base that names its Go module and binaries in a release.toml and copies them from release/<base>/ is compiled with the fleet's release flags, and the binaries are staged there.",
	Surface:   "any:bases/*/release.toml",
	Toolchain: "go",
	Timeout:   45 * time.Minute,
	Check:     check,
}

const id = "bases:go-release"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StageBases, Lane: checks.LaneAny}

// check compiles, for each base with a Go release root, what its release.toml
// declares, in the module's directory and with the flags go:release uses
// (-trimpath, -ldflags="-s -w", CGO_ENABLED=0 unless the manifest says cgo,
// -buildvcs=false so the bytes do not differ by commit, -mod=vendor exactly where
// the module vendors; nothing is downloaded for a vendored module). The first
// failure stops the chain and names the base and the binary. The module's tests
// belong to the gate, not to the release.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	tree := releasekit.DiskOf(in)
	roots, refused, err := releasekit.BaseGoRoots(ctx, tree)
	switch {
	case err != nil:
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the tree could not be read: "+err.Error())
	case len(refused) > 0:
		return checks.VerdictOf(row, int(checks.StateFindings), id+": FINDINGS - "+strings.Join(refused, "; "))
	case len(roots) == 0:
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - no base carries a release.toml beside a Dockerfile that copies from "+buildlane.ReleaseDir+"/<base>/, so no base has a Go release of its own")
	}
	goflags := "GOFLAGS=" + strings.TrimSpace(os.Getenv("GOFLAGS")+" -buildvcs=false")
	var made []string
	var last checks.Verdict
	for _, root := range roots {
		out := filepath.Join(releasekit.OutDir(), root.Name)
		if err := os.MkdirAll(out, 0o755); err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the release directory could not be made: "+err.Error())
		}
		dir := filepath.Join(in.Root, filepath.FromSlash(root.Module))
		if !root.Vendored {
			if v, ok := atoms.Provision(ctx, in, row, dir, "go", "mod", "download"); !ok {
				v.Reason = root.Name + ": " + v.Reason
				return v
			}
		}
		module, version, fault := releasekit.Stamp(ctx, in, dir)
		if fault != "" {
			return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - "+root.Name+": "+fault)
		}
		last = checks.VerdictOf(row, int(checks.StatePass), "")
		for _, b := range root.Binaries {
			argv := checks.GoReleaseArgsTo(out, b, root.Vendored, module, version)
			env := []string{"CGO_ENABLED=" + checks.ReleaseCgo(b), goflags}
			o, code, bad := atoms.RunTool(ctx, in, row, atoms.Cmd{Dir: dir, Name: argv[0], Args: argv[1:], Env: env, Both: true})
			if bad != nil {
				return *bad
			}
			if last = checks.VerdictOf(row, code, o); last.State != int(checks.StatePass) {
				last.Reason = fmt.Sprintf("%s: %s: %s", root.Name, b.Name, last.Reason)
				return last
			}
			made = append(made, fmt.Sprintf("%s/%s/%s", buildlane.ReleaseDir, root.Name, b.Name))
		}
	}
	last.Reason = fmt.Sprintf("release build: %d base(s) compiled; %s\n%s", len(roots), strings.Join(made, ", "), last.Reason)
	return last
}
