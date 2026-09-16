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

// RustCriticalModules/criticalModules and ModulesDeclared were this lane's
// copies of helpers every mutation lane needed. They are one each now:
// checks.CriticalModules and checks.MutationScope (pythonlane.go,
// pythonlane_test.go). This lane's distinct cases moved into those tables.

// THE CUT IS 200 RUNES AND THE BOUNDARY IS EXACT. Without both sides of it a
// cut at 199 or at 201 reads exactly like this one, and the length assertion
// above passes either way — MEASURED 2026-09-12 on PR #31, where the boundary
// mutant at rustlane.go:53:32 LIVED against that assertion alone.
func TestFirstCargoErrorCutsAtExactlyTwoHundredRunes(t *testing.T) {
	// é is two bytes, so a byte cut here would answer 200 BYTES — half the
	// line, ending mid-character — rather than 200 runes.
	at199 := "error: " + strings.Repeat("é", 192)
	at200 := "error: " + strings.Repeat("é", 193)
	at201 := "error: " + strings.Repeat("é", 194)
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"one under the limit is whole", at199, at199},
		{"exactly at the limit is whole", at200, at200},
		{"one over the limit loses exactly one rune", at201, string([]rune(at201)[:200])},
	} {
		got := FirstCargoError(tc.in)
		if got != tc.want {
			t.Errorf("%s: FirstCargoError answered %d runes, want %d",
				tc.name, len([]rune(got)), len([]rune(tc.want)))
		}
	}
	if n := len([]rune(FirstCargoError(at201))); n != 200 {
		t.Errorf("a diagnostic over the limit was cut to %d runes, want 200", n)
	}
}

// AT THE LIMIT THE LINE IS RETURNED, NOT REBUILT — and that is the only input
// on which the `>` this function is written with differs from a `>=`.
// string([]rune(line)[:200]) equals line for 200 runes of WELL-FORMED UTF-8,
// which is what makes the boundary mutant survive every ordinary table.
//
// rustc prints the source expression it choked on, and source is bytes: a
// diagnostic carrying one byte that is not valid UTF-8 is still 200 runes
// long, and rebuilding it through []rune silently replaces that byte with
// U+FFFD. The reader is owed what cargo printed.
func TestFirstCargoErrorAtTheLimitIsNotRebuiltThroughRunes(t *testing.T) {
	at200 := "error: " + strings.Repeat("x", 192) + "\xff"
	if n := len([]rune(at200)); n != 200 {
		t.Fatalf("the fixture is %d runes, not the 200 this case is about", n)
	}
	if got := FirstCargoError(at200); got != at200 {
		t.Errorf("the line at the limit came back rebuilt:\n got %q\nwant %q", got, at200)
	}
}
