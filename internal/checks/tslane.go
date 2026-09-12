package checks

// The ts lane's facts, in the one package a test can reach.

// StocksRulesets is the fleet's ruleset directory RELATIVE TO the foundry-stocks
// tree, for reading a file straight off the Directory in Go; RulesetsDir is the
// same directory as a container sees it, under the /stocks mount, for the
// message an atom prints when the file is not there. They are spelled once and
// derived from each other so the two halves cannot drift.
//
// A tool pointed at a file here ignores whatever config the repository carries,
// which is the point (Rob, 2026-09-11: the fleet decides the atoms AND their
// rulesets). A ruleset the atom cannot read is a gate that never looked, and
// that is never a pass.
const (
	StocksRulesets = "ci/lib/rulesets"
	// Spelled out rather than composed: a package-level `+` is a line no
	// test can cover, so the mutation lane reads it as NOT COVERED forever.
	// TestRulesetsDirIsTheLiteralPathTheAtomsPrint holds the two together.
	RulesetsDir = "/stocks/ci/lib/rulesets"
)

// TSTestPatterns is BUN'S OWN TEST-FILE PATTERN as globs over the gate's
// population — {.test,.spec,_test_,_spec_} × {js,ts,jsx,tsx}.
//
// The presence check is the ATOM'S rather than bun's. `bun test` does refuse a
// tree with no test file (exit 1, "0 test files matching"), but ts:bun-gate
// runs the repo's own `gate` script, which may never reach `bun test` at all —
// so a tree with no test could run a green gate that tested nothing. Rob,
// 2026-09-11: nothing is built without tests.
//
// Every pattern is anchored with `**/` because the population is repo-root
// relative and a test file at the root must match as surely as one three
// directories down; node_modules is already gone before these are applied
// (GateExclude), which is what the shell body's `-path ./node_modules -prune`
// was doing by hand.
var TSTestPatterns = []string{
	"**/*.test.ts", "**/*.test.tsx", "**/*.test.js", "**/*.test.jsx",
	"**/*.spec.ts", "**/*.spec.tsx", "**/*.spec.js", "**/*.spec.jsx",
	"**/*_test_*", "**/*_spec_*",
}
