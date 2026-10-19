package castrelease

import (
	"context"
	"fmt"
	"slices"

	"dagger/foundry-tools/internal/castlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/releasekit"
)

// facts are what the tree says about itself.
type facts struct {
	vendored bool // the root carries vendor/
}

// decide answers whether atom a (the compile of toolchain mine) has a build to
// make here. The verdict ends an atom that has none: an absence where the
// tree's binaries are the other toolchain's, a finding where the tree names no
// single toolchain, a could-not-run where the tree could not be read. The order
// is cast.go's: the root's entries, then the modules.
//
// A tree that declares both toolchains or neither is refused by the rust atom
// alone, so the refusal is said once whichever of the two the planner keeps.
func decide(ctx context.Context, t releasekit.Tree, a checks.AtomDef, mine string) (facts, *checks.Verdict) {
	entries, err := t.Entries(ctx)
	if err != nil {
		return facts{}, cannot(a, fmt.Sprintf("could not run: the repository root could not be read: %v", err))
	}
	files, err := t.Population(ctx, "**/go.mod")
	if err != nil {
		return facts{}, cannot(a, fmt.Sprintf("could not run: the tree's Go modules could not be enumerated: %v", err))
	}
	rust := checks.DeclaresLane(entries, checks.LaneRust)
	goRoot := slices.Contains(checks.GoModuleDirs(files), ".")
	have, refusal := castlane.Toolchain(rust, goRoot)
	switch {
	case refusal != "" && mine == castlane.RustToolchain:
		return facts{}, findings(a, "findings: "+refusal)
	case refusal != "":
		return facts{}, absent(a, refusal+"; "+rustID+" says so")
	case have != mine:
		return facts{}, absent(a, "the tree's binaries are built with "+have+", so "+have+":cast-release is the cast's compile")
	}
	return facts{vendored: slices.Contains(entries, "vendor/")}, nil
}

// rustID is the atom that reports a tree no toolchain can be chosen for.
const rustID = "rust:cast-release"
