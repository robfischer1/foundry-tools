package main

import (
	"context"
	"fmt"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE WIT ATOMS' ENVIRONMENT. rust:wit-guest and rust:wit-compose each run in the
// rust lane with the dependency graph fetched, the pinned tools they exec on PATH
// and a release volume of their OWN. The chain they replace gave both one volume
// (the repository's release cache), SHARED, and remounted it LOCKED for the
// guest's native test, so a compose exec waited behind the guest's tests or raced
// its builds, and the two atoms ordered themselves through a mount (foundry-tools
// #308/#309). Neither reads the other's output, so each has a volume keyed by its
// atom and the atoms are independent (DEF-B4-wit-1).
//
// The atoms honour CARGO_TARGET_DIR: here it is the volume's path, and on the
// executor it is the pod's own.

// Each wit atom's release volume, by the suffix on checks.ReleaseCacheFor's key.
const (
	witGuestVolume     = "wit-guest"
	witGuestTestVolume = "wit-guest-test"
	witComposeVolume   = "wit-compose"
)

// witVolumeKey is the cache volume key of one wit atom's volume for this run's
// repository: the release cache's per-repo key, then the atom's name.
func (r *run) witVolumeKey(name string) string {
	return checks.ReleaseCacheFor(r.repo).Key + "-" + name
}

// withWitVolume mounts one of the wit atoms' volumes at path.
func (r *run) withWitVolume(ctr *dagger.Container, name, path string, sharing dagger.CacheSharingMode) *dagger.Container {
	return ctr.WithMountedCache(path, dag.CacheVolume(r.witVolumeKey(name)), dagger.ContainerWithMountedCacheOpts{Sharing: sharing})
}

// witGuestLane is the fetched rust lane with the guest's build volume SHARED (the
// module is carried out in the same atom run) and, nested in it, the native
// test's directory LOCKED: that run builds test binaries there and then
// executes them, and two gates of one repo must take turns on it
// (foundry-tools#16916). The two are separate volumes, so locking the test's
// does not park the build.
func witGuestLane(r *run) *dagger.Container {
	ctr := r.cargoDeps().WithEnvVariable("CARGO_TARGET_DIR", checks.WitGuestTargetDir)
	ctr = r.withWitVolume(ctr, witGuestVolume, checks.WitGuestTargetDir, dagger.CacheSharingModeShared)
	return r.withWitVolume(ctr, witGuestTestVolume, checks.WitGuestTestTargetDir, dagger.CacheSharingModeLocked)
}

// witComposeLane is the fetched rust lane with the composer's own volume SHARED,
// as the chain held its release volume.
func witComposeLane(r *run) *dagger.Container {
	ctr := r.cargoDeps().WithEnvVariable("CARGO_TARGET_DIR", checks.WitGuestTargetDir)
	return r.withWitVolume(ctr, witComposeVolume, checks.WitGuestTargetDir, dagger.CacheSharingModeShared)
}

// witTools puts the pinned programs the wit atoms exec on PATH: wasm-tools for
// both, and wac for the composer. A tool that will not fetch is left off the
// container, and the atom's own version probe settles 2 on it.
func witTools(withWac bool) func(ctx context.Context, r *run, ctr *dagger.Container) *dagger.Container {
	return func(ctx context.Context, _ *run, ctr *dagger.Container) *dagger.Container {
		if tooled, err := withTarballBinary(ctx, ctr, checks.WasmToolsURL, checks.WasmToolsMember, "wasm-tools", 1); err == nil {
			ctr = tooled
		}
		if !withWac {
			return ctr
		}
		if wac, err := fetchPinnedTool(ctx, checks.WacURL, checks.ToolSHA256); err == nil {
			ctr = ctr.WithFile("/usr/local/bin/wac", wac, dagger.ContainerWithFileOpts{Permissions: 0o755})
		}
		return ctr
	}
}

// withTarballBinary fetches a release tarball and places one member of it at
// /usr/local/bin/<bin>, proved by `<bin> --version` under the default Expect.
// strip is how many leading path components the member carries. The extraction
// is provisioning: a failure is a could-not-run, never a finding about the tree.
func withTarballBinary(ctx context.Context, ctr *dagger.Container, url, member, bin string, strip int) (*dagger.Container, error) {
	return withTarballBinaryProbe(ctx, ctr, url, member, bin, strip, []string{bin, "--version"}, "")
}

// withTarballBinaryProbe is withTarballBinary for a program that proves itself
// some other way than `--version` (gofmt has no such flag): probe is its argv and
// stdin what it reads.
func withTarballBinaryProbe(ctx context.Context, ctr *dagger.Container, url, member, bin string, strip int, probe []string, stdin string) (*dagger.Container, error) {
	f, err := fetchPinnedTool(ctx, url, checks.ToolSHA256)
	if err != nil {
		return nil, err
	}
	tgz := "/tmp/" + bin + ".tar.gz"
	args := []string{"tar", "xzf", tgz, "-C", "/usr/local/bin"}
	if strip > 0 {
		args = append(args, fmt.Sprintf("--strip-components=%d", strip))
	}
	args = append(args, member)
	ctr = ctr.WithFile(tgz, f).WithExec(args)
	if stdin != "" {
		return ctr.WithExec(probe, dagger.ContainerWithExecOpts{Stdin: stdin}), nil
	}
	return ctr.WithExec(probe), nil
}
