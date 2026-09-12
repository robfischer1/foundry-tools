package checks

// CacheMount is one toolchain cache an atom's container mounts: a Dagger cache
// volume, persisted on the engine across runs, at the path the image's
// toolchain already writes to.
//
// THE PATHS ARE THE IMAGES' OWN, MEASURED 2026-09-12 from each image's config
// blob at registry.notusmi.com (zot serves them anonymously):
//
//	go-ci        GOPATH=/go  GOCACHE=/opt/go-build-cache (warm race stdlib);
//	             /go/pkg/mod does NOT exist in the image (measured 2026-09-12:
//	             "stat /go/pkg/mod: no such file" seeding it) — no seed there
//	python-ci    UV_CACHE_DIR=/opt/uv-cache (warm: ruff/mypy/pytest/pip-audit/cosmic-ray)
//	rust-ci      CARGO_HOME=/usr/local/cargo (registry/ and git/ beside a
//	             config.toml routing crates through Nexus — the config is NOT
//	             covered by a mount, so the route survives)
//	frontend-ci  bun's default /root/.bun/install/cache, warmed
//
// Mounting at the toolchain's own path rather than redirecting it with an
// environment variable means a Seed can copy the image's warm layer into the
// volume the first time it is created (Dagger's `source` option), so the
// volume starts where the image left off instead of empty.
type CacheMount struct {
	// Path is where the volume mounts inside the container.
	Path string
	// Key names the volume on the engine. One key per toolchain, shared by
	// every repo the gate grades — a module cache is content-addressed, a
	// build cache is keyed on its inputs, and cargo's registry and target dir
	// are keyed on package id, so sharing is safe and is the point.
	Key string
	// Seed copies the image's directory at Path into the volume on first
	// creation. False where the image has nothing there (a target dir, a
	// cargo git checkout dir the image never populated).
	Seed bool
	// EnvVar, when set, is exported with Path as its value — for the one
	// cache whose location the toolchain does not derive from anything else
	// (cargo's target dir).
	EnvVar string
}

// CachesFor names the caches for a lane image. An image nothing here knows
// (the sweep's kubeconform and kube-linter) mounts none.
func CachesFor(image string) []CacheMount {
	switch image {
	case ImageGo:
		return []CacheMount{
			{Path: "/go/pkg/mod", Key: "foundry-go-mod"},
			{Path: "/opt/go-build-cache", Key: "foundry-go-build", Seed: true},
		}
	case ImagePython:
		return []CacheMount{
			{Path: "/opt/uv-cache", Key: "foundry-uv", Seed: true},
		}
	case ImageRust:
		return []CacheMount{
			{Path: "/usr/local/cargo/registry", Key: "foundry-cargo-registry", Seed: true},
			{Path: "/usr/local/cargo/git", Key: "foundry-cargo-git"},
			{Path: "/cache/cargo-target", Key: "foundry-cargo-target", EnvVar: "CARGO_TARGET_DIR"},
		}
	case ImageTS:
		return []CacheMount{
			{Path: "/root/.bun/install/cache", Key: "foundry-bun", Seed: true},
		}
	}
	return nil
}
