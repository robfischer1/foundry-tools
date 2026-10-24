package tsmut

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// A carried ts survivor joins the ranges Stryker is handed; a file of another
// language does not.
func TestNightlyACarriedTSSurvivorJoinsStrykersMutate(t *testing.T) {
	p := newPull(t, "")
	t.Setenv(checks.EnvMutationRanges, "src/gate.ts:5-5,src/x.py:1-1")
	p.run()
	c, ok := p.call("--mutate")
	line := strings.Join(c.Args, " ")
	if !ok || !strings.Contains(line, "src/gate.ts:5-5") || strings.Contains(line, "x.py") {
		t.Fatalf("stryker was handed %q (ran %v)", line, ok)
	}
}

// A night with nothing new in ts still mutates its carried line.
func TestNightlyACarriedTSSurvivorIsGradedWhenNothingNewChanged(t *testing.T) {
	p := newPull(t, "")
	t.Setenv(checks.EnvMutationRanges, "src/gate.ts:5-5")
	p.first(rule{"git diff --unified=0", "", 0})
	v := p.run()
	if strings.Contains(said(v), "nothing to mutate") {
		t.Fatalf("stood down: %s", said(v))
	}
	if c, ok := p.call("--mutate"); !ok || !strings.Contains(strings.Join(c.Args, " "), "src/gate.ts:5-5") {
		t.Fatalf("stryker was handed %v", c.Args)
	}
	if !tsSource("a/b.mjs") || tsSource("a/b.py") {
		t.Fatal("tsSource")
	}
}

// Ranges that do not parse are a could-not-run.
func TestNightlyMalformedTSRangesCannotRun(t *testing.T) {
	p := newPull(t, "")
	t.Setenv(checks.EnvMutationRanges, "src/gate.ts:9-1")
	if v := p.run(); v.State != 2 || !strings.Contains(said(v), "the carried ranges") {
		t.Fatalf("%s", said(v))
	}
}
