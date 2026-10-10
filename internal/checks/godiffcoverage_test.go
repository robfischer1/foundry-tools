package checks

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// THE FIXTURE IS A REAL PROFILE. covSource was compiled and run with
// `go test -race -coverprofile` (go1.26.6, 2026-10-09) under a test that calls
// F(1) once, and covProfile is what the toolchain wrote — so the block columns
// below are the cover tool's, not a guess at them.
const covSource = `package cov

func F(x int) int {
	if x > 0 {
		return 1
	} else if x < -5 {
		return 2
	}
	switch {
	case x == -1:
		return 3
	default:
	}
	y := []int{
		1,
	}
	return y[0] // c
}
`

const covProfile = `mode: atomic
example.com/cov/notest/n.go:3.14,5.2 1 0
example.com/cov/a.go:3.19,4.11 1 1
example.com/cov/a.go:4.11,6.3 1 1
example.com/cov/a.go:6.8,6.19 1 0
example.com/cov/a.go:6.19,8.3 1 0
example.com/cov/a.go:9.2,9.9 1 0
example.com/cov/a.go:10.15,11.11 1 0
example.com/cov/a.go:12.10,12.10 0 0
example.com/cov/a.go:14.2,17.13 2 0
`

const covNoTest = "package notest\n\nfunc G() int {\n\treturn 1\n}\n"

const covPackages = "example.com/cov\t/src\nexample.com/cov/notest\t/src/notest\n"

// covExclude is the mutation lane's own pattern (atoms_go.go goMutationExclude),
// spelled here because this package cannot import main.
var covExclude = regexp.MustCompile(`^vendor/|(^|/)dagger\.gen\.go$|\.pb\.go$|(^|/)zz_generated`)

// hunk is a -U0 diff of one file adding lines from..from+n-1.
func hunk(file string, from, n int) string {
	return fmt.Sprintf("diff --git a/%[1]s b/%[1]s\n--- a/%[1]s\n+++ b/%[1]s\n@@ -0,0 +%d,%d @@\n", file, from, n)
}

func covInput(diff, profile string) GoDiffCoverageInput {
	return GoDiffCoverageInput{
		Dir: ".", Root: "/src", Diff: diff, Packages: covPackages, Profile: profile,
		Exclude: covExclude, Sources: map[string]string{"a.go": covSource, "notest/n.go": covNoTest},
	}
}

func subjects(found []Finding) string {
	var out []string
	for _, f := range found {
		out = append(out, f.Subject)
	}
	return strings.Join(out, ",")
}

// THE LINES gomutants WOULD CALL NOT COVERED, and no others. Each case names
// the cover tool's behaviour it pins.
func TestGoUncoveredLinesFlagsOnlyCodeInsideBlocksThatNeverRan(t *testing.T) {
	for _, tc := range []struct {
		name, diff, profile, want string
	}{
		{
			// The whole file is new. Lines 3-5 ran; 6 is the else-if condition
			// (its own zero block), 7, 9, 11, 14, 15 and 17 are statements that
			// never ran. 8, 13 and 16 are closing braces; 10 and 12 are case
			// labels whose clause blocks start after the colon; 18 closes F.
			"a new file", hunk("a.go", 1, 18), covProfile,
			"a.go:6,a.go:7,a.go:9,a.go:11,a.go:14,a.go:15,a.go:17",
		},
		{"only the changed lines", hunk("a.go", 5, 3), covProfile, "a.go:6,a.go:7"},
		{"a change to covered lines only", hunk("a.go", 3, 3), covProfile, ""},
		// A block's last line is judged when the range ends on it.
		{"a range ending on a block's last line", hunk("a.go", 17, 1), covProfile, "a.go:17"},
		// A package with no test is in the profile at count 0 (go1.22+).
		{"a package with no test", hunk("notest/n.go", 1, 5), covProfile, "notest/n.go:4"},
		{"two files, in file order", hunk("notest/n.go", 4, 1) + hunk("a.go", 7, 1), covProfile, "a.go:7,notest/n.go:4"},
		// A deletion adds no line, so it judges none.
		{"a deletion", "--- a/a.go\n+++ b/a.go\n@@ -7,2 +6,0 @@\n", covProfile, ""},
		{"a test file", hunk("a_test.go", 1, 5) + "", "example.com/cov/a_test.go:1.1,5.2 1 0\n", ""},
		{"generated Go", hunk("dagger.gen.go", 1, 18), strings.ReplaceAll(covProfile, "/a.go:", "/dagger.gen.go:"), ""},
		{"a non-Go file", hunk("a.txt", 1, 18), strings.ReplaceAll(covProfile, "/a.go:", "/a.txt:"), ""},
		// A RANGE THAT RAN ANYWHERE IS COVERED, whichever row comes first.
		{"a duplicate row that ran, after", hunk("a.go", 7, 1), covProfile + "example.com/cov/a.go:6.19,8.3 1 4\n", ""},
		{"a duplicate row that ran, before", hunk("a.go", 7, 1), "example.com/cov/a.go:6.19,8.3 1 4\n" + covProfile, ""},
		// A package outside the module (a nested module's) places nothing.
		{"a file of no listed package", hunk("a.go", 7, 1), strings.ReplaceAll(covProfile, "example.com/cov/a.go", "example.com/other/a.go"), ""},
		{"a row with no import path", hunk("a.go", 7, 1), "a.go:6.19,8.3 1 0\n", ""},
		{"a malformed row", hunk("a.go", 7, 1), "example.com/cov/a.go:6.19,8.3 1\nexample.com/cov/a.go:6.19-8.3 1 0\n", ""},
		// A zero-statement block keeps its count; empty, it spans no code.
		{"an empty default clause", hunk("a.go", 12, 1), covProfile, ""},
		{"a zero-statement block over code", hunk("a.go", 7, 1), "example.com/cov/a.go:7.1,7.20 0 0\n", "a.go:7"},
		// Two never-run blocks with code on one line are one finding.
		{"two blocks on one line", hunk("a.go", 7, 1), "example.com/cov/a.go:7.1,7.5 1 0\nexample.com/cov/a.go:7.5,7.20 1 0\n", "a.go:7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found := GoUncoveredLines(covInput(tc.diff, tc.profile))
			if got := subjects(found); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			for _, f := range found {
				if f.Verdict != VerdictViolated || f.Cause != "not-covered" || f.Probe != "go:diff-coverage" ||
					f.Detail != "this pull changed the line and no test executes it: the suite's coverage profile counts its block 0 times" {
					t.Errorf("finding %+v is not a NOT COVERED violation", f)
				}
			}
		})
	}
}

// THE SEGMENT A BLOCK SPANS IS WHAT IS JUDGED, never the whole line: the code
// before a block's start column and after its end column belongs to another
// block, and a line is only uncovered where the never-run block itself has code.
func TestGoUncoveredLinesReadsTheSpanOfEachBlock(t *testing.T) {
	for _, tc := range []struct {
		name, src, row, want string
	}{
		// An uncovered if-body closes on `} else {`: the else is a block that
		// ran, so the line is not this block's — only its `}` is.
		{"the closing brace before a covered else", "x\n\t} else {\n", "2.1,2.3 1 0", ""},
		{"code past the end column", "x\n\tdo() // c\n", "2.1,2.6 1 0", "m.go:2"},
		// The block starts at the column of its code, and nothing before it counts.
		{"code at the start column", "x\n{  ok\n", "2.4,2.6 1 0", "m.go:2"},
		{"layout before the start column", "x\ncall() {\n", "2.8,3.1 1 0", ""},
		{"an end column past the line", "x\n\tdo()\n", "2.1,2.99 1 0", "m.go:2"},
		{"a start column past the line", "x\n\tdo()\n", "2.99,2.99 1 0", ""},
		{"a comment at the line's start", "x\n// note\n", "2.1,2.9 1 0", ""},
		{"an indented comment", "x\n\t// note\n", "2.1,2.9 1 0", ""},
		{"brackets and separators", "x\n\t}, ([]);\n", "2.1,2.12 1 0", ""},
		// A source with no trailing newline: its last line is still a line.
		{"the source's last line", "x\nreturn 2", "2.1,2.9 1 0", "m.go:2"},
		{"a block past the source's end", "x\n", "3.1,3.9 1 0", ""},
		{"a file whose source was not read", "", "2.1,2.9 1 0", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := GoDiffCoverageInput{
				Dir: ".", Root: "/src", Diff: hunk("m.go", 1, 3),
				Packages: "example.com/m\t/src\n", Profile: "example.com/m/m.go:" + tc.row + "\n",
				Exclude: covExclude, Sources: map[string]string{"m.go": tc.src},
			}
			if got := subjects(GoUncoveredLines(in)); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// A NESTED MODULE's findings name the file from the repository root, and its
// listing is read against the module's own directory.
func TestGoUncoveredLinesInANestedModuleAreRepositoryRelative(t *testing.T) {
	in := covInput(hunk("a.go", 7, 1), covProfile)
	in.Dir, in.Root = "tools/forge", "/src/tools/forge"
	in.Packages = "example.com/cov\t/src/tools/forge\nexample.com/cov/notest\t/elsewhere/notest\n"
	if got := subjects(GoUncoveredLines(in)); got != "tools/forge/a.go:7" {
		t.Errorf("got %q", got)
	}
	// A package listed outside the module's directory places nothing.
	in.Diff = hunk("notest/n.go", 4, 1)
	if got := subjects(GoUncoveredLines(in)); got != "" {
		t.Errorf("a package outside the module was placed: %q", got)
	}
}

func TestGoDiffCoverageFilesAreChangedNonTestNonGeneratedGo(t *testing.T) {
	diff := hunk("b.go", 1, 1) + hunk("a.go", 1, 1) + hunk("a.go", 9, 2) + hunk("a_test.go", 1, 1) +
		hunk("vendor/x/x.go", 1, 1) + hunk("internal/api/api.pb.go", 1, 1) + hunk("README.md", 1, 1) +
		"--- a/gone.go\n+++ /dev/null\n@@ -1,3 +0,0 @@\n" +
		"--- a/c.go\n+++ b/c.go\n@@ -4,2 +3,0 @@\n"
	if got := strings.Join(GoDiffCoverageFiles(diff, covExclude), ","); got != "b.go,a.go" {
		t.Errorf("got %q, want b.go,a.go", got)
	}
	if got := GoDiffCoverageFiles("", covExclude); got != nil {
		t.Errorf("an empty diff names %v", got)
	}
}

func TestGoDiffCoverageVerdictPassesOrNamesEachLine(t *testing.T) {
	a := AtomDef{ID: "go:diff-coverage", Stage: StagePrepush, Lane: LaneGo}
	v := GoDiffCoverageVerdict(a, 3, nil)
	if v.State != 0 || v.Result != "pass" || v.Findings != nil ||
		strings.Join(v.Logs, "\n") != "go:diff-coverage: every changed line in 3 Go file(s) ran under the suite" {
		t.Errorf("a clean module: %+v", v)
	}

	found := GoUncoveredLines(covInput(hunk("a.go", 5, 3), covProfile))
	v = GoDiffCoverageVerdict(a, 1, found)
	want := "go:diff-coverage: 2 changed line(s) no test executes — NOT COVERED, the sharper half of what the mutation lane reports:\n  a.go:6\n  a.go:7"
	if v.State != 1 || v.Result != "findings" || !strings.HasSuffix(v.Reason, want) || len(v.Findings) != 2 {
		t.Errorf("an uncovered module: %+v", v)
	}

	many := make([]Finding, findingCap+5)
	for i := range many {
		many[i] = Finding{Verdict: VerdictViolated, Subject: fmt.Sprintf("a.go:%d", i+1), Cause: GoDiffCoverageCause, Probe: a.ID}
	}
	v = GoDiffCoverageVerdict(a, 1, many)
	if len(v.Findings) != findingCap+1 || v.Findings[findingCap].Cause != "finding-cap" ||
		!strings.Contains(v.Reason, fmt.Sprintf("%d changed line(s)", findingCap+5)) {
		t.Errorf("the cap: %d findings, last %+v", len(v.Findings), v.Findings[len(v.Findings)-1])
	}
}
