// Package rustrelease is the atom rust:release: the release binaries the image
// will carry compile under cargo's release profile. The compile is the build
// lane's too, so its logic is internal/releasekit's and both runners call it.
package rustrelease

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/copyout"
	"dagger/foundry-tools/internal/releasekit"
)

// Def is the atom. It dates the tree (atoms.RustStamp), which a shared tree
// would carry to every other atom, so it says it writes it. It names the star
// by its answers file or its record in foundry-dies, so it needs the dies.
var Def = atomkit.Def{
	ID: "rust:release", Stage: checks.StagePrepush, Lane: checks.LaneRust,
	Group:      checks.GroupLanguage,
	Desc:       "The release binaries the image will carry compile under cargo's release profile — for a Dockerfile that copies from release/; one that compiles itself is built by the build lane, and this is absent.",
	Surface:    "root:Cargo.toml",
	Toolchain:  "rust",
	Needs:      []string{"dies", "tool:python-standalone"},
	Timeout:    45 * time.Minute,
	WritesTree: true,
	Check:      check,
}

const id = "rust:release"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneRust}

// check compiles what the star's Dockerfile copies out of release/.
//
// THE RELEASE BUILD IS THE ONE THE IMAGE CARRIES — go:release's contract for
// the Rust lane. What it builds is derived, not declared: `cargo build
// --release --locked -p <star>` from a workspace whose binary crate is named for
// the star, which is the one shape the fleet's Rust Dockerfiles and
// rust-repo-template pour (checks.RustReleasePlan carries the measurement).
//
// THE TARGET DIRECTORY IS THE CALLER'S: CARGO_TARGET_DIR when set (the pod's
// own, or the engine's release volume), else one in the atom's work directory.
// Each binary is cleared from it before the build and carried out of it after,
// so what reaches the release directory is this tree's, whatever the target
// held. The atom takes no lock and mounts nothing.
//
// THE EXIT IS READ AFTER EACH BINARY, in cargo's vocabulary (101 is findings,
// not could-not-run), so a binary that did not compile is reported with the
// compiler's own words rather than by the one after it.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	tree := releasekit.DiskOf(in)
	sub, stood := releasekit.Resolve(ctx, tree, row, in.Origin, releasekit.Compiles)
	if stood != nil {
		return *stood
	}
	plan, why := releasekit.RustPlan(sub)
	if why != "" {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - "+why)
	}
	target := os.Getenv("CARGO_TARGET_DIR")
	if target == "" {
		target = filepath.Join(atoms.WorkDir(), "cargo-target")
	}
	out := releasekit.OutDir()
	if err := os.MkdirAll(out, 0o755); err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - the release directory could not be made: "+err.Error())
	}
	if v, ok := atoms.RustStamp(ctx, in, row); !ok {
		return v
	}
	env := slices.Concat(checks.CargoBoundedEnv(), []string{"CARGO_TARGET_DIR=" + target})
	v := checks.VerdictOf(row, int(checks.StatePass), "")
	for _, b := range plan.Binaries {
		pair := []copyout.Pair{{Src: checks.RustReleaseBuiltIn(target, b), Dst: filepath.Join(out, b.Name)}}
		if err := copyout.Clear(pair); err != nil {
			return checks.VerdictOf(row, copyout.CopyFailed, err.Error())
		}
		o, code := atoms.CargoExec(ctx, in, env, checks.RustReleaseArgs(b)...)
		if v = atoms.CargoVerdict(row, o, code); v.State != int(checks.StatePass) {
			break
		}
		if err := copyout.Carry(pair); err != nil {
			v = checks.VerdictOf(row, copyout.CopyFailed, err.Error())
			break
		}
	}
	return releasekit.Scoped(plan, v)
}
