package checks

// The image map — the toolchain lives in the module, not on a laptop.
//
// ONE PLACE, deliberately. Every lane image and every tool pin is named here
// so digest pinning is a single edit rather than a sweep: the fleet has been
// broken twice by a floating base, and `digest-pins` is the sweep that will
// land on this block.
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
// source at a version that moves most (staticcheck, govulncheck, gremlins;
// cargo-audit, cargo-mutants). A cache hit survives everything but the last
// layer moving.
//
// WHAT EACH LANE EXECS, measured at the exec sites (atoms_*.go and the
// mutation scripts in foundry-stocks ci/lib/mutation/):
//
//	go       go, staticcheck, govulncheck, gremlins (go.sh), python3
//	         (go.sh scores with go_score.py), git, bash
//	python   uv, uvx, python3, opengrep, git, tar, bash; opa (dies) and
//	         oras (sweep) the atoms fetch themselves, pinned below
//	rust     cargo (+ rustfmt, clippy, audit, mutants), git, bash
//	ts       bun, node (ts.sh runs stryker under node), git, bash
//
// BY DIGEST, NEVER BY TAG. Every image here is a moving tag upstream; a gate
// whose image floats is a gate whose verdict is not a function of the pin
// the door declared, and the pin is the whole of F7's join. The host is the
// fleet's docker mirror (docker.notusmi.com), never docker.io directly.
const (
	ImageGo     = "docker.notusmi.com/library/golang:1.26.6-bookworm@sha256:116d58cbd88c1297624acc6e967a060012422bacf9930927e23fb719189c6f36"
	ImagePython = "docker.notusmi.com/library/python:3.14-slim-bookworm@sha256:9ab8d9c8514b44f90cf0029dd42fdd7e9e211e639c8b995304cc04568dee900f"
	ImageRust   = "docker.notusmi.com/library/rust:1.97.0-bookworm@sha256:8fa55b2f3ddf97471ab6a767bfa3f37e6bad0986ba823e75fea57e2a2a5c3073"
	ImageTS     = "docker.notusmi.com/oven/bun:1.4-slim@sha256:cb3bbbb08e13a4a2ff400f24c7a2a1d5efa83f6ef8544d52d95a519631e2fc61"
	// The fleet atoms run in the python lane: they are python and shell, and
	// three of them provision with uvx.
	ImageFleet = ImagePython
)

// The tool images a lane copies ONE binary out of. Both are scratch upstream
// (no shell), which is why the binary is taken by File and never by exec.
const (
	// ImageUV carries /uv and /uvx — the python lane's whole package
	// manager, and the version the fleet's uv.lock files were written under.
	ImageUV = "docker.notusmi.com/astral-sh/uv:0.12.13@sha256:b485bd65cc2cf1c9a93b3554012c9c3778cf7b1b5fd3d3096ce9e1226c97e1e6"
	// ImageNode carries the node the ts mutation script runs stryker under
	// (`./node_modules/.bin/stryker` under node, never `bunx --bun` — ts.sh
	// says why). The bun image ships no node.
	ImageNode = "docker.notusmi.com/library/node:24-bookworm-slim@sha256:2fe369e969550cde8e867afc3fe370b260140cab4a23d467074295b42163d553"
)

// The tools the lanes install, pinned. Binaries come through Nexus's
// github-raw proxy (the same route the retired CI images fetched them by);
// the go tools are built by `go install pkg@version` through GoProxy; the
// cargo tools by `cargo install --locked --version`.
//
// opa is NOT here: the dies atoms fetch it themselves at checks.OpaVersion
// (below), mirror then upstream, and verify the version they got.
const (
	OpengrepVersion = "v1.25.0"
	OpengrepMirror  = "https://nexus.notusmi.com/repository/github-raw/opengrep/opengrep/releases/download/" + OpengrepVersion + "/opengrep_manylinux_x86"

	StaticcheckModule   = "honnef.co/go/tools/cmd/staticcheck@2025.1.1"
	GovulncheckModule   = "golang.org/x/vuln/cmd/govulncheck@v1.1.4"
	GremlinsModule      = "github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0"
	CargoAuditVersion   = "0.22.2"
	CargoMutantsVersion = "27.1.0"
)

// LaneImages is every image an atom may run in, so a test can assert the ONE
// property this block exists to hold — that each is pinned by digest. Listed
// rather than derived from Atoms because an image nothing currently references
// is still an image this module would ship.
var LaneImages = []string{
	ImageGo, ImagePython, ImageRust, ImageTS, ImageFleet,
	ImageUV, ImageNode,
	ImageKubeconform, ImageKubeLinter,
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
	GoProxy = "http://ourea.default.svc.cluster.local:8215/goproxy|https://proxy.golang.org,direct"
	// GoNoSumDB keeps the one thing GOPRIVATE was doing for the forge host.
	GoNoSumDB = "forgejo.notusmi.com"
	// GoPrivate is DELIBERATELY EMPTY and set anyway. The retired go-ci BAKED
	// GOPRIVATE=git.notusmi.com,forgejo.notusmi.com for the act lane's netrc,
	// GOPRIVATE is the default for GONOPROXY, and a GONOPROXY naming the forge
	// sends the fetch direct to it — the SSO redirect again, with GOPROXY
	// correctly set. The upstream image bakes nothing; setting it empty keeps
	// the invariant explicit rather than inherited.
	GoPrivate = ""
)

// StocksRepo / StocksRef pin the ONE definition of the checks that are scripts
// rather than tool invocations.
//
// A copy would be the defect the script itself exists to catch: on 2026-08-16 a
// sweep found 533 noqa on one rule, 368 of them eight decisions replicated into
// 46 repos by a scaffold pour. `stop_justifications.py` has exactly one home,
// and this reads it there through the door rather than vendoring a second.
const (
	StocksRepo = "https://git.notusmi.com/foundry/foundry-stocks.git"
	StocksRef  = "main"
)

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
// a version number. Same ref, and the same reasoning, as StocksRef above.
const (
	DiesRepo = "https://git.notusmi.com/foundry/foundry-dies.git"
	DiesRef  = "main"
)

// The sweep's images. Same rule as the lane images above — one place, so the
// digest-pins sweep lands on a single block — but these are pinned by DIGEST
// today rather than by tag, because the two of them are the atoms that judge
// pinning and a floating base under a pin checker is the joke telling itself.
const (
	// ImageKubeconform is the ALPINE variant, and the pin is the alpine one:
	// the atom's body was a shell script that had to read an exit code and a
	// summary line, which the scratch variant carries nothing to run. The
	// typed atom execs /kubeconform directly and no longer needs the shell,
	// but the DIGEST is what this block exists to hold still — re-pinning to
	// scratch is a decision to make on purpose against a measured pull, not a
	// side effect of deleting the shell.
	ImageKubeconform = "ghcr.io/yannh/kubeconform:v0.7.0-alpine@sha256:8f0eeaaa96ba27ba1500b0e4b1c215acc358d159c62a7ecae58d7a03403287b0"
	// ImageKubeLinter — likewise alpine, and likewise held still.
	ImageKubeLinter = "docker.io/stackrox/kube-linter:v0.8.3-alpine@sha256:b8311611c27032d4922bc67719225e373e4a0ab0c767bbdcf5f20a9306b1a3bb"
)

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

// OrasURL / OrasMirror fetch the client `digest-pins.sh` resolves pins with.
//
// Nexus first, github.com second, and a failure of BOTH is exit 2 rather than a
// fallthrough — resolving zero pins and reporting them all fine is precisely
// the outage the script exists to catch, in reverse.
const (
	OrasVersion = "1.3.0"
	OrasMirror  = "https://nexus.notusmi.com/repository/github-raw/oras-project/oras/releases/download/v" + OrasVersion + "/oras_" + OrasVersion + "_linux_amd64.tar.gz"
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
	ComposeVersion = "2.39.2"
	ComposeMirror  = "https://nexus.notusmi.com/repository/github-raw/docker/compose/releases/download/v" + ComposeVersion + "/docker-compose-linux-x86_64"
	ComposeURL     = "https://github.com/docker/compose/releases/download/v" + ComposeVersion + "/docker-compose-linux-x86_64"
)

// OpaVersion / OpaMirror / OpaURL fetch the policy engine the dies: atoms grade
// with, and THE VERSION IS PART OF THE QUESTION.
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
	OpaVersion = "1.18.0"
	OpaMirror  = "https://nexus.notusmi.com/repository/github-raw/open-policy-agent/opa/releases/download/v" + OpaVersion + "/opa_linux_amd64_static"
	OpaURL     = "https://openpolicyagent.org/downloads/v" + OpaVersion + "/opa_linux_amd64_static"
)

// PinSurfacePattern and PinRefPattern are the two questions the digest-pins
// atom has to ask SEPARATELY. Conflating them is the defect this pair exists
// to end.
//
// MEASURED, not theorised. ca-sweep-manual-1788973171 (2026-09-09) answered
// `cannot-run` on 57 of the 86 repos in custody, every one of them with:
//
//	0 pin(s) checked, 0 broken, 0 drifted
//	no pins found under .forgejo/workflows - the scan is broken, not the tree clean
//
// That sentence is TRUE in foundry-stocks, where the script lives and where
// cast.yml carries several pins. It is false everywhere else: a star calls the
// reusable workflow (`uses: foundry/foundry-stocks/.forgejo/workflows/gate.yml@main`)
// and the image pin lives in the CALLEE's tree. Those 57 repos have an empty
// pin population, which is an absence, not a broken scan - and 57 false
// could-not-runs is how the one real finding in that run got buried.
//
//	PinSurfacePattern - does this tree carry ANY digest reference at all?
//	  no  -> ABSENT. Nothing was checked and nothing needed to be.
//	  yes -> the population is not empty, so the extractor must find it.
//	PinRefPattern     - the canonical extractor's own form, mirrored from
//	  digest_pins() in foundry-stocks/ci/lib/digest-pins.sh. When the surface
//	  is there and this extracts nothing, THAT is the broken scan the script's
//	  message names, and it stays a CANNOT RUN.
//
// The surface pattern must be the BROADER of the two - every reference the
// canonical extractor accepts has to match it - or the atom would file a real
// pin as an absence, which is the failure direction that matters.
// TestPinSurfaceAdmitsEveryCanonicalRef holds that invariant.
const (
	PinSurfacePattern = `@sha256:[0-9a-f]{64}`
	PinRefPattern     = `[a-zA-Z0-9._-]+\.[a-zA-Z]+/[a-zA-Z0-9._/-]+(:[a-zA-Z0-9._-]+)?@sha256:[0-9a-f]{64}`
)
