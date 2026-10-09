package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

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
	register("rust:wit-compose", rustWitCompose)
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

// Every wit-guest-<world> feature of the crate builds for wasm32-unknown-unknown,
// the module lifts to a component, the component validates, and the world's
// own tests pass natively.
//
// DISCOVERED, NOT NAMED (rob/stellar-core-rust#14205): checks.WitGuestWorlds
// reads the [features] table, so a new world is graded with no lane edit and a
// manifest with none is ABSENT, decided in Go before any container runs. The
// default-features test run never compiles a guest's WIT export, so a guest that
// stopped building - or whose WIT drifted - read green everywhere; this is the
// one atom that compiles each world, and its native `cargo test` is where the
// crossing-type round trips run, because the types exist only under the feature.
//
// FIVE STEPS PER WORLD, EACH ITS OWN VERDICT: the build, `wasm-tools component
// new`, `wasm-tools validate`, `wasm-tools component wit` (read-back), and
// `cargo test --no-default-features --features <world>`. The first failure
// returns, and its reason and first log line NAME THE WORLD, so a red says
// which one. A pass carries every world's lines, each prefixed with its name.
func rustWitGuest(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("rust:wit-guest")

	manifest, err := r.src.File("Cargo.toml").Contents(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - Cargo.toml would not read: %v", a.ID, err))
	}
	worlds := checks.WitGuestWorlds(manifest)
	if len(worlds) == 0 {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - Cargo.toml declares no "+checks.WitGuestPrefix+"<world> feature, so this crate exports no WIT guest to build")
	}
	artifact := checks.WitGuestArtifact(manifest)
	if artifact == "" {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - Cargo.toml declares wit-guest features but names no [package] or [lib] name, so the guest's file name is unknown")
	}

	// UNDER THE UNSTALE STAMP (cargoFresh), for both halves. The guest builds
	// into the repository's release volume, where every tree of the repo
	// leaves its artifacts at one path: unstamped, cargo would call the crate
	// Fresh and hand back a guest an older tree built. The native test builds
	// into the gate's shared foundry-cargo-target and needed the stamp first —
	// WITHOUT IT cargo ran the test binary an older tree had left in the
	// volume: a test module added since (a new file, which the old dep-info
	// never listed) was neither compiled nor run, and a compile_error! under
	// cfg(test) read green. MEASURED on stellar-core-rust#14205: the identity
	// world stayed at 197 tests with 26 round-trip tests in the tree.
	base := r.withReleaseCache(r.cargoFresh()).WithExec([]string{"rustup", "target", "add", checks.WitGuestTarget})
	base, err = withTarballBinary(ctx, base, checks.WasmToolsURL, checks.WasmToolsMember, "wasm-tools", 1)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - %v. A guest that was never validated is not a guest that passed.", a.ID, err))
	}

	inVolume := checks.WitGuestTargetDir + "/" + checks.WitGuestTarget + "/release/" + artifact
	var logs []string
	for _, world := range worlds {
		// The module as this world's build left it, copied out of the volume
		// by the build's own exec: the next world's build overwrites the one in
		// the volume.
		module := "/tmp/" + world + ".wasm"
		component := "/tmp/" + world + ".component.wasm"
		built := base.WithExec(copiedOut([]string{
			"cargo", "build", "--locked", "--release",
			"--no-default-features", "--features", world,
			"--target", checks.WitGuestTarget, "--target-dir", checks.WitGuestTargetDir,
		}, inVolume+"="+module), anyExit)
		v := cargoVerdict(ctx, a, built)
		if v.State != int(checks.StatePass) {
			return namedWorld(v, a, world, "build")
		}
		ctr := built
		for _, step := range []struct {
			name string
			args []string
		}{
			{"component new", []string{"wasm-tools", "component", "new", module, "-o", component}},
			{"validate", []string{"wasm-tools", "validate", component}},
			{"component wit", []string{"wasm-tools", "component", "wit", component}},
		} {
			ctr = ctr.WithExec(step.args, anyExit)
			if v = verdict(ctx, a, ctr); v.State != int(checks.StatePass) {
				return namedWorld(v, a, world, step.name)
			}
		}
		logs = append(logs, prefixLines(world, v.Logs)...)
		// The native test, on base's stamp (see above).
		tested := base.WithExec([]string{
			"cargo", "test", "--locked", "--no-default-features", "--features", world,
		}, anyExit)
		if v = cargoVerdict(ctx, a, tested); v.State != int(checks.StatePass) {
			return namedWorld(v, a, world, "test")
		}
	}
	return checks.VerdictOf(a, 0, strings.Join(logs, "\n"))
}

// namedWorld stamps a failing step's verdict with the world and step that
// produced it, in the reason and as the first log line.
func namedWorld(v checks.Verdict, a checks.AtomDef, world, step string) checks.Verdict {
	v.Reason = fmt.Sprintf("%s [world %s, %s]%s", a.ID, world, step, strings.TrimPrefix(v.Reason, a.ID))
	v.Logs = append([]string{fmt.Sprintf("world %s failed at %s", world, step)}, v.Logs...)
	return v
}

func prefixLines(world string, lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = "[" + world + "] " + l
	}
	return out
}

// Every guest composes into ONE component and every tape the crate carries for
// a world replays through it with the answers the native core gave.
//
// WHY A LANE (stellar-core-rust#15266). rust:wit-guest grades each world alone;
// the fleet ships them composed, and a composition has failure modes no single
// guest has (a world that `use`s another's types, two worlds sharing a package).
// The check was opt-in (COMPOSE_CHECK=1) behind tools the plain test lane does
// not carry, and the replay host lived in a scratch directory, so nothing ran
// either. This atom carries them: the wasm32 target, wasm-tools and wac.
//
// ABSENT UNLESS THE CRATE OWNS ALL THREE PIECES: a wit-guest world, the
// composer (tools/compose/compose.sh) and the replay host
// (tools/replay/Cargo.toml). A repository with a guest and no composer is
// graded by rust:wit-guest alone.
//
// THREE STEPS, EACH ITS OWN VERDICT: build the replay host (its own lockfile,
// its own target dir), compose every guest through the script, and replay the
// tapes named for the discovered worlds (tests/tapes/<world>.json, when the
// tree carries one). A tape the composition answers differently is a finding.
func rustWitCompose(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("rust:wit-compose")

	manifest, err := r.src.File("Cargo.toml").Contents(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - Cargo.toml would not read: %v", a.ID, err))
	}
	if len(checks.WitGuestWorlds(manifest)) == 0 {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - Cargo.toml declares no "+checks.WitGuestPrefix+"<world> feature, so there is nothing to compose")
	}
	files, err := r.population(ctx, checks.WitComposeScript, checks.WitReplayManifest, "tests/tapes/*.json")
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !slices.Contains(files, checks.WitComposeScript) || !slices.Contains(files, checks.WitReplayManifest) {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - the tree carries no "+checks.WitComposeScript+" and "+checks.WitReplayManifest+", so it declares no composition to check")
	}
	tapes := checks.WitComposeTapes(manifest, files)
	if len(tapes) == 0 {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - no tests/tapes/<world>.json for any wit-guest world, so a composition would be built and answer nothing")
	}

	// THE RELEASE VOLUME, UNDER THE STAMP, as rust:wit-guest builds: the
	// script builds every guest into CARGO_TARGET_DIR and reads them in its own
	// exec, and the replay host copies itself out of the volume as it builds.
	base := r.withReleaseCache(r.cargoFresh()).WithExec([]string{"rustup", "target", "add", checks.WitGuestTarget})
	base, err = withTarballBinary(ctx, base, checks.WasmToolsURL, checks.WasmToolsMember, "wasm-tools", 1)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - %v. A composition that was never built is not one that passed.", a.ID, err))
	}
	wac, err := fetchTool(ctx, checks.WacURL)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - %v. A composition that was never built is not one that passed.", a.ID, err))
	}
	base = base.
		WithFile("/usr/local/bin/wac", wac, dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithExec([]string{"wac", "--version"}).
		WithEnvVariable("CARGO_TARGET_DIR", checks.WitGuestTargetDir)

	ctr := base
	for _, step := range []struct {
		name string
		args []string
	}{
		{"replay host", copiedOut([]string{"cargo", "build", "--locked", "--release", "--manifest-path", checks.WitReplayManifest, "--target-dir", checks.WitReplayTargetDir},
			checks.WitReplayTargetDir+"/release/replay="+checks.WitReplayHost)},
		{"compose", []string{checks.WitComposeScript, checks.WitComposedPath}},
		{"replay", append([]string{checks.WitReplayHost, checks.WitComposedPath}, tapes...)},
	} {
		ctr = ctr.WithExec(step.args, anyExit)
		if v := verdict(ctx, a, ctr); v.State != int(checks.StatePass) {
			return namedStep(v, a, step.name)
		}
	}
	return verdict(ctx, a, ctr)
}

// namedStep stamps a failing step's verdict with the step that produced it.
func namedStep(v checks.Verdict, a checks.AtomDef, step string) checks.Verdict {
	v.Reason = fmt.Sprintf("%s [%s]%s", a.ID, step, strings.TrimPrefix(v.Reason, a.ID))
	v.Logs = append([]string{"failed at " + step}, v.Logs...)
	return v
}
