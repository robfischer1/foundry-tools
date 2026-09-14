package checks

import (
	"regexp"
	"strings"
	"testing"
)

// canonicalPins are pin references taken verbatim off the fleet's own trees on
// 2026-09-09. They are the population the canonical extractor
// (foundry-stocks/ci/lib/digest-pins.sh, digest_pins()) is written to find.
var canonicalPins = []string{
	"forgejo.notusmi.com/rob/stellar_core:go-ci@sha256:aeb43e74f78f467e31cde95bfc9c0ac825a5cb615050e0dff1d497f95f917b71",
	"forgejo.notusmi.com/rob/stellar_core:python-ci@sha256:b1a409c4a80c24709cffc02ae458e45157f37ebdf3805749cc9064757c371170",
	"registry.notusmi.com/rob/stellar_core:python-ci@sha256:b1a409c4a80c24709cffc02ae458e45157f37ebdf3805749cc9064757c371170",
	"ghcr.io/yannh/kubeconform:v0.7.0-alpine@sha256:8f0eeaaa96ba27ba1500b0e4b1c215acc358d159c62a7ecae58d7a03403287b0",
	"docker.io/stackrox/kube-linter:v0.8.3-alpine@sha256:b8311611c27032d4922bc67719225e373e4a0ab0c767bbdcf5f20a9306b1a3bb",
	"ghcr.io/astral-sh/uv@sha256:b1a409c4a80c24709cffc02ae458e45157f37ebdf3805749cc9064757c371170",
	// The same two pins through the fleet mirror, the form images.go carries
	// since 2026-09-14.
	"docker.notusmi.com/yannh/kubeconform:v0.7.0-alpine@sha256:8f0eeaaa96ba27ba1500b0e4b1c215acc358d159c62a7ecae58d7a03403287b0",
	"docker.notusmi.com/stackrox/kube-linter:v0.8.3-alpine@sha256:b8311611c27032d4922bc67719225e373e4a0ab0c767bbdcf5f20a9306b1a3bb",
}

// THE INVARIANT THE FIX RESTS ON. The surface probe decides whether a repo has
// a pin population at all, and a false NEGATIVE there files a live pin as an
// absence — the exact failure direction this module exists to make
// unrepresentable. So every reference the canonical extractor accepts must
// also match the surface pattern; the surface pattern is allowed to be
// broader, and deliberately is, because anything it matches that the extractor
// then cannot read is a BROKEN SCAN rather than an empty tree.
func TestPinSurfaceAdmitsEveryCanonicalRef(t *testing.T) {
	surface := regexp.MustCompile(PinSurfacePattern)
	ref := regexp.MustCompile(PinRefPattern)
	for _, pin := range canonicalPins {
		if !ref.MatchString(pin) {
			t.Errorf("PinRefPattern does not match %q — it has drifted from digest_pins() in foundry-stocks", pin)
		}
		if !surface.MatchString(pin) {
			t.Errorf("PinSurfacePattern does not match %q — the atom would call a live pin an absence", pin)
		}
	}
}

// The surface probe is what turns 57 false could-not-runs into 57 honest
// absences, so it has to be a probe and not a wildcard: a tree with no digest
// reference is genuinely empty, and one with a malformed reference is NOT.
func TestPinSurfaceRejectsWhatIsNotADigestPin(t *testing.T) {
	surface := regexp.MustCompile(PinSurfacePattern)
	for _, notAPin := range []string{
		"uses: foundry/foundry-stocks/.forgejo/workflows/gate.yml@main",
		"image: docker.io/library/golang:1.26-bookworm",
		"@sha512:aeb43e74f78f467e31cde95bfc9c0ac825a5cb615050e0dff1d497f95f917b71",
		"@sha256:aeb43e74f78f467e31cde95bfc9c0ac825a5cb615050e0dff1d497f95f917b7",
		"@sha256:AEB43E74F78F467E31CDE95BFC9C0AC825A5CB615050E0DFF1D497F95F917B71",
	} {
		if surface.MatchString(notAPin) {
			t.Errorf("PinSurfacePattern matched %q — a tree with no real pin would be scanned as though it had one", notAPin)
		}
	}
}

// THE DEFECT, ASSERTED. `no pins found under .forgejo/workflows — the scan is
// broken, not the tree clean` is right in foundry-stocks and wrong in the 57
// stars that call the reusable workflow, and the atom is the only layer that
// can tell the two apart. The probe itself is HasPinSurface, with its own table
// in sweeplane_test.go; what the atom does with the answer — ABSENT for no
// surface, CANNOT RUN for a surface the canonical extractor could not read — is
// a pair of literals in sweepDigestPins, and the ORDER (probe before the oras
// fetch, so a repo with nothing to check pays no network round trip) is now the
// order of the statements rather than the order of two strings in one script.

// An atom that found no surface said so on stdout. Reading that as a pass is
// the same conflation as reading a could-not-run as a pass, one shelf up.
func TestVerdictOfCarriesAnAtomsOwnAbsence(t *testing.T) {
	a := AtomByID("sweep:digest-pins")
	out := "sweep:digest-pins: ABSENT - .forgejo/workflows carries no digest reference at all.\n"

	v := VerdictOf(a, 0, out)
	if v.Result != "absent" {
		t.Fatalf("an atom that announced ABSENT rendered as %q", v.Result)
	}
	if v.State != int(StatePass) {
		t.Fatalf("an absence is not a failure; state was %d", v.State)
	}
	if !strings.Contains(v.Reason, "no digest reference") {
		t.Fatalf("the absence must carry the atom's own reason, got %q", v.Reason)
	}
	if _, err := v.Answer(); err != nil {
		t.Fatalf("an absence must answer nil, got %v", err)
	}
}

// The prefix is the atom's id for a reason: this atom greps a tree, and the
// word it greps for could appear in the tree it is grepping.
func TestVerdictOfDoesNotReadAnotherAtomsAbsence(t *testing.T) {
	a := AtomByID("sweep:digest-pins")
	for _, out := range []string{
		"fleet:opengrep-sast: ABSENT - no rules/sast in this tree\n",
		"the word ABSENT appears in a scanned file\n",
		"",
	} {
		if v := VerdictOf(a, 0, out); v.Result != "pass" {
			t.Errorf("output %q rendered as %q, want pass", out, v.Result)
		}
	}
}

// A non-pass keeps the full three-state reason. The absence branch must not
// swallow a findings or a could-not-run.
func TestAbsenceNeverMasksANonPass(t *testing.T) {
	a := AtomByID("sweep:digest-pins")
	out := "sweep:digest-pins: ABSENT - no .forgejo/workflows in this tree\n"
	if v := VerdictOf(a, 2, out); v.Result != "cannot-run" {
		t.Errorf("exit 2 rendered as %q — an absence claim must not outrank a refusal", v.Result)
	}
	if v := VerdictOf(a, 1, out); v.Result != "findings" {
		t.Errorf("exit 1 rendered as %q", v.Result)
	}
}
