package checks

import (
	"reflect"
	"strings"
	"testing"
)

func TestPythonSourceDirsKeepsTheShellsOrder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []string
		want    []string
	}{
		{"both, listed backwards", []string{"tests", "pyproject.toml", "src"}, []string{"src", "tests"}},
		{"trailing slashes", []string{"src/", "tests/"}, []string{"src", "tests"}},
		{"dot-slash prefixed", []string{"./src", "./tests"}, []string{"src", "tests"}},
		{"only src", []string{"src", "README.md"}, []string{"src"}},
		{"only tests", []string{"tests"}, []string{"tests"}},
		{"neither", []string{"pyproject.toml", "scripts"}, nil},
		{"a file named src is still the entry we see", []string{"src"}, []string{"src"}},
		{"both spellings at once", []string{"./src/", "./tests/"}, []string{"src", "tests"}},
		{"the same dir twice is one target", []string{"src", "src/"}, []string{"src"}},
		{"a path below the root is not a root entry", []string{"src/x.py", "tests/test_x.py"}, nil},
		{"no entries at all", nil, nil},
	} {
		if got := PythonSourceDirs(tc.entries); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRuffFormatTargetsScopesAGoStarToItsProductPython(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []string
		want    []string
		wantOK  bool
	}{
		{"a python star formats itself whole", []string{"pyproject.toml", "src", "tests"}, []string{"."}, true},
		{"a python star with no src or tests still formats whole", []string{"pyproject.toml"}, []string{"."}, true},
		{"a go star with both", []string{"go.mod", "pyproject.toml", "src", "tests"}, []string{"src", "tests"}, true},
		{"a go star with only tests", []string{"go.mod", "tests"}, []string{"tests"}, true},
		{"a go star with neither is absent", []string{"go.mod", "pyproject.toml", "scripts"}, nil, false},
		{"a go star with only src", []string{"go.mod", "src"}, []string{"src"}, true},
		{"go.mod under a directory does not make a go star", []string{"pyproject.toml", "vendor/go.mod"}, []string{"."}, true},
		{"./go.mod is still the root manifest", []string{"./go.mod", "src"}, []string{"src"}, true},
		// The manifest is a FILE. Dagger names a directory with a trailing
		// slash, so a directory called go.mod is not the thing that declares
		// the Go lane and the tree formats itself whole.
		{"a directory named go.mod is not the manifest", []string{"go.mod/", "pyproject.toml"}, []string{"."}, true},
		{"an empty tree formats itself whole", nil, []string{"."}, true},
	} {
		got, ok := RuffFormatTargets(tc.entries)
		if ok != tc.wantOK || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v/%v, want %v/%v", tc.name, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestDeclaresForgeTestkitReadsTheDependencyEntryNotTheWord(t *testing.T) {
	declares := []string{
		`    "forge-testkit>=0.5.0",`,
		`  "forge-testkit",`,
		`"forge-testkit"`,
		"\t\"forge-testkit[dev]>=1\",",
		`    "forge-testkit ~= 0.4",`,
		`    "forge-testkit`,
		`    "forge-testkit!=0.3",`,
		`    "forge-testkit~=0.4",`,
		"    \"forge-testkit>=1\",\r",
		`    "forge-testkit>1",`,
		`    "forge-testkit<2",`,
	}
	for _, line := range declares {
		if !DeclaresForgeTestkit("[project]\ndependencies = [\n" + line + "\n]\n") {
			t.Errorf("%q: should be a declared dependency", line)
		}
	}
	mentions := []string{
		`# forge-testkit is how the fleet lints tests`,
		`  # "forge-testkit>=0.5",`,
		`forge-testkit`,
		`require forgejo.notusmi.com/foundry/forge-testkit-go v0.2.0`,
		`    "forge-testkit-extras>=1",`,
		`    'forge-testkit>=0.5',`,
		``,
		// The one entry on the one line the array opens on is not an entry
		// this reads: the quote has to start the line. everyLaneTree spelled
		// it this way and the three forge-testkit atoms stood down ABSENT.
		`dependencies = ["forge-testkit>=1"]`,
		`    "forge_testkit>=1",`,
	}
	for _, line := range mentions {
		if DeclaresForgeTestkit(line) {
			t.Errorf("%q: is a mention, not a dependency entry", line)
		}
	}
}

func TestPytestStateFoldsEveryNonZeroToAFinding(t *testing.T) {
	if state, reason := PytestState(0); state != 0 || reason != "" {
		t.Errorf("0: got %d/%q", state, reason)
	}
	state, reason := PytestState(5)
	if state != 1 || reason == "" {
		t.Errorf("5: got %d/%q, want a finding with its own reason", state, reason)
	}
	// 4 and 6 bracket the one code that carries its own sentence, so a mutant
	// that moves the boundary off 5 dies here.
	for _, code := range []int{1, 2, 3, 4, 6, 127, 137, -1} {
		state, reason := PytestState(code)
		if state != 1 || reason != "" {
			t.Errorf("%d: got %d/%q, want 1 with the tool's own output as the reason", code, state, reason)
		}
	}
}

// ONE READER FOR THE THREE LANES THAT DECLARE. python, rust and ts each landed
// their own parse of .copier-answers.yml on their port branch — CriticalModules
// here, criticalModules/RustCriticalModules in rustlane.go — because three
// branches could not declare one exported name without colliding at the merge.
// This is the survivor and this is the one table; the cases below the blank
// line came off the rust copy, and both parses agreed on every one of them.
func TestCriticalModulesReadsTheFirstDeclarationAndUnquotesIt(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want string
	}{
		{"double quoted", "_src_path: x\ncritical_modules: \"src/hades src/eros\"\n", "src/hades src/eros"},
		{"single quoted", "critical_modules: 'src/a'\n", "src/a"},
		{"bare", "critical_modules: src/a src/b\n", "src/a src/b"},
		{"no whitespace after the colon", "critical_modules:src/a\n", "src/a"},
		{"tabs after the colon", "critical_modules:\t\tsrc/a\n", "src/a"},
		{"empty declaration", "critical_modules:\n", ""},
		{"empty quoted declaration", "critical_modules: \"\"\n", ""},
		{"first wins", "critical_modules: one\ncritical_modules: two\n", "one"},
		{"absent", "_src_path: x\nproject_name: y\n", ""},
		{"indented is not the root key", "  critical_modules: src/a\n", ""},
		{"a key that merely starts the same", "critical_modules_extra: src/a\n", ""},
		{"nothing at all", "", ""},
		{"carriage returns survive the split", "critical_modules: \"src/a\"\r\n", "src/a"},

		{"no answers file at all", "", ""},
		{"declared blank", "critical_modules:   \n", ""},
		{"a longer key does not shadow this one", "critical_modules_extra: a\ncritical_modules: b\n", "b"},
		{"windows line endings, bare value", "critical_modules: src/x\r\n", "src/x"},

		// ONE QUOTE OFF EACH END, AND ONLY ONE — sed's `s/^['"]//` then
		// `s/['"]$//`, which is what the shell did and what the two index
		// arithmetics here have to keep doing.
		{"opening quote only", `critical_modules: "src/a`, "src/a"},
		{"closing quote only", `critical_modules: src/a"`, "src/a"},
		{"mismatched quotes come off anyway", `critical_modules: "src/a'`, "src/a"},
		{"only the outermost quote comes off", `critical_modules: ""src/a""`, `"src/a"`},
		{"a value that is one quote character", `critical_modules: "`, ""},
		{"a value that is two quote characters", `critical_modules: ''`, ""},
		// The leading whitespace is the regex's; the trailing whitespace is
		// nobody's, and the shell kept it too.
		{"trailing whitespace survives", "critical_modules: src/a  \n", "src/a  "},
		{"a value with a colon in it", "critical_modules: src/a:b\n", "src/a:b"},
		{"a carriage return is the whole value", "critical_modules:\r\n", ""},
		{"the last line needs no newline", "critical_modules: src/a", "src/a"},
	} {
		if got := CriticalModules(tc.yaml); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// THE SCOPE LINE IS PRINTED EITHER WAY, IN EVERY LANE THAT DECLARES. It carries
// the atom's own id because three lanes print it and three copies of one
// sentence is three places for the wording to drift — the rust port had built
// it inline from a ModulesDeclared predicate, which is folded in here.
//
// AND IT MUST NOT READ AS AN ABSENCE: the line shares the atom's id prefix with
// every announcement, and an "an empty list is not an opt-out" that VerdictOf
// read as ABSENT would be a repo opting out of the mutation gate by declaring
// nothing.
func TestMutationScopeSaysSoEitherWayInEveryLane(t *testing.T) {
	for _, id := range []string{"python:mutation", "rust:mutation", "ts:mutation"} {
		scoped := MutationScope(id, "src/a src/b")
		if want := id + ": scoped to the declared critical modules: src/a src/b"; scoped != want {
			t.Errorf("got %q, want %q", scoped, want)
		}
		// The blank test is the shell's `tr -d ' '`: only spaces is no
		// declaration. A tab-only value stays a declaration, which is what the
		// shell did — the rust port's TrimSpace predicate called it blank, and
		// that is the one input the two disagreed on.
		for _, blank := range []string{"", " ", "    "} {
			line := MutationScope(id, blank)
			if line == "" || line == scoped {
				t.Errorf("%s/%q: got %q, want the not-scoped line", id, blank, line)
			}
			if !strings.Contains(line, "an empty list is not an opt-out") {
				t.Errorf("%s/%q: the not-scoped line does not say the list is not an opt-out: %q", id, blank, line)
			}
			if _, ok := AnnouncedAbsence(id, line); ok {
				t.Errorf("%s/%q: the scope line reads as an absence", id, blank)
			}
		}
		// THE ONE INPUT THE TWO PARSES DISAGREED ON, asserted rather than left
		// in the comment: `tr -d ' '` deletes spaces and nothing else, so a
		// tab-only value is still a declaration. The rust port's TrimSpace
		// predicate called it blank; this is the shell's answer.
		for _, tabbed := range []string{"\t", " \t ", "\n"} {
			if line := MutationScope(id, tabbed); line != id+": scoped to the declared critical modules: "+tabbed {
				t.Errorf("%s/%q: a value the shell's `tr -d ' '` does not empty is still a declaration: %q", id, tabbed, line)
			}
		}
		// The declared value is printed verbatim, padding and all.
		if line := MutationScope(id, " src/a "); !strings.HasSuffix(line, ": scoped to the declared critical modules:  src/a ") {
			t.Errorf("%s: the declaration is printed verbatim: %q", id, line)
		}
		// Every line this module answers with leads with the atom's id, so a
		// reader (and AnnouncedAbsence) can tell whose sentence it is.
		if !strings.HasPrefix(scoped, id+": ") {
			t.Errorf("%s: the scope line must lead with the atom id: %q", id, scoped)
		}
	}
}

// ---- a .py anywhere declares the lane (2026-09-25) ----

func TestPythonFilesTakesThePythonARepositoryOwns(t *testing.T) {
	got := PythonFiles([]string{
		"setup.py",
		"hooks/plane_session_end/plane_session_end.py",
		"README.md",
		"tools/forge/main.go",
	})
	want := []string{"hooks/plane_session_end/plane_session_end.py", "setup.py"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v — every .py the repo owns, sorted", got, want)
	}
}

// THE EXCLUSIONS ARE THE GO LANE'S, SHARED RATHER THAN COPIED. A .py under a
// vendored or hidden directory is not the repository's python, for the same
// reason a go.mod under one is not its module — and the alternative was a
// second copy of the list, free to drift from the first.
func TestPythonFilesSkipsWhatNoLaneCountsAsSource(t *testing.T) {
	for _, f := range []string{
		"vendor/pkg/thing.py",
		"testdata/fixture.py",
		"_build/gen.py",
		".tox/site-packages/x.py",
		"src/vendor/deep/thing.py",
	} {
		if got := PythonFiles([]string{f}); len(got) != 0 {
			t.Errorf("%q is not the repository's own python, got %v", f, got)
		}
	}
	// The root is never excluded by the dir rule — it has no directory.
	if got := PythonFiles([]string{"conftest.py"}); len(got) != 1 {
		t.Errorf("a .py at the root is the repository's own python, got %v", got)
	}
	// A name that merely CONTAINS .py is not a .py.
	if got := PythonFiles([]string{"docs/notes.python", "x.pyi"}); len(got) != 0 {
		t.Errorf("only .py counts here, got %v", got)
	}
}

// A GO STAR'S src/ IS NOT A MYPY TARGET. urania, helios and nyx carry src/ and
// tests/ full of .go and one or two incidental .py elsewhere; mypy pointed at
// those answers exit 2, which this module files as CANNOT RUN — a check that
// did not happen, against a repository with nothing wrong with it.
func TestDirsCarryingPythonIntersectsWithTheTree(t *testing.T) {
	dirs := []string{"src", "tests"}
	pys := []string{"tests/test_thing.py", "scripts/oneoff.py"}
	got := DirsCarryingPython(dirs, pys)
	if strings.Join(got, ",") != "tests" {
		t.Errorf("got %v, want [tests] — src/ holds no python here", got)
	}
	if got := DirsCarryingPython(dirs, nil); len(got) != 0 {
		t.Errorf("a tree with no python targets nothing, got %v", got)
	}
	// The candidates' own order is kept: it is the order the shell loop walked
	// and the order the targets reach the tool.
	both := DirsCarryingPython(dirs, []string{"tests/t.py", "src/a.py"})
	if strings.Join(both, ",") != "src,tests" {
		t.Errorf("got %v, want the candidates' order", both)
	}
	// A prefix match is on the SEGMENT, not the string: srcx/ is not src/.
	if got := DirsCarryingPython([]string{"src"}, []string{"srcx/a.py"}); len(got) != 0 {
		t.Errorf("srcx/ is not src/, got %v", got)
	}
}

func TestPythonTestFilesIsPytestsTwoConventions(t *testing.T) {
	got := PythonTestFiles([]string{
		"hooks/x/test_hook.py",
		"pkg/a_test.py",
		"src/app.py",
		"tests/conftest.py",
	})
	want := "hooks/x/test_hook.py,pkg/a_test.py"
	if strings.Join(got, ",") != want {
		t.Errorf("got %v, want %s", got, want)
	}
	if got := PythonTestFiles([]string{"src/app.py"}); len(got) != 0 {
		t.Errorf("a tree with no test file collects nothing, got %v", got)
	}
}

// WITHOUT A PROJECT, `uv run` HAS NOTHING TO RUN. It resolves a pyproject.toml
// or it fails, and --all-extras is a project flag — so a lane declared by its
// files alone would be a lane that is declared and then cannot run. This is
// the line that makes the widening real rather than nominal.
func TestUvRunAsksForAProjectOnlyWhenThereIsOne(t *testing.T) {
	withProject := UvRun([]string{"pyproject.toml"}, "pytest", "-q")
	if strings.Join(withProject, " ") != "uv run --all-extras pytest -q" {
		t.Errorf("got %v — a project's own extras are what its checks run against", withProject)
	}
	without := UvRun([]string{"README.md"}, "pytest", "-q")
	if strings.Join(without, " ") != "uv run --no-project --with pytest pytest -q" {
		t.Errorf("got %v — no project to resolve, and the tool has to be fetched", without)
	}
}

func TestRuffLineLengthFollowsTheStarNotThePyprojectToml(t *testing.T) {
	// THE FLEET HAS ONE RULESET AND TWO WIDTHS, and the atom had been passing
	// neither — rulesets/ruff.toml carries 80, so every go, rust and ts star
	// was graded at 80 against a repo whose own ruff.toml declares 100.
	// Measured on cerberus 2026-09-25: 38 files red at 80, 33 at 100.
	//
	// The second row is the one that matters. A go star carrying a root
	// pyproject.toml for its product python is still a GO star, poured
	// go-repo-template's ruff.toml at 100 — and ruff reads ruff.toml over
	// pyproject.toml, so 100 is also what its editor shows. A first cut that
	// tested for pyproject.toml alone answered 80 there and would have
	// recreated this defect one lane over.
	for _, tc := range []struct {
		name    string
		entries []string
		want    string
	}{
		{"a python star", []string{"pyproject.toml", "src", "tests"}, "80"},
		{"a go star that also declares python", []string{"go.mod", "pyproject.toml", "src"}, "100"},
		{"a rust star that also declares python", []string{"Cargo.toml", "pyproject.toml"}, "100"},
		{"a ts star that also declares python", []string{"package.json", "pyproject.toml"}, "100"},
		{"a rust star with only incidental python", []string{"Cargo.toml", "probes"}, "100"},
		{"a go star with only incidental python", []string{"go.mod", "scripts"}, "100"},
		{"a tree declaring nothing", []string{"README.md"}, "100"},
		{"dagger's leading ./ is not a different repository", []string{"./pyproject.toml"}, "80"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RuffLineLength(tc.entries); got != tc.want {
				t.Errorf("RuffLineLength(%v) = %q, want %q", tc.entries, got, tc.want)
			}
		})
	}
}
