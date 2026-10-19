package checks

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBoundOutputLeavesWhatFitsAlone(t *testing.T) {
	for _, s := range []string{"", "a\nb\n", strings.Repeat("x", ReasonCap)} {
		if got := BoundOutput(s); got != s {
			t.Errorf("%d bytes changed to %d", len(s), len(got))
		}
	}
}

// A python:pytest atom on stellar-core-go 6b986c9 wrote 23,047,351 bytes of
// Reason. The verdict sentence and the summary at the end must survive.
func TestAHugeOutputGivesABoundedReasonWithItsVerdictAndSummary(t *testing.T) {
	noise := strings.Repeat("tests/test_x.py::test_case PASSED [ 12%]\n", 23_047_351/41)
	out := "collecting ...\n" + noise + "=========== 3 failed, 9000 passed in 812.40s ===========\n"
	for _, tc := range []struct {
		exit   int
		prefix string
	}{{1, "python:pytest: FINDINGS (exit 1)\n"}, {2, "python:pytest: CANNOT RUN (exit 2)"}} {
		v := VerdictOf(atomFor("python:pytest"), tc.exit, out)
		if !strings.HasPrefix(v.Reason, tc.prefix) {
			t.Errorf("exit %d: the verdict sentence is gone: %.60q", tc.exit, v.Reason)
		}
		if len(v.Reason) > ReasonCap+200 {
			t.Errorf("exit %d: Reason is %d bytes, the cap is %d", tc.exit, len(v.Reason), ReasonCap)
		}
		for _, want := range []string{"collecting ...", "3 failed, 9000 passed in 812.40s", "bytes elided — full output in Logs/artifact"} {
			if !strings.Contains(v.Reason, want) {
				t.Errorf("exit %d: Reason lost %q", tc.exit, want)
			}
		}
		if v.OriginalBytes != len(out) || !v.Truncated || len(v.Logs) == 0 {
			t.Errorf("exit %d: Logs must still carry the capped output: truncated=%v original=%d", tc.exit, v.Truncated, v.OriginalBytes)
		}
	}
}

func TestTheMarkerCountsExactlyWhatWasElided(t *testing.T) {
	out := strings.Repeat("0123456789\n", 20_000)
	got := BoundOutput(out)
	head, rest, _ := strings.Cut(got, "… ")
	var n int
	var tail string
	if _, err := fmt.Sscan(rest, &n); err != nil {
		t.Fatal(err)
	}
	_, tail, _ = strings.Cut(rest, "…\n")
	if len(head)+n+len(tail) != len(out) {
		t.Errorf("head %d + elided %d + tail %d != %d", len(head), n, len(tail), len(out))
	}
	if !strings.HasSuffix(head, "\n") || !strings.HasPrefix(tail, "0123456789\n") {
		t.Errorf("the cut must land on lines: head ends %q tail starts %.12q", head[len(head)-3:], tail)
	}
}

func TestBoundOutputNeverSplitsARune(t *testing.T) {
	out := strings.Repeat("é", ReasonCap) // one 2-byte rune, no newline anywhere
	got := BoundOutput(out)
	if !utf8.ValidString(got) {
		t.Error("the bounded output holds a broken rune")
	}
	if len(got) > ReasonCap+200 {
		t.Errorf("%d bytes", len(got))
	}
}

// The mutation atoms' structured summary is the whole of their output; their
// tables open it and the survivors close it, so both survive a bound.
func TestAMutationReasonStillListsItsSurvivors(t *testing.T) {
	var b strings.Builder
	b.WriteString("| file | killed | lived |\n|---|---|---|\n| a.go | 40 | 2 |\n\n")
	b.WriteString(strings.Repeat("_a long explanatory paragraph line the summary carries_\n", 3000))
	b.WriteString("**Survivors** — each is a HYPOTHESIS, not a finding.\n\n```\n")
	b.WriteString("internal/x/y.go:12:5 LIVED conditionals-boundary\ninternal/x/z.go:99:1 NOT COVERED arithmetic\n```\n")
	v := VerdictOf(atomFor("go:mutation"), 1, b.String())
	for _, want := range []string{"| a.go | 40 | 2 |", "**Survivors**", "internal/x/y.go:12:5 LIVED", "internal/x/z.go:99:1 NOT COVERED", "bytes elided"} {
		if !strings.Contains(v.Reason, want) {
			t.Errorf("Reason lost %q", want)
		}
	}
}
