package checks

import (
	"regexp"
	"strings"
)

// pythonProductDirs are the two directories the fleet calls PRODUCT python.
// Order matters: it is the order the shell loop walked (`for d in src tests`),
// and it is the order the targets reach ruff and mypy.
var pythonProductDirs = []string{"src", "tests"}

// PythonSourceDirs answers which of src/ and tests/ these repository-root
// entries carry, in that order.
//
// Dagger names a directory entry with a trailing slash in some contexts and
// without in others; both spellings are accepted here so the answer is a fact
// about the tree rather than about which call produced the list.
func PythonSourceDirs(entries []string) []string {
	present := map[string]bool{}
	for _, e := range entries {
		present[strings.TrimSuffix(strings.TrimPrefix(e, "./"), "/")] = true
	}
	var out []string
	for _, d := range pythonProductDirs {
		if present[d] {
			out = append(out, d)
		}
	}
	return out
}

// RuffFormatTargets answers what `ruff format --check` is pointed at, and
// whether there is anything to point it at.
//
// THE PATH SCOPE IS THE MEASUREMENT THE GO STARS' HOOK RECORDS: incidental
// python (scripts/, conformance recorders) is linted but not formatted;
// product python under src/ and tests/ is. So a tree that declares the Go lane
// — go.mod at its root — is formatted only where its product python lives, and
// a go star with none is ABSENT rather than a pass over an empty set. Every
// other tree formats itself whole ("."), which is what the python stars want.
//
// The bool is "there is something to format": false is the ABSENT the caller
// announces, never an empty command line.
func RuffFormatTargets(entries []string) ([]string, bool) {
	goStar := false
	for _, e := range entries {
		if strings.TrimPrefix(e, "./") == "go.mod" {
			goStar = true
			break
		}
	}
	if !goStar {
		return []string{"."}, true
	}
	dirs := PythonSourceDirs(entries)
	if len(dirs) == 0 {
		return nil, false
	}
	return dirs, true
}

// forgeTestkitDep is the forge-testkit DEPENDENCY ENTRY as it appears in a
// pyproject.toml dependency array: a quoted "forge-testkit" optionally
// followed by a version specifier, an extras bracket, or the closing quote.
//
// A BARE WORD IS NOT A DEPENDENCY. Measured on helios by Lovelace13,
// 2026-09-10: the first cut of this check grepped the bare word, so a comment
// naming the package — and go.mod's unrelated forge-testkit-go — read as a
// declared dependency and the atom ran a linter the repo had never installed.
// The leading quote is what separates the entry from the mention, and the
// character class after the name is what separates forge-testkit from
// forge-testkit-anything-else.
var forgeTestkitDep = regexp.MustCompile(`^[[:space:]]*"forge-testkit([<>=!~ \["]|$)`)

// DeclaresForgeTestkit reports whether this pyproject.toml TAKES THE
// DEPENDENCY. A repo that never took it has nothing for forge-testkit-lint to
// read, and the atom says ABSENT rather than provisioning a tool to find out.
func DeclaresForgeTestkit(pyproject string) bool {
	for _, line := range strings.Split(pyproject, "\n") {
		if forgeTestkitDep.MatchString(strings.TrimRight(line, "\r")) {
			return true
		}
	}
	return false
}

// PytestState maps pytest's own exit code to this atom's state and, where the
// code means something pytest's output does not say plainly, the reason.
//
// EXIT 5 IS "NO TESTS RAN", AND IT IS A FINDING. This atom used to answer
// ABSENT — exit 0 — for it, on the reasoning that a go star with one .py file
// (gate-helios-057545b, 2026-09-10) was in its permanent state rather than at
// fault. Rob, 2026-09-11: nothing is built without tests. A python lane with
// nothing to collect is red.
//
// EVERY OTHER NON-ZERO CODE IS ALSO A FINDING, deliberately and against this
// module's usual rule: the shell body folded pytest's 1 (failures), 2
// (interrupted), 3 (internal error) and 4 (usage error) to one exit 1, and
// that fold is carried rather than re-litigated here — the port changes the
// shape, not the verdicts. An empty reason means the tool's own output is the
// reason.
func PytestState(code int) (state int, reason string) {
	switch code {
	case 0:
		return 0, ""
	case 5:
		return 1, "pytest collected no tests (exit 5); nothing is built without tests"
	default:
		return 1, ""
	}
}

// criticalModulesLine is the `critical_modules:` declaration in a repo's
// .copier-answers.yml — the template question's own key, at the root of the
// document.
var criticalModulesLine = regexp.MustCompile(`^critical_modules:[[:space:]]*`)

// CriticalModules reads the repo's declared critical modules out of
// .copier-answers.yml.
//
// THE ONE REPO FACT THE MUTATION LANE READS. Rob, 2026-09-11: a repo has no
// say in anything that runs, so the first cut's ci/mutation.env — MUT_* knobs
// standing in for the retired workflow's inputs, including a one-line switch
// that turned the gate off — is gone. critical_modules stays because it
// declares WHAT matters, not how hard to look, and it is the same string the
// retired mutation.yml rendered into its `modules` input.
//
// The semantics are the shell's, exactly: the FIRST line whose start is the
// key, the value with its leading whitespace stripped, and then ONE leading
// and ONE trailing quote character removed (sed's `s/^['"]//` and
// `s/['"]$//`). A value that is quoted on one side only loses that one quote,
// because that is what sed did.
func CriticalModules(yaml string) string {
	for _, line := range strings.Split(yaml, "\n") {
		line = strings.TrimRight(line, "\r")
		loc := criticalModulesLine.FindStringIndex(line)
		if loc == nil {
			continue
		}
		v := line[loc[1]:]
		if len(v) > 0 && (v[0] == '\'' || v[0] == '"') {
			v = v[1:]
		}
		if len(v) > 0 && (v[len(v)-1] == '\'' || v[len(v)-1] == '"') {
			v = v[:len(v)-1]
		}
		return v
	}
	return ""
}

// MutationScope is the line a mutation atom prints about what the run was
// scoped to, and it is printed EITHER WAY.
//
// AN EMPTY LIST IS NOT AN OPT-OUT. A repo that declared nothing gets the whole
// diff as its scope, and the line says so — silence would read as the atom
// having quietly narrowed itself to nothing, which is the shape this module
// exists to refuse. The blank test is the shell's `tr -d ' '`: a value that is
// only spaces is no declaration, and it is the whole of the ModulesDeclared
// predicate the rust port carried separately.
//
// It takes the atom id because three lanes print this sentence — python, rust
// and ts — and three copies of it is three places for the wording to drift.
func MutationScope(id, mods string) string {
	if strings.ReplaceAll(mods, " ", "") != "" {
		return id + ": scoped to the declared critical modules: " + mods
	}
	return id + ": no critical modules declared - the whole diff is the scope; an empty list is not an opt-out"
}
