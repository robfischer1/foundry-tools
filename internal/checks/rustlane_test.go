package checks

import (
	"strings"
	"testing"
)

// 101 IS THE CODE THE LANE TURNS ON. Everything else keeps the meaning
// StateFor already gives it.
func TestCargoExitMapsOnlyCargosOwnFailureCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   int
		want int
	}{
		{"clean stays a pass", 0, 0},
		{"rustfmt's own findings code is already right", 1, 1},
		{"clippy denied a lint, cargo said 101", 101, 1},
		{"cargo test failed a test, cargo said 101", 101, 1},
		{"no such binary stays a could-not-run", 127, 127},
		{"OOM kill stays a could-not-run", 137, 137},
		{"cancelled stays a could-not-run", 143, 143},
		{"2 is not cargo's and is left alone", 2, 2},
	} {
		if got := CargoExit(tc.in); got != tc.want {
			t.Errorf("%s: CargoExit(%d) = %d, want %d", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestCargoExitLeavesEveryNonPassANonPass(t *testing.T) {
	for code := 1; code < 256; code++ {
		if StateFor(CargoExit(code)) == StatePass {
			t.Fatalf("CargoExit(%d) turned a failure into a pass", code)
		}
	}
	if StateFor(CargoExit(0)) != StatePass {
		t.Fatal("CargoExit(0) is not a pass")
	}
}

func TestFirstCargoErrorReadsTheFirstDiagnostic(t *testing.T) {
	long := "error[E0599]: " + strings.Repeat("x", 400)
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"empty output has no error line", "", ""},
		{"nothing matching answers empty", "   Compiling foo v0.1.0\nwarning: unused", ""},
		{
			"the first of several wins",
			"   Compiling foo\nerror[E0425]: cannot find value `x`\nerror: aborting due to 1 previous error",
			"error[E0425]: cannot find value `x`",
		},
		{
			"only a line STARTING with error counts",
			"note: an error occurred earlier\nerror: could not compile `foo`",
			"error: could not compile `foo`",
		},
		{"carriage returns are not part of the line", "error: nope\r\nmore", "error: nope"},
	} {
		if got := FirstCargoError(tc.in); got != tc.want {
			t.Errorf("%s: FirstCargoError(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
	if got := []rune(FirstCargoError(long)); len(got) != 200 {
		t.Errorf("a long diagnostic was cut to %d runes, want 200", len(got))
	}
}

func TestCargoListsTestsReadsLibtestsOwnListing(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want bool
	}{
		{"an empty listing lists nothing", "", false},
		{"the header alone is not a test", "\n0 tests, 0 benchmarks", false},
		{"one test is enough", "checks::lane::works: test\n\n1 test, 0 benchmarks", true},
		{"a benchmark is not a test", "bench::throughput: benchmark\n\n0 tests, 1 benchmark", false},
		{"the suffix is anchored", "this line mentions: test cases", false},
		{"carriage returns do not hide the suffix", "a::b: test\r\n", true},
	} {
		if got := CargoListsTests(tc.in); got != tc.want {
			t.Errorf("%s: CargoListsTests(%q) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestMutationScoreRefusesAnythingThatIsNotAVerdict(t *testing.T) {
	for _, tc := range []struct {
		name   string
		in     string
		want   int
		wantOk bool
	}{
		{"clean", "0\n", 0, true},
		{"survivors", "1", 1, true},
		{"could not measure", " 2 \n", 2, true},
		{"an empty file is no verdict", "", 0, false},
		{"whitespace is no verdict", "  \n\t", 0, false},
		{"prose is no verdict", "clean", 0, false},
		{"two numbers are no verdict", "0 1", 0, false},
	} {
		got, ok := MutationScore(tc.in)
		if got != tc.want || ok != tc.wantOk {
			t.Errorf("%s: MutationScore(%q) = (%d, %v), want (%d, %v)", tc.name, tc.in, got, ok, tc.want, tc.wantOk)
		}
	}
}

func TestCriticalModulesReadsTheTemplatesAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"no answers file at all", "", ""},
		{"the key is absent", "_src_path: x\nproject_name: foo\n", ""},
		{"bare value", "critical_modules: src/engine\n", "src/engine"},
		{"single quoted", "critical_modules: 'src/engine src/core'\n", "src/engine src/core"},
		{"double quoted", "critical_modules: \"src/engine\"\n", "src/engine"},
		{"no space after the colon", "critical_modules:src/engine\n", "src/engine"},
		{"declared empty", "critical_modules: ''\n", ""},
		{"declared blank", "critical_modules:   \n", ""},
		{"the first line wins", "critical_modules: a\ncritical_modules: b\n", "a"},
		{"the key must start the line", "  critical_modules: a\n", ""},
		{"a longer key is not this key", "critical_modules_extra: a\ncritical_modules: b\n", "b"},
		{"windows line endings", "critical_modules: src/x\r\n", "src/x"},
	} {
		if got := criticalModules(tc.in); got != tc.want {
			t.Errorf("%s: criticalModules(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
		if got := RustCriticalModules(tc.in); got != tc.want {
			t.Errorf("%s: RustCriticalModules(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestModulesDeclaredTreatsBlankAsUndeclared(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"", false},
		{"   ", false},
		{"\t", false},
		{"src/engine", true},
		{" src/engine ", true},
	} {
		if got := ModulesDeclared(tc.in); got != tc.want {
			t.Errorf("ModulesDeclared(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
