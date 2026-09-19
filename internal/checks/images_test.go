package checks

import (
	"regexp"
	"strings"
	"testing"
)

// THE DEFECT THIS FILE HOLDS SHUT (foundry-tools#7626, measured 2026-09-09).
//
// The gate could not go green in ANY repository at the declared pin, and not
// one of the three reasons was in a repo's code. They were all properties of
// the containers the atoms ran in: no curl for opengrep's installer, no git for
// the tree walk, and a go command that resolved forgejo.notusmi.com straight at
// the forge's SSO portal. A verdict vector whose max state is 2 refuses the
// push, so every repo taking the delivered hook stopped being pushable.
//
// Each test below pins one of the properties that fix rests on — the ones that
// are properties of the IMAGES. The three that read the atom bodies (a git
// probe, a worktree prelude, opengrep's curl fallback) went with the bodies:
// provisioning is its own exec under the default Expect now, so a tool that is
// not there is a Dagger error and verdict() files it as state 2 by
// construction rather than by a guard a test had to find in a string.

// The images the atoms run in are the upstream toolchains, named by the
// registry they live on. The fleet's own CI images retired 2026-09-12 (Rob:
// "Maintaining our own was a mistake"), and a lane image that is one of ours
// is the regression. The names read forgejo.notusmi.com until 2026-09-10 and
// the Nexus alias docker.notusmi.com until 2026-09-19; the digests did not
// change in either move and must not: the assertion is about WHERE the
// images are addressed, not which images they are.
//
// MIRRORED BY THE ENGINE, NOT BY THE STRING. The registries here are the
// ones the engine's registries.mirrors sends through zot (MirroredRegistries),
// so naming docker.io IS the mirrored pull; a registry outside that set is
// a pull nothing mirrors.
func TestEveryLaneImageIsAnUpstreamToolchainOnTheMirror(t *testing.T) {
	for _, img := range []string{ImageGo, ImagePython, ImageRust, ImageTS, ImageFleet, ImageUV, ImageNode} {
		if !mirrored(img) {
			t.Errorf("lane image %q is not on a registry the engine mirrors (%v)", img, MirroredRegistries)
		}
		if strings.Contains(img, "stellar_core:") || strings.HasPrefix(img, "registry.notusmi.com/rob/") {
			t.Errorf("lane image %q is one of the fleet's own images — those are what Rob stopped maintaining", img)
		}
		if strings.HasPrefix(img, "docker.notusmi.com/") || strings.HasPrefix(img, "forgejo.notusmi.com/") {
			t.Errorf("lane image %q names a retired alias; name the registry it lives on", img)
		}
	}
}

func mirrored(img string) bool {
	for _, m := range MirroredRegistries {
		if strings.HasPrefix(img, m) {
			return true
		}
	}
	return false
}

// BY DIGEST, NEVER BY TAG. All four CI images are MOVING tags: CronJob
// foundry-weekly rebuilds them every Monday and base-rescan rebuilds them
// whenever the vulnerability DB moves. A gate whose image floats produces a
// verdict that is not a function of the pin the door declared, and that pin is
// the whole of the F7 join.
//
// The pin is asserted whole — registry host, repository, optional tag, then
// the digest at the END — not merely that a hex string appears somewhere.
func TestEveryImageIsPinnedByDigest(t *testing.T) {
	pinned := regexp.MustCompile(`^[a-zA-Z0-9._-]+\.[a-zA-Z]+/[a-zA-Z0-9._/-]+(:[a-zA-Z0-9._-]+)?@sha256:[0-9a-f]{64}$`)
	for _, img := range LaneImages {
		if !pinned.MatchString(img) {
			t.Errorf("image %q is not pinned by digest — a rebuild would move the gate under the declared pin", img)
		}
	}
}

// THROUGH THE FLEET MIRROR, NEVER UPSTREAM DIRECTLY. Every image an atom or
// the build function runs lives on a registry the engine mirrors through zot
// (MirroredRegistries): an upstream the fleet does not mirror rate-limits and
// moves on its own schedule, and this is where a quay.io or vendor image
// would be caught. The trivy databases are the exception by design — pulled
// by trivy from inside the lane container, where the engine's mirror config
// does not reach — and name zot's prefix on the fleet's own registry.
func TestEveryImageComesThroughTheFleetMirror(t *testing.T) {
	for _, img := range append(append([]string{}, LaneImages...), ImageCosign, ImageSyft, ImageStatic) {
		if !mirrored(img) {
			t.Errorf("image %q is not on a registry the engine mirrors (%v)", img, MirroredRegistries)
		}
	}
	for _, repo := range []string{TrivyDBRepo, TrivyJavaDBRepo} {
		if !strings.HasPrefix(repo, "registry.notusmi.com/ghcr/") {
			t.Errorf("trivy database %q is pulled from inside the lane container and must name zot's ghcr/ prefix, not an upstream", repo)
		}
	}
}

// Every image an atom names has to be in the block a digest sweep lands on. An
// image referenced from the atom table but absent from LaneImages is an image
// no pin check would ever look at.
func TestEveryAtomRunsInAListedImage(t *testing.T) {
	listed := map[string]bool{}
	for _, img := range LaneImages {
		listed[img] = true
	}
	for _, a := range Atoms {
		if a.Image == "" {
			t.Errorf("atom %q names no image", a.ID)
			continue
		}
		if !listed[a.Image] {
			t.Errorf("atom %q runs in %q, which LaneImages does not list — nothing would ever check that pin", a.ID, a.Image)
		}
	}
}

// The go command's coordinates, asserted in the order that matters. The door
// answers 404 for anything that is not a fleet module, so it must come FIRST
// and `direct` must come LAST: reversed, the fetch leaves for forgejo directly
// and meets the SSO portal, which is the measured failure.
func TestGoProxyPutsTheDoorFirstAndDirectLast(t *testing.T) {
	parts := strings.Split(GoProxy, ",")
	if len(parts) < 2 {
		t.Fatalf("GOPROXY %q has no fallback at all", GoProxy)
	}
	if !strings.HasPrefix(parts[0], "http://ourea.default.svc.cluster.local:") {
		t.Errorf("GOPROXY does not start at the in-cluster door: %q", GoProxy)
	}
	if parts[len(parts)-1] != "direct" {
		t.Errorf("GOPROXY does not end at `direct`: %q", GoProxy)
	}
	for _, p := range parts[:len(parts)-1] {
		if p == "direct" {
			t.Errorf("GOPROXY reaches `direct` before its last element — the fetch would meet the SSO portal: %q", GoProxy)
		}
	}
}

// GOPRIVATE IS EMPTY ON PURPOSE, and the emptiness is the assertion. go-ci
// BAKES GOPRIVATE=git.notusmi.com,forgejo.notusmi.com for the act lane's
// netrc; GOPRIVATE is GONOPROXY's default; a GONOPROXY naming the forge sends
// the fetch direct to it however right GOPROXY is. Anything non-empty here
// silently restores the bug.
func TestGoPrivateIsEmptySoTheImagesBakedOneCannotWin(t *testing.T) {
	if GoPrivate != "" {
		t.Errorf("GOPRIVATE is %q — a non-empty value re-enables the direct fetch that meets the SSO portal", GoPrivate)
	}
	// Both of the forge's names, and the order is a test's convenience only.
	// A star's go.sum never asks the checksum database; `go install pkg@v`
	// asks for everything, and MutationGateModule lives under the first.
	if GoNoSumDB != "git.notusmi.com,forgejo.notusmi.com" {
		t.Errorf("GONOSUMDB is %q — the forge answers to both names and a go install of a fleet module asks the sumdb about the first", GoNoSumDB)
	}
}

// The tools the lanes provision are pinned, and the pins are legible: the
// opengrep download names its version on the mirror's github-raw route, the
// go tools carry a version after @ that is never @latest, and the cargo
// tools are plain versions.
func TestTheProvisionedToolsArePinnedAndTheReleaseURLCarriesTheVersion(t *testing.T) {
	if !strings.HasPrefix(OpengrepURL, "https://github.com/opengrep/opengrep/releases/download/") {
		t.Errorf("opengrep is fetched from somewhere other than its own release URL: %s", OpengrepURL)
	}
	if !strings.Contains(OpengrepURL, "/"+OpengrepVersion+"/") || !strings.HasSuffix(OpengrepURL, "/opengrep_manylinux_x86") {
		t.Errorf("the opengrep URL does not name the pin and the artefact: %s", OpengrepURL)
	}
	for _, mod := range []string{StaticcheckModule, GovulncheckModule, GremlinsModule, MutationGateModule} {
		at := strings.LastIndex(mod, "@")
		if at < 0 || at == len(mod)-1 || mod[at+1:] == "latest" {
			t.Errorf("go tool %q is not pinned to a version", mod)
		}
	}
	if host := MutationGateModule[:strings.Index(MutationGateModule, "/")]; !strings.Contains(","+GoNoSumDB+",", ","+host+",") {
		t.Errorf("mutation-gate is installed from %s, which GONOSUMDB %q does not name — the install would ask sum.golang.org and get a 404", host, GoNoSumDB)
	}
	for _, v := range []string{CargoAuditVersion, CargoMutantsVersion} {
		if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(v) {
			t.Errorf("cargo tool version %q is not a plain semver pin", v)
		}
	}
}
