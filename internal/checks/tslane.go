package checks

// The ts lane's facts, in the one package a test can reach.

// RulesetsDir is where the atoms WRITE the fleet's rulesets into their
// container. It was /stocks/ci/lib/rulesets, a git mount of foundry-stocks,
// until 2026-09-23 (CA F18): the files are embedded in this module now
// (internal/checks/rulesets), so they arrive with the atom that reads them
// rather than being fetched from a repo that can drift from it.
//
// A tool pointed at a file here ignores whatever config the repository under
// test carries, which is the point: the fleet decides the atoms AND their
// rulesets. What changed is only WHERE the fleet keeps them.
//
// THE "CANNOT READ THE RULESET" BRANCH IS GONE WITH THE MOUNT, and that is the
// real gain rather than the deleted lines. It used to be a live could-not-run:
// a mount that did not arrive meant a gate that never looked. An embedded file
// cannot be absent — it is a compile error — so the failure mode is now
// unreachable instead of merely handled.
const RulesetsDir = "/rulesets"

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
