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
