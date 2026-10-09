package checks

// CacheMount is one toolchain cache an atom's container mounts: a Dagger cache
// volume, persisted on the engine across runs, at the path the image's
// toolchain already writes to.
//
// THE PATHS ARE THE UPSTREAM TOOLCHAINS' OWN. The fleet's go-ci, python-ci,
// rust-ci and frontend-ci images are retired; each lane is the upstream image
// (images.go) plus the layers provision() installs, and these volumes sit at
// the directories its toolchain already writes to:
//
//	go      GOPATH=/go (module cache at /go/pkg/mod); GOCACHE is exported
//	python  UV_CACHE_DIR is exported
//	rust    CARGO_HOME=/usr/local/cargo (registry/ and git/); CARGO_TARGET_DIR
//	        is exported
//	ts      bun's default /root/.bun/install/cache
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
	// PerRepo keys the volume by the repository under check (CachesForRepo).
	// Cargo target dir only. It stays SHARED (default sharing) within a repo:
	// the atoms of ONE gate run build into it concurrently and must share
	// artifacts. LOCKED parked them silently and PRIVATE gave each a cold
	// instance; either way the gate's wit-guest step went silent past the 5m
	// watchdog (foundry-tools#308/#309, stellar-core-rust runs 499, cffc2e0).
	// Concurrent TREES of one repo are NOT isolated by this: that needs a
	// tree-keyed volume (cold per commit) and is left open on #15765.
	PerRepo bool
}

// CachesForRepo is CachesFor with the per-repo mounts keyed to repo (an opaque
// digest, as SourceCacheKey). An empty repo (a local run that names none) keeps
// the shared key, shared.
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
			{Path: "/cache/cargo-target", Key: "foundry-cargo-target", EnvVar: "CARGO_TARGET_DIR", PerRepo: true},
		}
	case ImageTS:
		return []CacheMount{
			{Path: "/root/.bun/install/cache", Key: "foundry-bun"},
		}
	}
	return nil
}

// ReleaseCachePath is where every Rust RELEASE build keeps its target
// directory — the cast, rust:release, the wit guest and the replay host — on a
// cache volume of its own, per repository (ReleaseCacheFor).
//
// WHY A VOLUME. These builds wrote to the container's filesystem, so each one
// compiled its whole dependency graph from nothing: MEASURED 2026-10-09, a
// cerberus cast built 234 crates with 0 Fresh (470-560 CPU-s, ~8 casts a day),
// tron's rust:release 357-391 CPU-s every gate, stellar-core-rust's wit guest
// and replay host 652 s and 333 s wall at p50.
//
// NOT THE GATE'S foundry-cargo-target. A release profile shares no artifact
// with the debug builds there, and that volume's sharing is tuned for the
// gate's atoms building one tree together (CacheMount.PerRepo).
//
// A FILE IN A VOLUME CANNOT BE READ BACK AS AN OUTPUT, so each build copies
// its outputs out in the same exec (internal/copyout), and every reader reads
// the copy. The workspace's own crates are rebuilt every time under the
// Unstale stamp, exactly as the gate's cached target does; only the registry
// crates are served from the volume.
const ReleaseCachePath = "/cache/cargo-release"

// ReleaseCacheFor is the release target volume for a repository: one key per
// repo (as CachesForRepo keys the gate's target), shared by every release
// build of that repo. An empty repo — a local run that names none — keeps the
// bare key.
//
// MOUNTED PRIVATE (runtime.go withReleaseCache), not SHARED or LOCKED. Two
// builds of one repo can run at once: a cast beside the next pull's gate, or
// rust:release beside rust:wit-guest in one gate. SHARED would let one build
// relink a binary between another's cargo exit and its copy, and let a stamp
// from one tree meet another's artifacts mid-build. LOCKED parks the second
// build silently behind the first, the shape that tripped the gate's 5m
// silence watchdog when the debug target tried it (foundry-tools#308/#309).
// PRIVATE gives a concurrent build its own instance and never shares one in
// flight. Its cost is the one that made it wrong for the debug target — a
// contended build starts cold — but there the atoms NEEDED each other's
// artifacts, and a cold release build is only what every release build was
// before this volume. The engine keeps the extra instance and hands it to a
// later build, so each warms in turn.
func ReleaseCacheFor(repo string) CacheMount {
	m := CacheMount{Path: ReleaseCachePath, Key: "foundry-cargo-release", PerRepo: true}
	if repo != "" {
		m.Key += "-" + repoDigest(repo)
	}
	return m
}
