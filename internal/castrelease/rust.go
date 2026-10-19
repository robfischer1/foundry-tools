package castrelease

import (
	"context"
	"os"
	"path/filepath"
	"slices"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/castlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/copyout"
	"dagger/foundry-tools/internal/releasekit"
)

// Rust is rust:cast-release: the binaries are the bin targets cargo metadata
// names on the workspace's default members, and ONE `cargo build --release
// --locked` over the workspace builds them (cargoRelease in cast.go), read back
// out of the target and carried into the release directory.
//
// THE TARGET DIRECTORY IS THE CALLER'S: CARGO_TARGET_DIR when set (the cast
// pod's, which the executor restores from the repo's archive), else one in the
// atom's work directory. Each binary is cleared from it before the build, so
// what is carried is this tree's whatever the target held.
//
// UNDER THE UNSTALE STAMP, as the Dagger lane is: every tree of the repo is at
// one path, so without it cargo would call the workspace's crates Fresh and link
// what an older commit built.
//
// No RUSTFLAGS and no path remapping, on either runner: the toolchain is the
// same image (tools.toml's rust is checks.ImageRust), and CARGO_HOME is
// /cache/cargo-home on both, so the registry crates' paths embedded in a
// binary agree.
func Rust(ctx context.Context, in atoms.Input, a checks.AtomDef) checks.Verdict {
	if _, stood := decide(ctx, releasekit.DiskOf(in), a, castlane.RustToolchain); stood != nil {
		return *stood
	}
	out := releasekit.OutDir()
	if err := os.MkdirAll(out, 0o755); err != nil {
		return *cannot(a, "could not run: the release directory could not be made: "+err.Error())
	}
	if v, ok := atoms.RustStamp(ctx, in, a); !ok {
		return v
	}
	target := os.Getenv("CARGO_TARGET_DIR")
	if target == "" {
		target = filepath.Join(atoms.WorkDir(), "cargo-target")
	}
	env := slices.Concat(checks.CargoBoundedEnv(), []string{"CARGO_TARGET_DIR=" + target})

	// --no-deps and --locked: the workspace's own members only, against the
	// committed lock. Stdout alone is the JSON; stderr rides in only on a failure.
	meta, code := atoms.CargoExec(ctx, in, env, "cargo", "metadata", "--no-deps", "--format-version", "1", "--locked")
	meta, bad := ran(a, "cargo metadata", "cargo metadata", meta, code)
	if bad != nil {
		return *bad
	}
	binaries, err := castlane.BinariesFromCargoMetadata(meta)
	if err != nil {
		return *findings(a, "findings: "+err.Error())
	}
	var pairs []copyout.Pair
	for _, b := range binaries {
		pairs = append(pairs, copyout.Pair{Src: checks.RustReleaseBuiltIn(target, checks.ReleaseBinary{Name: b}), Dst: filepath.Join(out, b)})
	}
	if err := copyout.Clear(pairs); err != nil {
		return checks.VerdictOf(a, copyout.CopyFailed, err.Error())
	}
	o, code := atoms.CargoExec(ctx, in, env, "cargo", "build", "--release", "--locked")
	if _, bad := ran(a, "cargo build", "the release build", o, code); bad != nil {
		return *bad
	}
	if err := copyout.Carry(pairs); err != nil {
		return checks.VerdictOf(a, copyout.CopyFailed, err.Error())
	}
	if bad := carried(a, out, binaries); bad != nil {
		return *bad
	}
	return built(a, "cargo build --release --locked over the workspace", binaries)
}
