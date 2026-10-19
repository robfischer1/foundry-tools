package checks

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/checks/rulesets"
)

// ForkSastDir is where fleet:opengrep-sast lays the built-in rulesets in its
// container, outside the tree so the scan never reads its own rules.
const ForkSastDir = "/fork-sast"

// SastRuleset is one built-in SAST ruleset: the file it is laid as, and its
// body.
type SastRuleset struct {
	File string
	Body string
}

// forkSastLangs is the fleet's SAST rules by language, in the order they are
// laid. Exts are the file extensions that make a tree "have" the language.
//
// THE LANE IS READ OFF THE FILES, NOT THE ROOT MANIFEST. fleet:sast-ruleset-lanes
// maps root manifests (go.mod, Cargo.toml, pyproject.toml, package.json) to
// languages, because a star's rules/sast has to name every lane the star
// builds. A fork has no rules/sast to audit, and its layout is its upstream's:
// gravity is a Cargo workspace at the root with its Go modules under
// examples/ and validation/, no root go.mod. Keyed on manifests the Go lane
// would be silently unscanned, the partial scan that atom exists to catch.
var forkSastLangs = []struct {
	File string
	Body string
	Exts []string
}{
	{"go.yml", rulesets.SastGo, []string{".go"}},
	{"rust.yml", rulesets.SastRust, []string{".rs"}},
	{"python.yml", rulesets.SastPython, []string{".py"}},
	{"frontend.yml", rulesets.SastFrontend, []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}},
}

// ForkSastGlobs are the patterns that list the files ForkSastRulesets reads,
// for a caller that has to ask the engine rather than hold a file list.
func ForkSastGlobs() []string {
	var globs []string
	for _, l := range forkSastLangs {
		for _, e := range l.Exts {
			globs = append(globs, "**/*"+e)
		}
	}
	return globs
}

// ForkSastRulesets are the built-in rulesets for the languages files holds.
// Empty when the tree holds none of them.
func ForkSastRulesets(files []string) []SastRuleset {
	have := map[string]bool{}
	for _, f := range files {
		have[path.Ext(f)] = true
	}
	var out []SastRuleset
	for _, l := range forkSastLangs {
		for _, e := range l.Exts {
			if have[e] {
				out = append(out, SastRuleset{File: l.File, Body: l.Body})
				break
			}
		}
	}
	return out
}

// ForkSastRefusal is the zero-file refusal for the built-in scan. The
// star's wording tells the reader to fix rules/sast, which a fork does not have.
const ForkSastRefusal = "opengrep: REFUSING a zero-file scan. The fleet's built-in rulesets matched no file in this fork, so 0 findings means NOTHING WAS EXAMINED - not that the code is clean."

var findingsLine = regexp.MustCompile(`Ran [0-9]+ rules on [0-9]+ files: ([0-9]+) findings?`)

// OpengrepFindingCount is the count opengrep's summary line reports, or -1
// when the output carries no such line.
func OpengrepFindingCount(out string) int {
	m := findingsLine.FindStringSubmatch(out)
	if m == nil {
		return -1
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return -1
	}
	return n
}

// ForkSastVerdict grades a built-in scan of a fork (record is the fork's
// record name, sets the rulesets it ran, out and code opengrep's output and
// exit under --error).
//
// FINDINGS IN A FORK ARE ADVISORY: a PASS (state 0, the exit 0 the door reads)
// whose reason carries them. Rob, 2026-10-13: findings in upstream-owned code
// must not hold a sync hostage to upstream's code. THE VOCABULARY HAS NO
// FOURTH STATE and none was added, because the record and slag consumers parse
// it: state 0 with the findings in the reason is what fleet:witness already
// does for an advisory (WitnessSummary). Verdict.Findings stays empty, since
// that field means "what this atom found" and the reader counts it as red.
//
// EVERYTHING ELSE KEEPS ITS STATE. A scan that examined no file is still a
// cannot-run, and an opengrep that failed (exit 2 and up) is still one: a fork
// is excused upstream's findings, not an unexamined tree.
func ForkSastVerdict(a AtomDef, record string, sets []SastRuleset, out string, code int) Verdict {
	if OpengrepZeroFiles(out) {
		return VerdictOf(a, int(StateCannotRun), out+"\n"+ForkSastRefusal)
	}
	if code != 0 && code != int(StateFindings) {
		return VerdictOf(a, code, out)
	}
	names := make([]string, len(sets))
	for i, s := range sets {
		names[i] = strings.TrimSuffix(s.File, ".yml")
	}
	scope := fmt.Sprintf("fleet/repos/%s.json is kind vendored, a fork of an upstream, with no rules/sast in its tree: scanned with the fleet's built-in rulesets [%s]", record, strings.Join(names, " "))
	v := VerdictOf(a, int(StatePass), out)
	if code == 0 {
		v.Reason = fmt.Sprintf("%s: PASS - %s; no findings.", a.ID, scope)
		return v
	}
	n := "some"
	if c := OpengrepFindingCount(out); c >= 0 {
		n = strconv.Itoa(c)
	}
	v.Reason = fmt.Sprintf("%s: ADVISORY - %s; %s finding(s) in code this fork mostly inherits, reported and NOT blocking.\n%s", a.ID, scope, n, BoundOutput(strings.TrimSpace(out)))
	return v
}
