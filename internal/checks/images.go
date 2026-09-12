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
// in. They named foundry-stocks .forgejo/workflows/gate.yml as their peer until
// that file left with the rest of the Forgejo workflows — the gate is the
// door's runner now, and this block is the only place the four are declared.
// Measured inside the engine on 2026-09-09, each carries what its atoms exec:
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
//
// REPINNED 2026-09-11, ALL FOUR AT ONCE. The bases were rebuilt and the
// registry collected every previous digest: go-ci, python-ci, rust-ci and
// frontend-ci all answered 404 by digest while their tags resolved fine, so
// every lane's mutation stage failed ~200ms in with an error naming the
// REGISTRY rather than the pin. Third time in nine days (foundry-stocks'
// ci/lib/digest-pins.sh records 09-02 and 09-07). That probe lives in the
// wrong repo to catch THIS file — the pins it checks are cast.yml's, and
// these are the gate's own. Until a schedule watches these four, a rebuild
// of stellar_core is a fleet-wide red that nobody sees until a landing.
//
// THE HOST IS registry.notusmi.com (zot), NOT THE FORGE. Storing anything on
// Forgejo is not an end state, and two of these four digests now exist ONLY on
// zot. The digests are unchanged by the move — the same four bytes-for-bytes
// images, addressed at the registry that will outlive the forge. Verified
// present before the move: all four answer 200 on
// /v2/rob/stellar_core/manifests/sha256:<digest>, and zot serves manifests AND
// blobs ANONYMOUSLY (measured: 200, 14867 bytes for python-ci's config blob
// with no credential), so the engine needs no auths entry for this host —
// ca-gate-pull's CA_GATE_DOCKER_CONFIG_JSON still names only the forge and
// does not have to change.
const (
	ImageGo     = "registry.notusmi.com/rob/stellar_core:go-ci@sha256:5f684657c2ba294752edcb456efbdf3237290b8a666ebdcd4cb7025431bbdf7a"
	ImagePython = "registry.notusmi.com/rob/stellar_core:python-ci@sha256:b2e0985bacc458d2619606b10e68f5d938275db37152fb408b9c7b69dff0ac32"
	ImageRust   = "registry.notusmi.com/rob/stellar_core:rust-ci@sha256:3c8159334177745d7526e26e16bafbcfa268ccfb7ca194cac2ddddd9d39343e9"
	ImageTS     = "registry.notusmi.com/rob/stellar_core:frontend-ci@sha256:966e17d2853028dc5a6fe202435171bbdf7f0271bcba9b114ed1aef3e78f31ec"
	// The fleet atoms run in python-ci: they are python and shell, and it is
	// the only one of the four carrying uvx, which three of them provision
	// with.
	ImageFleet = ImagePython
)

// LaneImages is every image an atom may run in, so a test can assert the ONE
// property this block exists to hold — that each is pinned by digest. Listed
// rather than derived from Atoms because an image nothing currently references
// is still an image this module would ship.
var LaneImages = []string{
	ImageGo, ImagePython, ImageRust, ImageTS, ImageFleet,
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
