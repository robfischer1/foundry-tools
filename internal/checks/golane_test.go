package checks

import (
	"strings"
	"testing"
)

// The budget decides which of two spellings of ONE gofmt run the atom uses, so
// what matters is that a realistic population takes the argument path and an
// unrealistic one still has somewhere to go.
func TestNeedsArgFileOnlyForAListNoArgvWouldCarry(t *testing.T) {
	if NeedsArgFile(nil) {
		t.Error("an empty population needs no arg file")
	}

	modest := make([]string, 0, 2000)
	for i := 0; i < 2000; i++ {
		modest = append(modest, "internal/checks/some/deep/package/file.go")
	}
	if NeedsArgFile(modest) {
		t.Errorf("%d files of %d bytes fit in an argv and must go as arguments",
			len(modest), len(modest[0]))
	}

	huge := make([]string, 0, 40000)
	for i := 0; i < 40000; i++ {
		huge = append(huge, "internal/checks/some/deep/package/file.go")
	}
	if !NeedsArgFile(huge) {
		t.Errorf("%d files is past the argv budget and must go through a file", len(huge))
	}

	// One path longer than the whole budget is enough on its own.
	if !NeedsArgFile([]string{strings.Repeat("a", argvBudget+1)}) {
		t.Error("a single path past the budget must go through a file")
	}
}

func TestGovulncheckExitReadsThreeAsAFinding(t *testing.T) {
	for code, want := range map[int]int{0: 0, 1: 1, 2: 2, 3: 1, 137: 137} {
		if got := GovulncheckExit(code); got != want {
			t.Errorf("GovulncheckExit(%d) = %d, want %d", code, got, want)
		}
	}
}
