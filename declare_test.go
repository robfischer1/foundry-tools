package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/pins"
)

// sayings captures what the lane printed. say writes to os.Stderr and reads
// the variable at call time, so swapping it is enough; the engine gives a
// lane no other output surface and neither does a test.
func sayings(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = old }()
	f()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// TestDeclarePrintsTheLineTheDoorReads — the declaration reaches ourea as one
// line of this pod's log and nothing else. A lane that computed the right pin
// and printed it somewhere unreadable has published an unrecorded artifact,
// which is exactly the gap build_pins exists to close.
func TestDeclarePrintsTheLineTheDoorReads(t *testing.T) {
	out := sayings(t, func() {
		declare(pins.Image("registry.notusmi.com/rob/ourea:stable", "sha256:"+strings.Repeat("a", 64)))
	})
	var found string
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, pins.MarkerPrefix); i >= 0 {
			found = line[i:]
		}
	}
	if found == "" {
		t.Fatalf("no declaration in the lane's output:\n%s", out)
	}
	for _, want := range []string{`"artifact":"registry.notusmi.com/rob/ourea"`, `"tag":"stable"`, `"kind":"image"`} {
		if !strings.Contains(found, want) {
			t.Errorf("the declaration is missing %s: %s", want, found)
		}
	}
}

// TestDeclareSaysWhatItWouldNotPrint — a lane that computed a pin it cannot
// declare is a lane with a bug, and the operator reading the log is the only
// one who will ever see it. Silence there is how a whole class of artifact
// goes unrecorded without anyone noticing.
func TestDeclareSaysWhatItWouldNotPrint(t *testing.T) {
	out := sayings(t, func() {
		declare(pins.Image("registry.notusmi.com/rob/ourea:stable", "sha256:fc4f0fa1610b"))
	})
	if strings.Contains(out, pins.MarkerPrefix) {
		t.Errorf("an uncheckable digest was declared: %s", out)
	}
	if !strings.Contains(out, "NOT declaring a pin") || !strings.Contains(out, "not sha256 and 64 hex") {
		t.Errorf("the refusal was silent: %s", out)
	}
}

// TestDeclaringNothingPrintsNothing — declare is called on a path that may
// have nothing to say, and a bare marker with no payload would be a line the
// door has to decide about.
func TestDeclaringNothingPrintsNothing(t *testing.T) {
	if out := sayings(t, func() { declare() }); out != "" {
		t.Errorf("declare() printed %q", out)
	}
}

// TestALaneDeclaresEveryPinInOneLineAtTheEnd — the shape that makes the
// door's log-tail bound survivable.
//
// Declared at push time, ourea's tip 0585814 put the marker 252,946 bytes
// before the end of a 277,219-byte log against a 65,536-byte window, and the
// row was never written. Accumulating and emitting once means the marker is
// the lane's last word, so the distance from the end is bounded by the line
// itself rather than by everything a build does after pushing.
func TestALaneDeclaresEveryPinInOneLineAtTheEnd(t *testing.T) {
	l := &buildLane{}
	// Three pushes, as a base build does: the g-pin, then the :stable move,
	// then a second artifact.
	for _, c := range []struct{ target, digest string }{
		{"registry.notusmi.com/foundry/base-images/go:g0123456789ab", "sha256:" + strings.Repeat("a", 64)},
		{"registry.notusmi.com/foundry/base-images/go:stable", "sha256:" + strings.Repeat("a", 64)},
		{"registry.notusmi.com/rob/ourea:stable", "sha256:" + strings.Repeat("b", 64)},
	} {
		l.published = append(l.published, pins.Image(c.target, c.digest))
	}

	out := sayings(t, l.declarePublished)
	var marked []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, pins.MarkerPrefix) {
			marked = append(marked, line)
		}
	}
	if len(marked) != 1 {
		t.Fatalf("%d marker lines, want exactly 1 — a per-push declaration is what put the\nmarker outside the door's tail window:\n%s", len(marked), out)
	}
	// Every pin is in that one line, each keeping its own tag.
	for _, want := range []string{`"tag":"g0123456789ab"`, `"tag":"stable"`, `"artifact":"registry.notusmi.com/rob/ourea"`} {
		if !strings.Contains(marked[0], want) {
			t.Errorf("the declaration is missing %s:\n%s", want, marked[0])
		}
	}
}

// TestALaneThatPublishedNothingSaysNothing — a pull-time build publishes
// nothing, and the deferred call still runs on every path.
func TestALaneThatPublishedNothingSaysNothing(t *testing.T) {
	l := &buildLane{}
	if out := sayings(t, l.declarePublished); out != "" {
		t.Errorf("a lane that published nothing said %q", out)
	}
}
