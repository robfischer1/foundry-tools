package checks

// The image map — the toolchain lives in the module, not on a laptop.
//
// ONE PLACE, deliberately. Every lane image and every tool pin is named here
// so digest pinning is a single edit rather than a sweep: the fleet has been
// broken twice by a floating base.
//
// THE LANES RUN ON UPSTREAM TOOLCHAINS, SINCE 2026-09-12. Rob: "We're going
// to stop maintaining CI images. We'll leverage dagger's caching instead.
// Maintaining our own was a mistake." Until then the four lanes ran in the
// fleet's own go-ci / python-ci / rust-ci / frontend-ci images — bases that
// baked every tool an atom execs, rebuilt weekly, reaped by digest three
// times in nine days (each time a fleet-wide red nobody saw until a landing).
// Now each lane is the upstream toolchain image from the mirror, by digest,
// and runtime.go's lane() installs what the atoms exec IN THE CHAIN, one
// exec per tool, every version pinned below. The engine caches each exec by
// its inputs, so a tool is fetched or compiled once per pin and every later
// gate starts from the cached layer — the same property the baked image had,
// without a second artifact to rebuild, reap and repin.
//
// LAYERED BY VOLATILITY, in Rob's words: "layer the new images to optimize
// for caching, and order the layers by volatility. Upstream base, least
// volatile tools in the next layer, most volatile tools last." So: from(the
// toolchain) → the distro packages the image lacks → the stable binaries
// (uv, node) → the pinned scanner (opengrep) → the tools built from
// source at a version that moves most (staticcheck, govulncheck, gomutants;
// cargo-audit, cargo-mutants). A cache hit survives everything but the last
// layer moving.
//
// WHAT EACH LANE EXECS, measured at the exec sites (atoms_*.go):
//
//	go       go, staticcheck, govulncheck, gomutants, git
//	python   uv, uvx, python3, prlimit, opengrep, git, tar, bash; opa (dies)
//	         the atoms fetch themselves, pinned below
//	rust     cargo (+ rustfmt, clippy, audit, mutants), git
//	ts       bun, node (stryker runs under it), git, curl, find
//
// BY DIGEST, NEVER BY TAG. Every image here is a moving tag upstream; a gate
// whose image floats is a gate whose verdict is not a function of the pin
// the door declared, and the pin is the whole of F7's join.
//
// BY THE REGISTRY'S OWN NAME, MIRRORED BY THE ENGINE. Until 2026-09-19 every
// image here named docker.notusmi.com — Nexus's docker group, a flat
// namespace that merged Hub, ghcr and gcr without saying which — because the
// image string was the only way through a mirror. It is not any more: the
// engine's registries.mirrors (infra flux/apps/dagger-engine.yaml, master-plan
// "Transparent Cache — Nexus Retired" F5) sends docker.io, ghcr.io and gcr.io
// to hub./ghcr./gcr.notusmi.com — three Distribution pull-through caches
// (registry-proxy.yaml) — with the upstream as buildkit's
// fallback, so `docker.io/library/python@sha256:…` IS the mirrored pull, and
// says where the image lives. MirroredRegistries below is that set, and the
// tests hold every image to it; an image from anywhere else (quay.io, a
// vendor registry) is a pull the fleet does not mirror, and the test says so.
// The digests are content-addressed and did not move with the names.
const (
	ImageGo     = "docker.io/library/golang:1.26.6-bookworm@sha256:116d58cbd88c1297624acc6e967a060012422bacf9930927e23fb719189c6f36"
	ImagePython = "docker.io/library/python:3.14-slim-bookworm@sha256:9ab8d9c8514b44f90cf0029dd42fdd7e9e211e639c8b995304cc04568dee900f"
	ImageRust   = "docker.io/library/rust:1.97.0-bookworm@sha256:8fa55b2f3ddf97471ab6a767bfa3f37e6bad0986ba823e75fea57e2a2a5c3073"
	ImageTS     = "docker.io/oven/bun:1.4-slim@sha256:cb3bbbb08e13a4a2ff400f24c7a2a1d5efa83f6ef8544d52d95a519631e2fc61"
	// The fleet atoms run in the python lane: they are python and shell, and
	// three of them provision with uvx.
	ImageFleet = ImagePython
)

// The tool images a lane copies ONE binary out of. Both are scratch upstream
// (no shell), which is why the binary is taken by File and never by exec.
const (
	// ImageUV carries /uv and /uvx — the python lane's whole package
	// manager, and the version the fleet's uv.lock files were written under.
	ImageUV = "ghcr.io/astral-sh/uv:0.12.13@sha256:b485bd65cc2cf1c9a93b3554012c9c3778cf7b1b5fd3d3096ce9e1226c97e1e6"

	// FleetPython is the interpreter version the fleet standardised on (Rob,
	// 2026-09-25). It is not decoration: `target-version = "py314"` in the ruleset
	// makes `ruff format` emit PEP 758 — `except A, B:` with no parentheses — which
	// is valid 3.14 and a SyntaxError on anything older. Any lane that EXECUTES the
	// fleet's python has to carry this, and the rust lane does: cerberus'
	// memory_plugin.rs spawns .cerberus/hooks/*.py from `cargo test`, so
	// rust:bookworm's own python3 (3.11.2, measured 2026-09-25) parsed five of
	// those tests into a SyntaxError the moment the formatter had been near them.
	//
	// NO STOCK RUST IMAGE CARRIES IT — bookworm is 3.11, trixie 3.13, slim-trixie
	// has none at all (all three measured). So the lane provisions it with uv, the
	// same tool the python lane already uses, rather than waiting on a base image.
	FleetPython = "3.14"
	// ImageNode carries the node ts:mutation runs stryker under
	// (`node_modules/.bin/stryker` under node, never `bunx --bun` — atoms_ts.go
	// says why). The bun image ships no node.
	ImageNode = "docker.io/library/node:24-bookworm-slim@sha256:2fe369e969550cde8e867afc3fe370b260140cab4a23d467074295b42163d553"

	// THE TEST DATABASES a Go star's DB-gated suites run against (testdb.go):
	// pgvector's own image for `live_db`, and library/postgres at the same
	// major — no extension installed — for `live_db_novector`. Bound as
	// Dagger services beside the lane container, never pulled by a script.
	// Digests resolved 2026-09-13 (through the Nexus alias, byte-identical).
	ImagePgvector = "docker.io/pgvector/pgvector:pg18@sha256:1d50c689b0a6511b9ea0a15615281c81a59fd04a08eb35057ec8646fb3a2118a"
	ImagePostgres = "docker.io/library/postgres:18-bookworm@sha256:a10c981235b4f635e65df0cfb66a5598064628128505dbc6a3ed4ca303717521"

	// THE TEST BROKER a Go star's broker-gated suites run against (testdb.go's
	// TestBrokers, tag `live_kafka`). Redpanda, NOT cp-kafka: the fleet's broker
	// is Redpanda, and a fixture standing for a different implementation pins
	// conformance to the wrong substrate. This is the SAME digest
	// forge-testkit-go's containers.Kafka fixture pulls, so a suite moving off
	// that fixture and onto the lane's service does not also change broker build.
	ImageRedpanda = "docker.notusmi.com/redpandadata/redpanda@sha256:6d627e3ebdc05438d1e8e3b55695ac1a7a0f79e7d5ca90b3b0f12dc984633e83"

	// THE BUILD LANE'S TOOLS, run by their own entrypoints: cosign signs the
	// published image and attests its SBOM, syft reads that SBOM, and the
	// empty static base runs the lane's own two binaries (verdict, hadescall).
	// cosign is ghcr's; the static base is gcr's. Digests resolved 2026-09-14
	// (through the Nexus alias, byte-identical).
	//
	// COSIGN IS V3, THE FLEET'S (foundry-stocks versions.env COSIGN_VERSION).
	// Constellation signatures are v3 bundles, which the registry serves only
	// through the OCI referrers API, and build.go's argv is cosign 3's
	// (--use-signing-config=false, copied from the phase script it replaced).
	// v2.5.3 was pinned here first. It has no such flag, so every tip build
	// failed at sign (athena a446514), and it would have written legacy
	// .sig tags that nothing in the fleet reads. The digest is the index's.
	ImageCosign = "ghcr.io/sigstore/cosign/cosign:v3.1.1@sha256:6bbe0d281d955c79f85b325f0f7e651c1bcab5a4fa4ad4903d74955178a3b2eb"
	ImageSyft   = "docker.io/anchore/syft:v1.33.0@sha256:f94e5d9fce1f2278491a8e3a63bd5f6ddb81fdfdbb8bf7a1637565c1d5344357"
	ImageStatic = "gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab"

	// THE BASE-IMAGE GATE'S SCANNER: trivy, and the two vulnerability
	// databases it pulls as OCI artifacts. The pin is the one ca-rescan and
	// vuln-3p already run (infra flux/apps/ca-rescan.yaml,
	// flux/infrastructure/vuln-3p.yaml); the databases move by tag on purpose,
	// because a scan against last month's advisories is not a scan.
	//
	// THE DATABASES NAME A FLEET HOST, NOT ghcr.io. trivy pulls them itself,
	// from INSIDE the lane container, where the engine's mirror config does
	// not reach — a bare ghcr.io there is an unmirrored, rate-limited pull on
	// every scan. So the reference has to carry the fleet host in the name.
	//
	// THE HOST CHANGED ON 2026-09-26, and the old one had been dead for a
	// week. These read registry.notusmi.com/ghcr/… — zot's on-demand sync
	// prefix — which zot stopped serving on 2026-09-19 when third-party
	// images moved to three Distribution pull-through caches (infra
	// registry-proxy.yaml). zot answers 404 for that path today, measured.
	// Unlike a containerd MIRROR miss there is no fallback here: the hostname
	// IS the fleet's, so every endpoint resolves to zot and the pull simply
	// fails. ghcr.notusmi.com fronts registry-proxy-ghcr, which serves
	// ghcr.io at its root with no prefix — measured 200 for both databases.
	ImageTrivy      = "docker.io/aquasec/trivy:0.72.0@sha256:cffe3f5161a47a6823fbd23d985795b3ed72a4c806da4c4df16266c02accdd6f"
	TrivyDBRepo     = "ghcr.notusmi.com/aquasecurity/trivy-db:2"
	TrivyJavaDBRepo = "ghcr.notusmi.com/aquasecurity/trivy-java-db:1"
)

// The tools the lanes install, pinned. Binaries come from their own release
// URLs, fetched by the engine (fetchTool in runtime.go has the caching
// argument); the go tools are built by `go install pkg@version` through
// GoProxy; the cargo tools by `cargo install --locked --version`.
//
// opa is NOT here: the dies atoms fetch it themselves at checks.OpaVersion
// (below) and verify the version they got.
//
// OpengrepURL is a LITERAL, not a join: a `+` in a const block is a
// declaration no coverage profile can mark, so the mutation lane reads it
// as NOT COVERED forever (foundry-tools#35, twice). The test holds the two
// in step instead. GitHub's own release asset, no mirror address: the engine
// fetches it, and inside the cluster the asset host is the fleet's cache.
const (
	// renovate: datasource=github-releases depName=opengrep/opengrep
	OpengrepVersion = "v1.25.0"
	OpengrepURL     = "https://github.com/opengrep/opengrep/releases/download/" + OpengrepVersion + "/opengrep_manylinux_x86"

	StaticcheckModule = "honnef.co/go/tools/cmd/staticcheck@2025.1.1"
	GovulncheckModule = "golang.org/x/vuln/cmd/govulncheck@v1.1.4"
	// GomutantsModule is the Go mutation runner. It replaced gremlins v0.6.0 on
	// 2026-09-29 and the flip was decided by measurement, on ourea's
	// internal/gatejob at 5743c86 against 8d9f610 — the same tree and the same
	// diff gremlins had just scored as ZERO survivors:
	//
	//	gremlins   28 viable, 0 lived                     148s
	//	gomutants  114 tested, 5 survivors, 0 timed out     69s
	//
	// The five are real: an unasserted `%w` wrap, an unexercised no-annotation
	// guard, an unexercised name-mismatch skip, an unexercised error return, and
	// a loop body no test reaches. gremlins never GENERATED any of them, because
	// its five mutators do not include the operators that make them.
	//
	// THE MODULE PATH IS NOT THE REPO PATH, and `go install
	// github.com/gomutants/gomutants@latest` fails on exactly that: the go.mod
	// declares `github.com/szhekpisov/gomutants`. Reported upstream; the declared
	// path is what installs.
	//
	// renovate: datasource=go depName=github.com/szhekpisov/gomutants
	GomutantsModule = "github.com/szhekpisov/gomutants@v0.6.1"
	// MutationGateModule is forge-testkit-go's gate, run in the lane after
	// the mutation run for its -json classification of what is unkillable — the AST
	// walk that decides a top-level declaration or a case expression has no
	// coverage block. It is installed the way gremlins is, through GoProxy,
	// which the door serves for fleet modules (probed: HTTP 200 for
	// forge-testkit-go/@v/list). It is NOT a Go import of this module: the
	// scorer runs on the host where the source is a Directory, and the engine
	// builds this module with its own proxy settings, not the lane's.
	//
	// A `package main` IS NOT ONE OF ITS CLASSES, and v0.9.0 is skipped on
	// purpose for that reason. v0.9.0 added a package-main class that forgave any
	// mutant whose file said `package main`, which was over-wide even while
	// gremlins#268 stood. It is doubly wrong now: gomutants resolves packages
	// properly and has no #268 at all — MEASURED 2026-09-29 against the
	// package-main control, the exact shape of the bug (a `package main` in a
	// subdirectory, nothing at the module root), which gomutants grades LIVED,
	// the honest verdict. So ScoreGoMutation's `ungraded` bucket is expected to
	// stay EMPTY, and the control is what says so rather than this pin.
	MutationGateModule = "git.notusmi.com/rob/forge-testkit-go/cmd/mutation-gate@v0.10.0"
	// renovate: datasource=crate depName=cargo-audit
	CargoAuditVersion = "0.22.2"
	// renovate: datasource=crate depName=cargo-mutants
	CargoMutantsVersion = "27.1.0"
	// The mutation atom's test runner (`cargo mutants --test-tool nextest`).
	// One process per test with a duration on every PASS/FAIL line, which is
	// what lets the lane say which tests the per-mutant cost is made of.
	// renovate: datasource=crate depName=cargo-nextest
	CargoNextestVersion = "0.9.145"
)

// LaneImages is every image an atom may run in, so a test can assert the ONE
// property this block exists to hold — that each is pinned by digest. Listed
// rather than derived from Atoms because an image nothing currently references
// is still an image this module would ship.
var LaneImages = []string{
	ImageGo, ImagePython, ImageRust, ImageTS, ImageFleet,
	ImageUV, ImageNode,
	ImageKubeconform, ImageKubeLinter,
	ImagePgvector, ImagePostgres,
}

// The go command's coordinates inside a lane container.
//
// MEASURED, foundry-tools#7626: every go:* atom failed on tartarus with
// `Get "https://auth.notusmi.com/portals/main": stopped after 10 redirects` —
// the module fetch left the container for forgejo.notusmi.com directly and met
// the forge's SSO portal, which redirects a machine forever. The engine runs IN
// the cluster, so the door's in-cluster goproxy is reachable from an atom's
// container (probed from inside one: HTTP 200 for
// forge-testkit-go/@v/list), and these are the same three values every other
// lane in the fleet already sets — infra's ci-gate-pipeline.yaml stepTemplate
// and tartarus's .forgejo/build-args.env, verbatim.
const (
	// GoProxy puts the DOOR FIRST. It answers 404 for anything that is not a
	// fleet module, so the go command moves on to the public proxy by itself;
	// `direct` is the last resort rather than the first.
	//
	// The separator after the door is `|`, not `,`: a comma falls through
	// only on 404/410, a pipe on ANY error. The door restarts on every
	// config change and image roll, and a gate running through one of those
	// windows saw "Get http://ourea…/goproxy/…: connection refused" as a
	// findings red — MEASURED 2026-09-10T02:58Z, gate-hades-c099a35,
	// govulncheck "loading packages" while the door rolled onto 9a5313fb.
	// A door that is briefly gone is not a verdict on the code.
	//
	// THE NAME IS BARE, AND THAT IS THE POINT. It read
	// ourea.default.svc.cluster.local until 2026-09-25, and naming a namespace
	// is what broke every lane in the fleet when the constellation moved to
	// `prime`: the FQDN pointed at an empty namespace and `go` could not reach a
	// proxy. A bare name resolves through whatever search path the client has,
	// so it is correct in every namespace and survives the next move. It is also
	// what the other ~2,100 service references in the fleet already use.
	//
	// NOT ENV-DRIVEN, AND NOT FOR WANT OF TRYING. A dagger MODULE has no ambient
	// environment: its runtime container is built by the engine, and the caller's
	// env does not reach it — `os.Getenv` here sees only what dagger injects
	// (DAGGER_SESSION_PORT/TOKEN). The two ways to make this configurable are a
	// constructor parameter threaded down to goToolchain() and runtime.go (they
	// are a package-level func and a lane builder, so that is a real refactor),
	// or a declaration in foundry-dies read through the /dies mount this module
	// already carries. The second is the right home — an address is fleet data,
	// not lane code — and it is a separate landing.
	GoProxy = "http://ourea:8215/goproxy|https://proxy.golang.org,direct"
	// GoNoSumDB keeps the one thing GOPRIVATE was doing for the forge host —
	// BOTH of its names. The fleet's module paths say git.notusmi.com
	// (stellar-core-go, forge-testkit-go) and forgejo.notusmi.com (the stars);
	// a star's go.sum carries every hash so its build never asks the checksum
	// database, which is why only the second name was ever needed here. A
	// `go install pkg@version` has no go.sum and asks for every module, so
	// installing MutationGateModule with git.notusmi.com absent MEASURED:
	// "verifying module: reading https://sum.golang.org/lookup/
	// git.notusmi.com/rob/forge-testkit-go@v0.6.1: 404 Not Found" — a clean
	// cache, the lane's exact three variables, 2026-09-17. With both names
	// the same install answered a binary.
	GoNoSumDB = "git.notusmi.com,forgejo.notusmi.com"
	// GoPrivate is DELIBERATELY EMPTY and set anyway. The retired go-ci BAKED
	// GOPRIVATE=git.notusmi.com,forgejo.notusmi.com for the act lane's netrc,
	// GOPRIVATE is the default for GONOPROXY, and a GONOPROXY naming the forge
	// sends the fetch direct to it — the SSO redirect again, with GOPROXY
	// correctly set. The upstream image bakes nothing; setting it empty keeps
	// the invariant explicit rather than inherited.
	GoPrivate = ""
)

// THE foundry-stocks MOUNT WENT 2026-09-23 (CA F18). StocksRepo/StocksRef
// pinned a git tree the atoms mounted at /stocks for two things: the scripts
// that ARE an atom's tool, and the fleet's rulesets. The scripts became
// //go:embed in internal/checks/scripts long ago; the rulesets followed on
// 2026-09-23 (internal/checks/rulesets). Nothing fetches foundry-stocks now,
// which is what lets its ci/ tree be deleted rather than merely unused.

// THE MUTATION GATE'S KNOBS HAVE ONE HOME, AND IT IS NOW THIS MODULE.
//
// They used to have two. `.gremlins.yaml` in forge-testkit-go carried the mutator
// set and the timeout coefficient; goMutationWorkers and goMutationExclude in
// atoms_go.go carried the rest — and the CLI's --workers overrode the file's, so
// the two homes did not even agree about the one knob they shared. The rule that
// made the file exist is A REPO HAS NO SAY, and that rule is untouched:
// foundry-tools is not the tree under test either, and a repo can no more edit
// this module than it could edit that file.
//
// gomutants takes flags and reads no config file, so keeping the split would
// have meant inventing a file format to hold flags in another repo, plus the git
// clone that fetched it and the could-not-run branch for when the door is
// unreachable. One home, in the code that builds the argv, is the smaller
// correct thing — goMutationDisable in atoms_go.go carries the set and the
// measurement behind it.
//
// TestkitRepo / TestkitRef went with it. The testkit is still the CLASSIFIER
// (MutationGateModule above), which the lane installs by module path; the TREE
// had exactly one reader in the whole module and it was that config file.

// DiesRepo / DiesRef pin the fleet's RECORD tree — foundry-dies — for the
// atoms whose subject is the fleet rather than the repository under test.
//
// WHY A MOVING REF AND NOT A SHA. #8118 proposed a committed ref "so the
// goldens are reproducible rather than reading a mutable working tree", and
// the mutable working tree is the half that mattered: what they used to
// resolve was a constant naming ONE DEVELOPER'S HOME DIRECTORY, so what they
// graded was whatever happened to be on that disk. A door clone is a
// committed tree either way. Pinning a sha would freeze the population the
// goldens walk, and these goldens exist to assert that a projection still
// agrees with EVERY record the fleet CURRENTLY carries — a record added after
// the pin would be graded by nothing, which is the silent-skip again wearing
// a version number. Same ref, and the same reasoning, as the retired StocksRef.
const (
	DiesRepo = "https://git.notusmi.com/foundry/foundry-dies.git"
	DiesRef  = "main"
)

// The sweep's images. Same rule as the lane images above — one place — and
// pinned by DIGEST rather than by tag, so a rebuild upstream cannot move the
// check under the pin.
const (
	// ImageKubeconform is the ALPINE variant, and the pin is the alpine one:
	// the atom's body was a shell script that had to read an exit code and a
	// summary line, which the scratch variant carries nothing to run. The
	// typed atom execs /kubeconform directly and no longer needs the shell,
	// but the DIGEST is what this block exists to hold still — re-pinning to
	// scratch is a decision to make on purpose against a measured pull, not a
	// side effect of deleting the shell.
	//
	// kubeconform is ghcr's, kube-linter is Hub's — the registries they named
	// directly until 2026-09-14 and name again since 2026-09-19, at the
	// digests they always had. #132 put kubeconform on docker.io on the word
	// of a zot hub/ probe that answered 200 off a STALE copy; the next zot
	// roll emptied the mirror store, Docker Hub answered "unauthorized" for a
	// repository it does not carry, and every infra gate's ops:flux atom was
	// could-not-run (gate-infra-cce87f4). Docker Hub itself, not a mirror's
	// cache, is what a home is measured against: hub=401, ghcr=200.
	ImageKubeconform = "ghcr.io/yannh/kubeconform:v0.7.0-alpine@sha256:8f0eeaaa96ba27ba1500b0e4b1c215acc358d159c62a7ecae58d7a03403287b0"
	// ImageKubeLinter — likewise alpine, and likewise held still.
	ImageKubeLinter = "docker.io/stackrox/kube-linter:v0.8.3-alpine@sha256:b8311611c27032d4922bc67719225e373e4a0ab0c767bbdcf5f20a9306b1a3bb"
)

// MirroredRegistries are the upstream registries the engine pulls through the
// fleet's caches (infra dagger-engine.yaml registries.mirrors →
// hub./ghcr./gcr.notusmi.com, which front three Distribution pull-through
// caches — registry-proxy.yaml, not zot; zot carried them only between
// 2026-09-18 and 2026-09-19), and so the only hosts a lane image may name: an
// image from any other registry is a pull the fleet does not mirror. What is
// pulled from INSIDE a lane container names one of those alias hosts in full
// instead (trivy's databases), because the engine's mirror config cannot see it.
var MirroredRegistries = []string{"docker.io/", "ghcr.io/", "gcr.io/"}

// CRDSchema is the schema location the kubeconform atom adds to the default
// store, and the probe URL that proves it is reachable.
//
// WITHOUT IT THE ATOM MEASURES ALMOST NOTHING. Measured against infra's own
// flux/ tree on 2026-09-08: the default store alone validates 517 resources and
// SKIPS 395 — every HelmRelease, Kustomization, GitRepository and
// CiliumNetworkPolicy in the tree, which is most of what that tree IS. With the
// catalogue: 809 validated, 101 skipped. A check that skips the majority of its
// population and exits 0 is the partial-scan failure this module was built to
// end, so the catalogue is not an enhancement here — it is the difference
// between the atom being honest and being decoration.
const (
	CRDSchemaLocation = "https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json"
	// CRDSchemaProbe is one known-present schema. It is fetched BEFORE the
	// scan, because `-ignore-missing-schemas` cannot tell "this CRD is not in
	// the catalogue" from "the catalogue is unreachable": both render as
	// `skipped`, and the second is a CANNOT RUN wearing a pass's clothes.
	CRDSchemaProbe = "https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/helm.toolkit.fluxcd.io/helmrelease_v2.json"
)

// OrasURL fetches the oras client: the build lane attaches its SBOM referrer
// with it, and bundle and cast push with it. A failed fetch is exit 2 rather
// than a fallthrough.
const (
	// renovate: datasource=github-releases depName=oras-project/oras extractVersion=^v(?<version>.*)$
	OrasVersion = "1.3.0"
	OrasURL     = "https://github.com/oras-project/oras/releases/download/v" + OrasVersion + "/oras_" + OrasVersion + "_linux_amd64.tar.gz"
)

// ComposeVersion / ComposeMirror / ComposeURL fetch the client the compose:
// atoms parse specs with. Same Nexus-then-upstream shape as oras above, and the
// same refusal: a spec that was never parsed is not a spec that parses.
//
// THE LANE IMAGES CARRY NO COMPOSE CLIENT. They are language CI images — go,
// python, rust, frontend — and none of the four ships docker or the compose
// plugin, so this is a provision rather than a fallback. `config` needs no
// docker daemon (it is a client-side parse), which is why an atom in a
// socket-less container can ask the question at all.
//
// PINNED BY VERSION for the reason every other tool on this page is: a floating
// client is a gate whose verdict is not a function of the pin the door
// declared. v2.39.2 is the version the fleet's own hosts run, so the gate and
// the box parse with the same schema.
const (
	// renovate: datasource=github-releases depName=docker/compose extractVersion=^v(?<version>.*)$
	ComposeVersion = "2.39.2"
	ComposeURL     = "https://github.com/docker/compose/releases/download/v" + ComposeVersion + "/docker-compose-linux-x86_64"
)

// OpaVersion / OpaURL fetch the policy engine the dies: atoms grade with, and
// THE VERSION IS PART OF THE QUESTION.
//
// Rego's language semantics are a property of the binary. A suite written for
// v1 and graded by another major answers a different question, and "the policy
// suite passed" would be a true statement about the wrong language. foundry-dies
// pinned 1.18.0 in its own workflow; the pin moves here rather than being left
// behind with the runner.
//
// The static build deliberately: `opa_linux_amd64_static` needs no libc the lane
// image may not have, which is the same reasoning that picks the alpine variants
// for kubeconform and kube-linter above.
const (
	// renovate: datasource=github-releases depName=open-policy-agent/opa extractVersion=^v(?<version>.*)$
	OpaVersion = "1.18.0"
	OpaURL     = "https://openpolicyagent.org/downloads/v" + OpaVersion + "/opa_linux_amd64_static"
)
