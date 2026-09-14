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

// The images the atoms run in must be the fleet's own CI images. A public base
// is a base nobody in this fleet controls the contents of, and the contents are
// exactly what broke.
//
// THE HOST IS zot, AND MOVING IT BACK TO THE FORGE IS THE REGRESSION. This read
// forgejo.notusmi.com until 2026-09-10. The digests did not change in that move
// and must not: the assertion is about WHERE the fleet's images are addressed,
// not which images they are. Two of the four now exist only on zot, so a revert
// of this one string is an unpullable gate.
func TestEveryLaneImageIsAnUpstreamToolchainOnTheMirror(t *testing.T) {
	const want = "docker.notusmi.com/"
	for _, img := range []string{ImageGo, ImagePython, ImageRust, ImageTS, ImageFleet, ImageUV, ImageNode} {
		if !strings.HasPrefix(img, want) {
			t.Errorf("lane image %q is not an upstream toolchain on the fleet's mirror (%s…) — the CI images retired 2026-09-12 and docker.io is never dialled directly", img, want)
		}
		if strings.Contains(img, "stellar_core:") {
			t.Errorf("lane image %q is one of the fleet's own images — those are what Rob stopped maintaining", img)
		}
	}
}

// BY DIGEST, NEVER BY TAG. All four CI images are MOVING tags: CronJob
// foundry-weekly rebuilds them every Monday and base-rescan rebuilds them
// whenever the vulnerability DB moves. A gate whose image floats produces a
// verdict that is not a function of the pin the door declared, and that pin is
// the whole of the F7 join.
//
// PinRefPattern is the canonical extractor's own form — the same one
// sweep:digest-pins reads pins with — so this asserts the block is legible to
// the sweep that exists to notice a collected digest, not merely that a hex
// string is present.
func TestEveryImageIsPinnedByDigest(t *testing.T) {
	ref := regexp.MustCompile(PinRefPattern)
	surface := regexp.MustCompile(PinSurfacePattern)
	for _, img := range LaneImages {
		if !surface.MatchString(img) {
			t.Errorf("image %q carries no digest — a rebuild would move the gate under the declared pin", img)
			continue
		}
		if !ref.MatchString(img) {
			t.Errorf("image %q carries a digest the canonical extractor cannot read", img)
		}
	}
}

// THROUGH THE FLEET MIRROR, NEVER UPSTREAM. Every image an atom or the build
// function runs is pulled from docker.notusmi.com. An upstream registry is a
// dependency the fleet does not operate, and it rate-limits and moves on its
// own schedule. kubeconform and kube-linter named ghcr.io and docker.io
// directly until 2026-09-14.
func TestEveryImageComesThroughTheFleetMirror(t *testing.T) {
	for _, img := range append(append([]string{}, LaneImages...), ImageCosign, ImageSyft, ImageStatic) {
		if !strings.HasPrefix(img, "docker.notusmi.com/") {
			t.Errorf("image %q is not pulled through the fleet mirror (docker.notusmi.com)", img)
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
	if GoNoSumDB != "forgejo.notusmi.com" {
		t.Errorf("GONOSUMDB is %q — the forge host is what GOPRIVATE was covering", GoNoSumDB)
	}
}

// The tools the lanes provision are pinned, and the pins are legible: the
// opengrep download names its version on the mirror's github-raw route, the
// go tools carry a version after @ that is never @latest, and the cargo
// tools are plain versions.
func TestTheProvisionedToolsArePinnedAndTheMirrorRouteCarriesTheVersion(t *testing.T) {
	if !strings.HasPrefix(OpengrepMirror, "https://nexus.notusmi.com/repository/github-raw/opengrep/opengrep/releases/download/") {
		t.Errorf("opengrep is fetched from somewhere other than the mirror's github-raw route: %s", OpengrepMirror)
	}
	if !strings.Contains(OpengrepMirror, "/"+OpengrepVersion+"/") || !strings.HasSuffix(OpengrepMirror, "/opengrep_manylinux_x86") {
		t.Errorf("the opengrep route does not name the pin and the artefact: %s", OpengrepMirror)
	}
	for _, mod := range []string{StaticcheckModule, GovulncheckModule, GremlinsModule} {
		at := strings.LastIndex(mod, "@")
		if at < 0 || at == len(mod)-1 || mod[at+1:] == "latest" {
			t.Errorf("go tool %q is not pinned to a version", mod)
		}
	}
	for _, v := range []string{CargoAuditVersion, CargoMutantsVersion} {
		if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(v) {
			t.Errorf("cargo tool version %q is not a plain semver pin", v)
		}
	}
}
