package checks

import (
	"path"
	"strings"
	"testing"
)

// THE MESSAGE AND THE PATTERN LIST MUST AGREE. The FINDINGS the atom prints
// names bun's pattern in prose — {.test,.spec,_test_,_spec_}.{js,ts,jsx,tsx} —
// and a reader takes that sentence as the definition of what was looked for.
// A pattern dropped from the list without the sentence changing is a check
// that quietly stopped looking, which is the drift this table refuses.
func TestTSTestPatternsCoverBunsOwnPattern(t *testing.T) {
	samples := []string{
		"a.test.ts", "a.test.tsx", "a.test.js", "a.test.jsx",
		"a.spec.ts", "a.spec.tsx", "a.spec.js", "a.spec.jsx",
		"src/deep/nested/b.test.ts", "src/b.spec.js",
		"my_test_helpers.ts", "my_spec_fixture.js",
	}
	for _, s := range samples {
		if !matchesAny(TSTestPatterns, s) {
			t.Errorf("no pattern in TSTestPatterns matches %q", s)
		}
	}
	for _, s := range []string{"main.ts", "src/index.tsx", "testing.ts", "spec.md"} {
		if matchesAny(TSTestPatterns, s) {
			t.Errorf("TSTestPatterns matched %q, which is not a test file", s)
		}
	}
}

// A ROOT-LEVEL TEST FILE MUST MATCH. The shell body walked from `.`, so a
// `a.test.ts` beside package.json counted; every pattern here is anchored with
// `**/` so the glob means the same thing.
func TestEveryTSTestPatternIsRootAnchored(t *testing.T) {
	for _, p := range TSTestPatterns {
		if !strings.HasPrefix(p, "**/") {
			t.Errorf("%q is not anchored with **/, so a test file at the repository root would not match", p)
		}
	}
}

// RulesetsDir is where the ruleset atoms WriteNewFile their embedded config and
// where the tool is then told to read it. It has to be absolute (a container
// path, not a path relative to whatever workdir the lane happens to set) and it
// has to sit outside /src, because /src is a mounted directory and a
// WithNewFile under a mount is shadowed by it.
func TestRulesetsDirIsAbsoluteAndOutsideTheSourceMount(t *testing.T) {
	if !strings.HasPrefix(RulesetsDir, "/") {
		t.Fatalf("RulesetsDir = %q, want an absolute container path", RulesetsDir)
	}
	if RulesetsDir == "/src" || strings.HasPrefix(RulesetsDir, "/src/") {
		t.Fatalf("RulesetsDir = %q is under the /src mount, which shadows WithNewFile", RulesetsDir)
	}
}

// matchesAny applies the patterns the way a doublestar glob does for this
// shape: `**/` matches zero or more leading path segments, so the rest of the
// pattern is matched against the basename.
func matchesAny(patterns []string, name string) bool {
	base := path.Base(name)
	for _, p := range patterns {
		ok, err := path.Match(strings.TrimPrefix(p, "**/"), base)
		if err == nil && ok {
			return true
		}
	}
	return false
}
