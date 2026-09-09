package checks

// The image map — the toolchain lives in the module, not on a laptop.
//
// ONE PLACE, deliberately. Every lane image is named here so digest pinning is
// a single edit rather than a sweep: the fleet has been broken twice by a
// floating base, and `digest-pins` is the sweep that will land on this block.
//
// THE FLEET'S OWN CI IMAGES, BY DIGEST, SINCE 2026-09-09. They were public
// bases on floating tags (golang:1.26-bookworm, ghcr.io/astral-sh/uv, rust:1,
// oven/bun) and the gate could not go green in ANY repo at the declared pin
// because of what those images do not carry (foundry-tools#7626, measured on
// tartarus and foundry-stocks):
//
//	fleet:opengrep-sast     cannot-run — "sh: 3: curl: not found"; the atom
//	                        provisions opengrep with curl and the uv image has
//	                        no curl.
//	fleet:stop-justifications  FileNotFoundError: 'git' — the canonical script
//	                        enumerates the tree with `git ls-files` and the uv
//	                        image has no git.
//	every go:* atom         "Get https://auth.notusmi.com/portals/main: stopped
//	                        after 10 redirects" — golang:1.26-bookworm resolves
//	                        forgejo.notusmi.com modules straight at the forge,
//	                        where the SSO portal answers. (The proxy vars in
//	                        main.go are the other half of that fix.)
//
// These four are the images the CANONICAL GATE already runs every star's checks
// in (foundry-stocks .forgejo/workflows/gate.yml carries the same four
// references, digest for digest). Measured inside the engine on 2026-09-09,
// each carries what its atoms exec:
//
//	go-ci        git curl python3 uv go opengrep tar wget bash
//	python-ci    git curl python3 uv uvx node opengrep tar bash
//	rust-ci      git curl python3 uv cargo node opengrep tar wget bash
//	frontend-ci  git curl bun node opengrep tar bash
//
// So the engine and CI grade with ONE toolchain rather than two that are free
// to disagree — which is the same argument StocksRepo below makes for the
// scripts, applied to the containers they run in.
//
// BY DIGEST, NEVER BY TAG. All four are moving tags: CronJob foundry-weekly
// rebuilds them every Monday, and base-rescan rebuilds them whenever the vuln
// DB moves. A gate whose image floats is a gate whose verdict is not a function
// of the pin the door declared, and the pin is the whole of F7's join.
const (
	imageGo     = "forgejo.notusmi.com/rob/stellar_core:go-ci@sha256:aeb43e74f78f467e31cde95bfc9c0ac825a5cb615050e0dff1d497f95f917b71"
	imagePython = "forgejo.notusmi.com/rob/stellar_core:python-ci@sha256:9a3b945979f280a565fcd5fb7efb779949344bd12744bd91e74ae00d0aae7ef8"
	imageRust   = "forgejo.notusmi.com/rob/stellar_core:rust-ci@sha256:85d31a89f0536c32eb9574e8285f5cdfb80e5b93b5846859f73bc2ed4b1027ce"
	imageTS     = "forgejo.notusmi.com/rob/stellar_core:frontend-ci@sha256:745401b9df433aa33e44a2e11550245cccbe3f3d1f336d64c94957bdf7ab8e2f"
	// The fleet atoms run in python-ci: they are python and shell, and it is
	// the only one of the four carrying uvx, which three of them provision
	// with.
	imageFleet = imagePython
)

// LaneImages is every image an atom may run in, so a test can assert the ONE
// property this block exists to hold — that each is pinned by digest. Listed
// rather than derived from Atoms because an image nothing currently references
// is still an image this module would ship.
var LaneImages = []string{
	imageGo, imagePython, imageRust, imageTS, imageFleet,
	imageKubeconform, imageKubeLinter,
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
	GoProxy = "http://ourea.default.svc.cluster.local:8215/goproxy,https://proxy.golang.org,direct"
	// GoNoSumDB keeps the one thing GOPRIVATE was doing for the forge host.
	GoNoSumDB = "forgejo.notusmi.com"
	// GoPrivate is DELIBERATELY EMPTY and must be set anyway. go-ci BAKES
	// GOPRIVATE=git.notusmi.com,forgejo.notusmi.com for the act lane's netrc,
	// GOPRIVATE is the default for GONOPROXY, and a GONOPROXY naming the forge
	// sends the fetch direct to it — the SSO redirect again, with GOPROXY
	// correctly set. Setting it empty overrides the image's.
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

// The sweep's images. Same rule as the lane images above — one place, so the
// digest-pins sweep lands on a single block — but these are pinned by DIGEST
// today rather than by tag, because the two of them are the atoms that judge
// pinning and a floating base under a pin checker is the joke telling itself.
const (
	// imageKubeconform is the ALPINE variant deliberately: the scratch image
	// carries the binary and nothing else, and every atom body here is a
	// shell script that has to read an exit code and a summary line.
	imageKubeconform = "ghcr.io/yannh/kubeconform:v0.7.0-alpine@sha256:8f0eeaaa96ba27ba1500b0e4b1c215acc358d159c62a7ecae58d7a03403287b0"
	// imageKubeLinter — likewise alpine, for the shell.
	imageKubeLinter = "docker.io/stackrox/kube-linter:v0.8.3-alpine@sha256:b8311611c27032d4922bc67719225e373e4a0ab0c767bbdcf5f20a9306b1a3bb"
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
