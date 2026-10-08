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
	//
	// SAFE ACROSS REPOS, NOT ACROSS COPIES OF ONE REPO. cargo's package id
	// is hashed relative to the workspace root, so the same crate checked
	// out at two paths shares one artifact name and one dep-info in the
	// target dir; the rust mutation atom, which builds cargo-mutants' copies,
	// therefore drops this mount and its variable (atoms_rust.go,
	// foundry-tools#8869). The gate's own atoms build one tree at one path —
	// but not the SAME tree each time: every commit of a repo mounts at /src
	// and shares that crate's one artifact, so the atoms that build here stamp
	// the tree with Unstale first (rustlane.go), or cargo links whatever tree
	// built it last.
	Key string
	// Seed copies the image's directory at Path into the volume on first
	// creation. False where the image has nothing there (a target dir, a
	// cargo git checkout dir the image never populated).
	Seed bool
	// EnvVar, when set, is exported with Path as its value — for the one
	// cache whose location the toolchain does not derive from anything else
	// (cargo's target dir).
	EnvVar string
	// PerRepo keys the volume by the repository under check (CachesForRepo),
	// and Locked makes the engine serialise the execs that mount it. Both are
	// for the cargo target dir only: it is the one cache whose CONTENTS are a
	// build of a particular tree, so two trees mounting it at once write over
	// each other (foundry-tools#15765: a doctest collected with a feature on
	// and linked against the sibling tree's library built with it off). Per
	// repo, so the lock serialises only trees of one repository, not the fleet's
	// rust lanes; locked, because the engine holds the lock for ONE EXEC at a
	// time, and a cargo build+test is one exec.
	PerRepo bool
	Locked  bool
}

// CachesForRepo is CachesFor with the per-repo mounts keyed to repo (an opaque
// digest, as SourceCacheKey). An empty repo (a local run that names none) keeps
// the shared key, still locked.
func CachesForRepo(image, repo string) []CacheMount {
	mounts := CachesFor(image)
	for i := range mounts {
		if mounts[i].PerRepo && repo != "" {
			mounts[i].Key += "-" + repoDigest(repo)
		}
	}
	return mounts
}

// CachesFor names the caches for a lane image. An image nothing here knows
// (the sweep's kubeconform and kube-linter) mounts none.
func CachesFor(image string) []CacheMount {
	switch image {
	// NOTHING IS SEEDED ANY MORE. The seeds copied a CI image's warm layer
	// (a built stdlib, a uv cache) into the volume on first creation; the
	// upstream toolchains carry no such layer, so a seed would name a
	// directory that does not exist. The volumes still persist across runs —
	// the second gate downloads nothing the first did — they just start
	// empty. The two paths the toolchain does not default to are exported
	// (GOCACHE, UV_CACHE_DIR); cargo and bun find theirs where they always
	// looked.
	case ImageGo:
		return []CacheMount{
			{Path: "/go/pkg/mod", Key: "foundry-go-mod"},
			{Path: "/opt/go-build-cache", Key: "foundry-go-build", EnvVar: "GOCACHE"},
		}
	case ImagePython:
		return []CacheMount{
			{Path: "/opt/uv-cache", Key: "foundry-uv", EnvVar: "UV_CACHE_DIR"},
		}
	case ImageRust:
		return []CacheMount{
			{Path: "/usr/local/cargo/registry", Key: "foundry-cargo-registry"},
			{Path: "/usr/local/cargo/git", Key: "foundry-cargo-git"},
			{Path: "/cache/cargo-target", Key: "foundry-cargo-target", EnvVar: "CARGO_TARGET_DIR", PerRepo: true, Locked: true},
		}
	case ImageTS:
		return []CacheMount{
			{Path: "/root/.bun/install/cache", Key: "foundry-bun"},
		}
	}
	return nil
}
