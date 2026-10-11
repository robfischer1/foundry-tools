package checks

import (
	"path"
	"regexp"
	"sort"
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

// RuffLineLength is the formatter width this tree is graded at, as a string
// ready for the command line. It reads the ROOT pyproject.toml's contents,
// not just its presence.
//
// THE FLEET HAS ONE RULESET AND TWO WIDTHS. rulesets/ruff.toml says so in as
// many words — "80 for a python star and 100 for python embedded in another
// lane ... the atom passes it as --line-length by lane" — and until #173 the
// atom did not pass it at all.
//
// A PYTHON STAR IS ONE THAT BUILDS A DISTRIBUTION. [build-system] is the test,
// and it is the only one that survives contact with the fleet. Presence of a
// pyproject.toml is not enough and neither is the absence of a sibling
// manifest; both were tried and both were wrong:
//
//	chaos     pyproject + go.mod, hatchling, publishes a wheel.
//	          Its python IS the product (src/chaos) and its own [tool.ruff]
//	          declares 80. "python + any other manifest -> 100" graded it at
//	          100 and reddened 22 files that were already correct.
//
//	cerberus  pyproject + Cargo.toml, NO build-system. Its own comment says
//	          it: "IT IS NOT A PACKAGE AND DOES NOT PUBLISH ... This declares
//	          an ENVIRONMENT". The artifact is the Rust binary; the python is
//	          hooks and probes, and its ruff.toml declares 100. "any
//	          pyproject -> 80" would grade it at 80 and redden 38 files.
//
// One rule answers both, because [build-system] is the thing that says the
// python is the SUBJECT of this repository rather than its tooling. Measured
// 2026-09-25 across all 24 repos the gate grades, against the atom's own
// scope: every repo is clean at the width this returns, and every repo's own
// declared line-length agrees with it.
//
// ONLY THE FORMATTER NEEDS IT. Measured at both widths: `ruff check` reports
// identically, because E501 is in the ruleset's ignore list and nothing else
// selected reads line-length. If E501 is ever un-ignored, the lint atom needs
// this too and does not have it.
func RuffLineLength(entries []string, pyproject string) string {
	if !DeclaresManifest(entries, ManifestFor(LanePython)) {
		return "100"
	}
	if !declaresBuildSystem(pyproject) {
		return "100"
	}
	return "80"
}

// declaresBuildSystem reports whether this pyproject.toml builds a
// distribution — a [build-system] table at the start of a line, which is the
// only place TOML puts one.
func declaresBuildSystem(pyproject string) bool {
	for _, line := range strings.Split(pyproject, "\n") {
		if strings.TrimSpace(line) == "[build-system]" {
			return true
		}
	}
	return false
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
	return answersValue(yaml, criticalModulesLine)
}

// answersValue is the shell's read of one .copier-answers.yml key, shared by
// every answer the lane reads (CriticalModules, ServiceName): the FIRST line
// whose start matches key, the value with its leading whitespace stripped,
// and one leading and one trailing quote character removed.
func answersValue(yaml string, key *regexp.Regexp) string {
	for _, line := range strings.Split(yaml, "\n") {
		line = strings.TrimRight(line, "\r")
		loc := key.FindStringIndex(line)
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

// PythonFiles answers every .py file in a population that declares the python
// lane, sorted.
//
// A .py ANYWHERE, NOT A ROOT MANIFEST. The lane used to be declared by
// pyproject.toml at the repository root alone, and that left every repository
// whose python is not a packaged project ungated: foundry-stocks carries 234
// hook tests that furnace renders into every session's live .claude/hooks/ —
// in-path for the whole fleet by the next stoke — and the gate ran none of
// them, because the repo has no pyproject.toml to declare a lane with.
//
// Rob, 2026-09-25: "The presence of a .py file anywhere in the repo should
// trigger the python CI atoms. Not the pyproject.toml." This is the same move
// GoModuleDirs made for go.mod on 2026-09-16 and for the same reason, so it
// keeps the same exclusion discipline (vendoredOrHidden, shared with the go
// lane rather than copied): a .py under vendor/ or testdata/, or
// under a directory whose name starts with "_" or ".", is not the repository's
// python. The population this reads is already the engine's gitignore filter
// plus GateExclude, so .venv/, __pycache__/ and node_modules/ never arrive.
//
// THE BUILD LANE IS NOT THIS. pyproject.toml still declares what BUILDS —
// AtomDef.NeedsManifest is how an atom says it needs the manifest rather than
// the files, and python:release, python:pip-audit and python:mutation each say
// so. Lint and test read the tree; the build reads the manifest.
func PythonFiles(files []string) []string {
	var out []string
	for _, f := range files {
		dir, name := path.Split(f)
		if !strings.HasSuffix(name, ".py") {
			continue
		}
		if dir != "" && vendoredOrHidden(strings.TrimSuffix(dir, "/")) {
			continue
		}
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// DirsCarryingPython narrows candidate directories to the ones that actually
// hold a .py, in the candidates' own order.
//
// WHY A DIRECTORY'S NAME IS NOT ENOUGH. PythonSourceDirs answers "does this
// repository have src/ and tests/", which was a good enough proxy while only a
// pyproject.toml put a repo in this lane. A .py-declared lane breaks the
// proxy: urania, helios and nyx are GO stars with src/ and tests/ full of .go
// and one or two incidental .py somewhere else entirely, and pointing mypy at
// those directories asks it to type-check a Go tree. mypy's answer to that is
// "there are no .py[i] files in directory", exit 2 — a CANNOT RUN, which is
// this module's word for "the check did not happen", filed against four repos
// that had nothing wrong with them.
//
// So the targets are intersected with the population: a directory reaches mypy
// because python was found under it, not because it is called src.
func DirsCarryingPython(dirs []string, pyFiles []string) []string {
	var out []string
	for _, d := range dirs {
		prefix := strings.TrimSuffix(d, "/") + "/"
		for _, f := range pyFiles {
			if strings.HasPrefix(strings.TrimPrefix(f, "./"), prefix) {
				out = append(out, d)
				break
			}
		}
	}
	return out
}

// PythonTestFiles answers the .py files in a population that pytest would
// collect by either of its two default naming conventions.
//
// IN GO, OFF THE TREE, BEFORE PYTEST IS ASKED — the discipline pythonPytest
// already had, now reading the files instead of asking whether a directory
// named tests/ exists. Same reason as DirsCarryingPython: a Go star's tests/
// is not python, and "this repo has a tests/ directory" stopped being evidence
// about python the moment a .py anywhere could declare the lane.
//
// The rule it feeds is unchanged and is Rob's, 2026-09-11 and again
// 2026-09-25: nothing is built without tests. A python lane with nothing to
// collect is RED. This only makes the emptiness a fact about python files
// rather than about a directory name, so the finding says something true.
func PythonTestFiles(pyFiles []string) []string {
	var out []string
	for _, f := range pyFiles {
		_, name := path.Split(f)
		if strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test.py") {
			out = append(out, f)
		}
	}
	return out
}

// UvRun builds a `uv run` command line for a tree that may or may not be a
// python PROJECT.
//
// `uv run` without a pyproject.toml fails: there is no project to resolve, and
// --all-extras is a project flag. That is exactly the tree this lane now
// reaches — foundry-stocks' 234 hook tests live in hooks/, declared by no
// manifest — so the no-project form is the one that makes a .py-declared lane
// more than a lane that is declared and then cannot run.
//
// WITH A PROJECT: `uv run --all-extras <tool> <args>`, because a check that
// cannot import the optional dependencies the code declares is a check of a
// different program. WITHOUT: `uv run --no-project --with <tool> <tool>
// <args>`, because nothing is installed and the tool has to be fetched — the
// same `--with` argument pip-audit already makes for the fleet's own tooling.
func UvRun(entries []string, tool string, args ...string) []string {
	if DeclaresManifest(entries, ManifestFor(LanePython)) {
		return append([]string{"uv", "run", "--all-extras", tool}, args...)
	}
	return append([]string{"uv", "run", "--no-project", "--with", PythonWith(tool), tool}, args...)
}
