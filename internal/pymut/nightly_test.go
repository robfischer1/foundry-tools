package pymut

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// A night with nothing new in python still re-tests its carried survivor: the
// scope diff the testkit reads adds the carried line, and its file is in scope.
func TestNightlyACarriedPythonSurvivorIsGradedWhenNothingNewChanged(t *testing.T) {
	p := newPull(t, "")
	putFile(t, p.in.Root, "src/y.py", "a = 1\nb = 2\n")
	t.Setenv(checks.EnvMutationRanges, "src/y.py:2-2,src/z.go:1-1")
	p.first(rule{"--name-only --diff-filter=AM", "", 0, ""})
	p.first(rule{"--no-ext-diff", "", 0, ""})
	p.first(rule{"diff -M --unified=0 since0", "", 0, ""})
	v := p.run()
	if strings.Contains(said(v), "nothing to mutate") {
		t.Fatalf("a night with a carried survivor stood down: %s", said(v))
	}
	raw, _ := os.ReadFile(filepath.Join(p.work, "python", "pr.diff"))
	if !strings.Contains(string(raw), "+++ b/src/y.py\n@@ -0,0 +2,1 @@\n+b = 2\n") || strings.Contains(string(raw), "z.go") {
		t.Fatalf("pr.diff = %q (%s)", raw, said(v))
	}
}

// Ranges that do not parse are a could-not-run.
func TestNightlyMalformedPythonRangesCannotRun(t *testing.T) {
	p := newPull(t, "")
	t.Setenv(checks.EnvMutationRanges, "../x.py:1-1")
	if v := p.run(); v.State != 2 || !strings.Contains(said(v), "the carried ranges") {
		t.Fatalf("%s", said(v))
	}
}
