package checks

// The image map — the toolchain lives in the module, not on a laptop.
//
// ONE PLACE, deliberately. Every lane image is named here so digest pinning is
// a single edit rather than a sweep: the fleet has been broken twice by a
// floating base, and `digest-pins` is the sweep that will land on this block.
// Tags today, digests when that sweep arrives.
const (
	imageGo     = "docker.io/library/golang:1.26-bookworm"
	imagePython = "ghcr.io/astral-sh/uv:python3.13-bookworm-slim"
	imageRust   = "docker.io/library/rust:1-bookworm"
	imageTS     = "docker.io/oven/bun:1-debian"
	imageFleet  = "ghcr.io/astral-sh/uv:python3.13-bookworm-slim"
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
