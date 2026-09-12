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
		{"go.mod under a directory does not make a go star", []string{"pyproject.toml", "vendor/go.mod"}, []string{"."}, true},
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
	for _, code := range []int{1, 2, 3, 4, 127, 137} {
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
	}
}
