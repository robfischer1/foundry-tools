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

// The rulesets directory is spelled once. RulesetsDir is what a container sees
// and StocksRulesets is the same path inside the foundry-stocks tree; a change
// to either that does not carry the other is the two-spellings defect.
func TestRulesetsDirIsTheStocksPathUnderTheMount(t *testing.T) {
	if RulesetsDir != "/stocks/"+StocksRulesets {
		t.Fatalf("RulesetsDir = %q, want /stocks/%s", RulesetsDir, StocksRulesets)
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

// AND IT IS THE PATH THE ATOMS PRINT. The assertion above is
// self-referential — an edit to StocksRulesets moves both of its sides at once
// — so the two spellings are also held against their literals: /stocks is the
// mount runtime.go's withStocks makes, and ci/lib/rulesets is where
// foundry-stocks keeps them. A change to either is a change to a message a
// human reads off a red gate, and to where the file is looked for.
func TestRulesetsDirIsTheLiteralPathTheAtomsPrint(t *testing.T) {
	if StocksRulesets != "ci/lib/rulesets" {
		t.Errorf("StocksRulesets = %q, want ci/lib/rulesets", StocksRulesets)
	}
	if RulesetsDir != "/stocks/ci/lib/rulesets" {
		t.Errorf("RulesetsDir = %q, want /stocks/ci/lib/rulesets", RulesetsDir)
	}
}
