package checks

import (
	"reflect"
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
	} {
		if got := CriticalModules(tc.yaml); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestPythonMutationScopeSaysSoEitherWay(t *testing.T) {
	scoped := PythonMutationScope("src/a src/b")
	if got, want := scoped, "python:mutation: scoped to the declared critical modules: src/a src/b"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	for _, blank := range []string{"", " ", "    "} {
		line := PythonMutationScope(blank)
		if line == "" || line == scoped {
			t.Errorf("%q: got %q, want the not-scoped line", blank, line)
		}
		if readsAsAbsence(line) {
			t.Errorf("%q: the scope line must not read as an absence", blank)
		}
	}
}

// readsAsAbsence is the test's own reader: the scope line shares the atom's id
// prefix with every announcement, and must not be mistaken for the ABSENT one.
func readsAsAbsence(line string) bool {
	_, ok := AnnouncedAbsence("python:mutation", line)
	return ok
}

func TestMutationOutcomeRefusesAnEmptyOrUnreadableVerdict(t *testing.T) {
	state, reason, ok := MutationOutcome("1\n", "3 mutants survived on src/a\n")
	if !ok || state != 1 || reason != "3 mutants survived on src/a" {
		t.Errorf("got %d/%q/%v", state, reason, ok)
	}
	if state, _, ok := MutationOutcome(" 0 ", ""); !ok || state != 0 {
		t.Errorf("a clean verdict: got %d/%v", state, ok)
	}
	if state, _, ok := MutationOutcome("2", "cosmic-ray could not run"); !ok || state != 2 {
		t.Errorf("a could-not-measure verdict: got %d/%v", state, ok)
	}
	for _, bad := range []string{"", "   ", "\n", "clean", "1 survivor"} {
		if _, _, ok := MutationOutcome(bad, "whatever"); ok {
			t.Errorf("%q: should not be readable as a verdict", bad)
		}
	}
}
