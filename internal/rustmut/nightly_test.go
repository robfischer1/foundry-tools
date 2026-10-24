package rustmut

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// A night with nothing new in rust still re-tests its carried survivor:
// cargo-mutants' -D diff adds the carried line with the tree's own text, and its
// crate is named.
func TestNightlyACarriedRustSurvivorIsGradedWhenNothingNewChanged(t *testing.T) {
	p := newPull(t, "")
	t.Setenv(checks.EnvMutationRanges, "src/lib.rs:1-1,README.md:1-1")
	p.first(rule{"git diff --relative since0", "", 0})
	p.first(rule{"--name-only -z", "", 0})
	v := p.run()
	if strings.Contains(said(v), "nothing to mutate") || !p.ran("cargo mutants --colors") {
		t.Fatalf("a night with a carried survivor stood down: %s", said(v))
	}
	raw, _ := os.ReadFile(filepath.Join(p.work, "rust", "pr.diff"))
	if !strings.Contains(string(raw), "+++ b/src/lib.rs\n@@ -0,0 +1,1 @@\n+fn f() -> i32 { 2 }\n") || strings.Contains(string(raw), "README") {
		t.Fatalf("pr.diff = %q", raw)
	}
}

// Ranges that do not parse are a could-not-run.
func TestNightlyMalformedRustRangesCannotRun(t *testing.T) {
	p := newPull(t, "")
	t.Setenv(checks.EnvMutationRanges, "src/lib.rs")
	if v := p.run(); v.State != 2 || !strings.Contains(said(v), "the carried ranges") {
		t.Fatalf("%s", said(v))
	}
}
