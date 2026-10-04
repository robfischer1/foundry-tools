package main

import (
	"context"
	"fmt"
	"slices"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE WIT ATOMS. wit:validate grades an interface repo's WIT by running the
// repository's own `just validate`; rust:wit-guest builds the Rust core's
// wasm32 guest and proves it lifts to a valid component. Why both exist is
// internal/checks/witlane.go's header (rob/stellar-core#13761).
//
// BOTH FETCH THEIR TOOLS FROM THE RELEASE URL, PINNED, and extract exactly one
// member of the tarball: no `curl | tar`, no shell (runtime.go rules 5 and 7).
// The extraction is provisioning, so it runs under the default Expect and a
// failure is a could-not-run, never a finding about the tree.

func init() {
	register("wit:validate", witValidate)
	register("rust:wit-guest", rustWitGuest)
}

// withTarballBinary fetches a release tarball and places one member of it at
// /usr/local/bin/<bin>, proved by `<bin> --version` under the default Expect.
// strip is how many leading path components the member carries.
func withTarballBinary(ctx context.Context, ctr *dagger.Container, url, member, bin string, strip int) (*dagger.Container, error) {
	f, err := fetchTool(ctx, url)
	if err != nil {
		return nil, err
	}
	tgz := "/tmp/" + bin + ".tar.gz"
	args := []string{"tar", "xzf", tgz, "-C", "/usr/local/bin"}
	if strip > 0 {
		args = append(args, fmt.Sprintf("--strip-components=%d", strip))
	}
	args = append(args, member)
	return ctr.
		WithFile(tgz, f).
		WithExec(args).
		WithExec([]string{bin, "--version"}), nil
}

// The repository's own `just validate` passes.
//
// ABSENT UNLESS THE TREE TRACKS WIT AND A VALIDATE RECIPE. wit/*.wit says there
// is an interface to resolve; a root justfile defining `validate` says the
// repository has its own answer for what resolving means (for stellar-core,
// `wasm-tools component wit` over every package). A repository that vendors a
// WIT copy and defines no such recipe — stellar-core-rust — is not graded
// twice: its guest build reads that copy at compile time.
//
// THE RECIPE'S OWN EXIT IS THE VERDICT: 0 pass, 1 findings (just passes the
// failing command's code through), anything else could-not-run. The fleet
// image carries bash, which the recipe's shebang names; wasm-tools and just
// are the two pinned fetches.
func witValidate(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("wit:validate")

	files, err := r.population(ctx, "wit/**/*.wit", "justfile")
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasWitFiles(files) {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - this repository tracks no wit/*.wit, so it declares no interface for wasm-tools to resolve")
	}
	if !slices.Contains(files, "justfile") {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - the repository tracks WIT but no root justfile, so it declares no validate recipe to run")
	}
	text, err := r.src.File("justfile").Contents(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the justfile would not read: %v", a.ID, err))
	}
	if !checks.HasJustValidate(text) {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - the justfile defines no validate recipe, so the repository declares no check of its WIT for the gate to run")
	}

	ctr, err := withTarballBinary(ctx, r.lane(checks.ImageFleet), checks.WasmToolsURL, checks.WasmToolsMember, "wasm-tools", 1)
	if err == nil {
		ctr, err = withTarballBinary(ctx, ctr, checks.JustURL, checks.JustMember, "just", 0)
	}
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - %v. WIT that was never resolved is not WIT that passed.", a.ID, err))
	}
	return verdict(ctx, a, ctr.WithExec([]string{"just", "validate"}, anyExit))
}

// The crate's wit-guest feature builds for wasm32-unknown-unknown, the module
// lifts to a component, and the component validates.
//
// ABSENT WHERE THE MANIFEST DECLARES NO wit-guest FEATURE, decided in Go off
// Cargo.toml before any container runs. The default-features test run never
// compiles the guest's WIT export, so a guest that stopped building — or whose
// WIT drifted from the copy it reads — read green everywhere; this is the one
// atom that compiles it.
//
// FOUR STEPS, EACH ITS OWN VERDICT: the build (cargo's exit, translated by
// cargoVerdict), `wasm-tools component new` (the module carries the component
// type wit-bindgen embedded, so a refusal here is a guest that is not the
// interface it claims), `wasm-tools validate` (the component is well formed),
// and `wasm-tools component wit` (the world can be read back; its text is the
// atom's result). The build writes to its own target directory, outside the
// lane's shared cargo-target volume, so the artifact's path is a fact this atom
// owns rather than a guess about a volume's contents.
func rustWitGuest(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("rust:wit-guest")

	manifest, err := r.src.File("Cargo.toml").Contents(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - Cargo.toml would not read: %v", a.ID, err))
	}
	if !checks.HasCargoFeature(manifest, checks.WitGuestFeature) {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - Cargo.toml declares no "+checks.WitGuestFeature+" feature, so this crate exports no WIT guest to build")
	}
	artifact := checks.WitGuestArtifact(manifest)
	if artifact == "" {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - Cargo.toml declares the "+checks.WitGuestFeature+" feature but names no [package] or [lib] name, so the guest's file name is unknown")
	}

	base := r.cargoDeps().WithExec([]string{"rustup", "target", "add", checks.WitGuestTarget})
	base, err = withTarballBinary(ctx, base, checks.WasmToolsURL, checks.WasmToolsMember, "wasm-tools", 1)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - %v. A guest that was never validated is not a guest that passed.", a.ID, err))
	}

	built := base.WithExec([]string{
		"cargo", "build", "--locked", "--release",
		"--no-default-features", "--features", checks.WitGuestFeature,
		"--target", checks.WitGuestTarget, "--target-dir", checks.WitGuestTargetDir,
	}, anyExit)
	if v := cargoVerdict(ctx, a, built); v.State != int(checks.StatePass) {
		return v
	}

	module := checks.WitGuestTargetDir + "/" + checks.WitGuestTarget + "/release/" + artifact
	const component = "/tmp/wit-guest.component.wasm"
	ctr := built
	var v checks.Verdict
	for _, step := range [][]string{
		{"wasm-tools", "component", "new", module, "-o", component},
		{"wasm-tools", "validate", component},
		{"wasm-tools", "component", "wit", component},
	} {
		ctr = ctr.WithExec(step, anyExit)
		if v = verdict(ctx, a, ctr); v.State != int(checks.StatePass) {
			return v
		}
	}
	return v
}
