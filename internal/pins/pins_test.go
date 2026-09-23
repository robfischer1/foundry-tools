package pins

import (
	"encoding/json"
	"strings"
	"testing"
)

func full(b byte) string { return "sha256:" + strings.Repeat(string(b), 64) }

// decode reads the line back the way the DOOR reads it: strip the marker,
// unmarshal the payload. A test that asserted on the raw string would pass
// on a line no parser accepts.
func decode(t *testing.T, line string) []Pin {
	t.Helper()
	body, ok := strings.CutPrefix(line, MarkerPrefix)
	if !ok {
		t.Fatalf("line %q does not open with the marker", line)
	}
	var doc struct {
		Pins []Pin `json:"pins"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("the payload is not JSON: %v (%q)", err, body)
	}
	return doc.Pins
}

// TestTheLineIsOneLineAndParsesBack — the transport is an echo into a pod log,
// which is line-oriented: a declaration split across two lines is a
// declaration the door finds half of. Nothing in the payload may introduce a
// newline, including an artifact name that somehow carries one.
func TestTheLineIsOneLineAndParsesBack(t *testing.T) {
	line, skipped := Line(
		Pin{Artifact: "registry.notusmi.com/rob/ourea", Digest: full('a'), Tag: "stable"},
		Pin{Artifact: "registry.notusmi.com/foundry/base-images/go", Digest: full('b'), Tag: "g0123456789ab"},
	)
	if len(skipped) != 0 {
		t.Errorf("skipped = %v; both are well formed", skipped)
	}
	if strings.ContainsAny(line, "\n\r") {
		t.Fatalf("the declaration spans lines: %q", line)
	}
	got := decode(t, line)
	if len(got) != 2 {
		t.Fatalf("pins = %+v", got)
	}
	if got[0].Artifact != "registry.notusmi.com/rob/ourea" || got[0].Tag != "stable" || got[0].Kind != KindImage {
		t.Errorf("pins[0] = %+v", got[0])
	}
	if got[1].Digest != full('b') {
		t.Errorf("pins[1].Digest = %q", got[1].Digest)
	}
}

// TestAnUncheckableDigestIsNEVERPrinted is the refusal that matters. A row in
// build_pins asserts an artifact exists at a digest; a digest that cannot be
// resolved against a registry makes that assertion unfalsifiable, which is
// worse than an absent row — the next build would write the real one.
func TestAnUncheckableDigestIsNEVERPrinted(t *testing.T) {
	for _, digest := range []string{
		"sha256:" + strings.Repeat("ab", 6), // the abbreviated 12-hex form prose carries
		strings.Repeat("ab", 32),            // 64 hex, but no algorithm names it
		"sha512:" + strings.Repeat("a", 64), // not the algorithm this fleet uses
		"sha256:" + strings.Repeat("A", 64), // hex is lower case
		"sha256:" + strings.Repeat("a", 63), // one short
		"sha256:" + strings.Repeat("a", 65), // one long
		"",
	} {
		line, skipped := Line(Pin{Artifact: "registry.notusmi.com/rob/x", Digest: digest})
		if line != "" {
			t.Errorf("%q was declared: %q", digest, line)
		}
		if len(skipped) != 1 || !strings.Contains(skipped[0], "not sha256 and 64 hex") {
			t.Errorf("%q was refused without saying why: %v", digest, skipped)
		}
	}
}

// TestAGoodPinSurvivesABadNeighbour — the lane has already published
// everything it is declaring. Dropping the whole line over one bad entry
// loses the record of artifacts that really exist.
func TestAGoodPinSurvivesABadNeighbour(t *testing.T) {
	line, skipped := Line(
		Pin{Artifact: "registry.notusmi.com/rob/good", Digest: full('c')},
		Pin{Artifact: "", Digest: full('d')},
		Pin{Artifact: "registry.notusmi.com/rob/odd", Digest: full('e'), Kind: "sculpture"},
	)
	got := decode(t, line)
	if len(got) != 1 || got[0].Artifact != "registry.notusmi.com/rob/good" {
		t.Fatalf("pins = %+v", got)
	}
	if len(skipped) != 2 {
		t.Fatalf("skipped = %v, want 2", skipped)
	}
	for _, want := range []string{"no artifact", "is not image, package or bundle"} {
		found := false
		for _, s := range skipped {
			if strings.Contains(s, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no refusal mentions %q: %v", want, skipped)
		}
	}
}

// TestNothingToDeclareIsAnEmptyLine — a lane calls this whether or not it has
// anything, so the no-pin answer must be printable as nothing at all.
func TestNothingToDeclareIsAnEmptyLine(t *testing.T) {
	if line, skipped := Line(); line != "" || len(skipped) != 0 {
		t.Errorf("Line() = %q, %v; want silence", line, skipped)
	}
	if line, _ := Line(Pin{}); line != "" {
		t.Errorf("an empty pin declared %q", line)
	}
}

// TestImageSplitsTheTagOffTheTarget — and specifically does NOT mistake a
// registry port for a tag. registry:5000/rob/x is a repository with no tag;
// splitting at the first or last colon blindly turns it into "5000/rob/x"
// tagged nothing, and every row for that registry would name the wrong
// artifact.
func TestImageSplitsTheTagOffTheTarget(t *testing.T) {
	for _, c := range []struct{ target, artifact, tag string }{
		{"registry.notusmi.com/rob/ourea:stable", "registry.notusmi.com/rob/ourea", "stable"},
		{"registry.notusmi.com/rob/ourea:g0123456789ab", "registry.notusmi.com/rob/ourea", "g0123456789ab"},
		{"registry.notusmi.com/rob/ourea", "registry.notusmi.com/rob/ourea", ""},
		{"registry.notusmi.com:5000/rob/ourea", "registry.notusmi.com:5000/rob/ourea", ""},
		{"registry.notusmi.com:5000/rob/ourea:stable", "registry.notusmi.com:5000/rob/ourea", "stable"},
		{"registry.notusmi.com/foundry/base-images/go:stable", "registry.notusmi.com/foundry/base-images/go", "stable"},
		// NEITHER a colon nor a slash: both indexes are -1, and a `>=` here
		// would slice target[:-1] and panic. The lane has already published
		// by the time this runs.
		{"ourea", "ourea", ""},
		{"ourea:stable", "ourea", "stable"},
	} {
		p := Image(c.target, full('a'))
		if p.Artifact != c.artifact || p.Tag != c.tag {
			t.Errorf("Image(%q) = {%q, %q}, want {%q, %q}", c.target, p.Artifact, p.Tag, c.artifact, c.tag)
		}
		if p.Kind != KindImage || p.Digest != full('a') {
			t.Errorf("Image(%q) = %+v", c.target, p)
		}
	}
}

// TestWhatALaneEchoedIsTrimmed — the digest reaches this from a regexp over an
// engine's output and the target from a string join; a stray space must not
// refuse a real pin.
func TestWhatALaneEchoedIsTrimmed(t *testing.T) {
	line, skipped := Line(Pin{Artifact: "  registry.notusmi.com/rob/ourea ", Digest: " " + full('a') + "  ", Tag: " stable "})
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v; whitespace must not refuse a real pin", skipped)
	}
	got := decode(t, line)
	if got[0].Artifact != "registry.notusmi.com/rob/ourea" || got[0].Digest != full('a') || got[0].Tag != "stable" {
		t.Errorf("not trimmed: %+v", got[0])
	}
}

// TestAnOmittedRoleIsBuiltAndNeverDep — the compatibility hinge. Every lane in
// the fleet declared pins before `role` existed, and each of those lines meant
// "I published this". Lines like that are still in logs the door has not read.
// A default of dep would reclassify them into the dependency half, where
// BuiltHere stops answering for them — the admission question would start
// saying no about images the fleet demonstrably built.
func TestAnOmittedRoleIsBuiltAndNeverDep(t *testing.T) {
	line, skipped := Line(Pin{Artifact: "registry.notusmi.com/rob/ourea", Digest: full('a')})
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v; a pin with no role is well formed", skipped)
	}
	// IT COMES BACK EMPTY ON THE WIRE, and that is the point: both ends read
	// empty as built, so the line does not carry ~16 bytes per pin to restate
	// it. The door defaults it on arrival.
	got := decode(t, line)
	if len(got) != 1 || got[0].Role != "" {
		t.Errorf("role = %+v, want it left off the wire", got)
	}
	if strings.Contains(line, `"role"`) {
		t.Errorf("the default role was written out; the tail window is the one place bytes cost:\n%s", line)
	}
}

// TestARoleThatIsNeitherIsREFUSEDBYNAME — the same contract the kind check
// holds. A row the door cannot classify is worse than no row: it would land in
// a table whose whole point is that the two halves stay tellable apart.
func TestARoleThatIsNeitherIsREFUSEDBYNAME(t *testing.T) {
	line, skipped := Line(
		Pin{Artifact: "registry.notusmi.com/rob/good", Digest: full('b'), Role: RoleDep},
		Pin{Artifact: "registry.notusmi.com/rob/bad", Digest: full('c'), Role: "consumed"},
	)
	if len(skipped) != 1 || !strings.Contains(skipped[0], "role consumed is not built or dep") {
		t.Fatalf("skipped = %v; the refusal must name the role it refused", skipped)
	}
	got := decode(t, line)
	if len(got) != 1 || got[0].Role != RoleDep {
		t.Errorf("the good neighbour did not survive: %+v", got)
	}
}

// TestConsumedReadsWhatADockerfileDEPENDSOn — the dependency half at its
// source. A FROM by digest is the thing the reaper can delete out from under
// the next rebuild, and it is the shape of incident #25.
func TestConsumedReadsWhatADockerfileDEPENDSOn(t *testing.T) {
	dockerfile := `
FROM docker.io/library/golang:1.27.1-bookworm@` + full('1') + ` AS boot
FROM foundry.notusmi.com/foundry/base-images/go:stable@` + full('2') + `
COPY --from=registry.notusmi.com/rob/tools:v1@` + full('3') + ` /bin/x /bin/x
FROM foundry.notusmi.com/foundry/base-images/go:stable@` + full('2') + `
FROM registry.notusmi.com/rob/untagged@` + full('4') + `
FROM registry.notusmi.com/rob/bytag:stable
`
	// THREE, not six. The docker.io FROM is not ours and cannot be reaped by
	// our retention; the second mention of the base collapses into the first;
	// and the tag-only reference names nothing a dep row could hold down.
	got := Consumed(dockerfile)
	if len(got) != 3 {
		t.Fatalf("got %d pins, want 3:\n%+v", len(got), got)
	}
	for _, p := range got {
		if p.Role != RoleDep {
			t.Errorf("%s came back role %q, want %q", p.Artifact, p.Role, RoleDep)
		}
		if strings.HasPrefix(p.Artifact, "docker.io/") {
			t.Errorf("an UPSTREAM image was recorded as a dependency: %s — "+
				"only our own registry's digests can be reaped by our own retention", p.Artifact)
		}
	}
	// The base named twice in two stages is ONE dependency.
	base := 0
	for _, p := range got {
		if p.Digest == full('2') {
			base++
			if p.Tag != "stable" || p.Artifact != "foundry.notusmi.com/foundry/base-images/go" {
				t.Errorf("the tag was not split off the target: %+v", p)
			}
		}
	}
	if base != 1 {
		t.Errorf("the same base in two stages made %d pins, want 1", base)
	}
	// A reference by TAG ALONE is not a pin — retention keeps a movable tag
	// unconditionally, so there is nothing for a dep row to hold down.
	for _, p := range got {
		if strings.Contains(p.Artifact, "bytag") {
			t.Errorf("a tag-only reference became a pin: %+v", p)
		}
	}
	// A digest with no tag still counts; it is the harder one to keep alive.
	found := false
	for _, p := range got {
		if p.Digest == full('4') && p.Tag == "" {
			found = true
		}
	}
	if !found {
		t.Error("an untagged digest reference was dropped; it is the one most at risk of reaping")
	}
}

// TestAFileWithNoInternalDigestDependsOnNothing — a star that builds FROM
// scratch or from upstream alone declares no dependency, and that silence is
// correct rather than a gap.
func TestAFileWithNoInternalDigestDependsOnNothing(t *testing.T) {
	for _, text := range []string{
		"",
		"FROM scratch\nCOPY x /x\n",
		"FROM docker.io/library/alpine@" + full('9') + "\n",
		"FROM registry.notusmi.com/rob/x:stable\n",
		"# registry.notusmi.com/rob/x@sha256:short\n",
	} {
		if got := Consumed(text); len(got) != 0 {
			t.Errorf("Consumed(%q) = %+v; want nothing", text, got)
		}
	}
}
