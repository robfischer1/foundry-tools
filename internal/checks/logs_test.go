package checks

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCaptureLogs(t *testing.T) {
	for _, c := range []struct {
		name      string
		output    string
		wantLines []string
		wantTrunc bool
		wantBytes int
	}{
		{name: "empty output is an EMPTY list, not nil — it printed nothing, it was not unset",
			output: "", wantLines: []string{}, wantBytes: 0},
		{name: "one line",
			output: "ok\tdagger/ourea\t0.4s", wantLines: []string{"ok\tdagger/ourea\t0.4s"}, wantBytes: 20},
		{name: "a trailing newline does not manufacture an empty last line",
			output: "a\nb\n", wantLines: []string{"a", "b"}, wantBytes: 4},
		{name: "interior blank lines are kept — they are the atom's own spacing",
			output: "a\n\nb", wantLines: []string{"a", "", "b"}, wantBytes: 4},
		{name: "a pass with output keeps it (Rob: we don't trust absence as evidence)",
			output: "go:vet: PASS", wantLines: []string{"go:vet: PASS"}, wantBytes: 12},
	} {
		t.Run(c.name, func(t *testing.T) {
			logs, trunc, n := CaptureLogs(c.output)
			if trunc != c.wantTrunc {
				t.Fatalf("truncated = %v, want %v", trunc, c.wantTrunc)
			}
			if n != c.wantBytes {
				t.Fatalf("originalBytes = %d, want %d", n, c.wantBytes)
			}
			if len(logs) != len(c.wantLines) {
				t.Fatalf("logs = %q, want %q", logs, c.wantLines)
			}
			for i := range logs {
				if logs[i] != c.wantLines[i] {
					t.Fatalf("logs[%d] = %q, want %q", i, logs[i], c.wantLines[i])
				}
			}
		})
	}
}

// AN EMPTY RESULT MUST BE NON-NIL, because nil marshals to null and null is
// "nobody set this", not "this atom printed nothing".
func TestEmptyOutputIsNonNil(t *testing.T) {
	logs, _, _ := CaptureLogs("")
	if logs == nil {
		t.Fatal("empty output must answer an empty slice, never nil — null and [] are different facts")
	}
}

// THE CUT IS STATED AND THE TRUE SIZE SURVIVES IT. This is the property the
// whole plan exists for: a reader can tell how much they are NOT seeing.
func TestCaptureLogsStatesItsCut(t *testing.T) {
	line := strings.Repeat("x", 999) + "\n"
	big := strings.Repeat(line, 2000) // ~2 MB, comfortably over the 1 MB cap
	if len(big) <= AtomLogCap {
		t.Fatalf("fixture is not over the cap: %d <= %d", len(big), AtomLogCap)
	}
	logs, trunc, n := CaptureLogs(big)
	if !trunc {
		t.Fatal("output over the cap must say it was truncated")
	}
	if n != len(big) {
		t.Fatalf("originalBytes = %d, want the TRUE pre-cut size %d — the carried size is not the original", n, len(big))
	}
	carried := len(strings.Join(logs, "\n"))
	if carried > AtomLogCap {
		t.Fatalf("carried %d bytes, cap is %d", carried, AtomLogCap)
	}
	// The TAIL survives: a failing tool explains itself at the end.
	if got := logs[len(logs)-1]; got != strings.Repeat("x", 999) {
		t.Fatalf("the last line should be the output's last line, got %q", got)
	}
}

func TestUnderTheCapIsNotClaimedTruncated(t *testing.T) {
	out := strings.Repeat("y", AtomLogCap-1)
	logs, trunc, n := CaptureLogs(out)
	if trunc {
		t.Fatal("output under the cap must not claim truncation")
	}
	if n != AtomLogCap-1 {
		t.Fatalf("originalBytes = %d, want %d", n, AtomLogCap-1)
	}
	if len(logs) != 1 || len(logs[0]) != AtomLogCap-1 {
		t.Fatalf("output under the cap is carried whole; got %d lines", len(logs))
	}
}

func TestExactlyTheCapIsNotTruncated(t *testing.T) {
	out := strings.Repeat("z", AtomLogCap)
	_, trunc, n := CaptureLogs(out)
	if trunc {
		t.Fatal("output exactly at the cap is not over it")
	}
	if n != AtomLogCap {
		t.Fatalf("originalBytes = %d, want %d", n, AtomLogCap)
	}
}

// A CUT MUST NOT SEVER A RUNE. The fleet paid for this once: a byte-sliced
// reason carrying box-drawing characters was refused by Postgres outright
// ("invalid byte sequence for encoding UTF8"), so a red settle with a real
// finding was never written (ourea, 2026-09-10).
func TestACutNeverSeversARune(t *testing.T) {
	// Box-drawing and accents, so the cap boundary lands mid-rune for at
	// least some of the offsets swept below.
	unit := "│ ✘ café ┆ ok\n"
	for pad := 0; pad < 8; pad++ {
		out := strings.Repeat("a", pad) + strings.Repeat(unit, (AtomLogCap/len(unit))+64)
		logs, trunc, _ := CaptureLogs(out)
		if !trunc {
			t.Fatalf("pad %d: fixture should exceed the cap", pad)
		}
		joined := strings.Join(logs, "\n")
		if !utf8.ValidString(joined) {
			t.Fatalf("pad %d: the cut severed a rune — the carried text is not valid UTF-8", pad)
		}
		if strings.ContainsRune(joined, utf8.RuneError) {
			t.Fatalf("pad %d: the carried text contains U+FFFD — a rune was broken", pad)
		}
	}
}

// A VALID MULTI-BYTE RUNE AT THE CUT IS KEPT, not eaten. The first
// implementation walked forward while `utf8.ValidString(kept[:1])` was false,
// which is true of the LEAD byte of every multi-byte rune — so it consumed the
// very characters the guard exists to protect.
func TestTheGuardDoesNotEatValidRunes(t *testing.T) {
	// Build output whose byte at the cut boundary is the lead byte of "é".
	head := strings.Repeat("a", 32)
	body := strings.Repeat("é", AtomLogCap)
	out := head + body
	logs, trunc, _ := CaptureLogs(out)
	if !trunc {
		t.Fatal("fixture should exceed the cap")
	}
	joined := strings.Join(logs, "\n")
	if !strings.HasPrefix(joined, "é") {
		t.Fatalf("a valid rune at the cut was eaten; carried text starts %q", joined[:8])
	}
	if !utf8.ValidString(joined) {
		t.Fatal("carried text is not valid UTF-8")
	}
}

// A CUT THAT IS MID-RUNE BY CONSTRUCTION, not by hope.
//
// The first version of this file swept a few padding offsets over a fixture of
// two-byte runes and called that coverage. It was not: "é" is 2 bytes and
// AtomLogCap is even, so the cut index was ALWAYS even and ALWAYS landed on a
// rune boundary — the guard's loop body never executed once, and the mutation
// lane said so by leaving four survivors on it. A 3-byte rune makes the cut
// land mid-rune for arithmetic reasons (AtomLogCap mod 3 == 1), and the test
// asserts that precondition rather than trusting it.
func TestTheCutIsMidRuneByConstruction(t *testing.T) {
	const r = "✘" // 3 bytes
	out := strings.Repeat(r, (AtomLogCap/len(r))+1024)
	cut := len(out) - AtomLogCap
	if cut <= 0 {
		t.Fatalf("fixture is not over the cap")
	}
	// PRECONDITION: the cut must NOT land on a rune boundary, or this test
	// proves nothing — which is exactly how the previous version passed while
	// covering nothing.
	if cut%len(r) == 0 {
		t.Fatalf("cut at %d lands on a rune boundary — this fixture does not exercise the guard", cut)
	}
	if utf8.RuneStart(out[cut]) {
		t.Fatalf("byte at the cut is a rune start — fixture does not exercise the guard")
	}

	logs, trunc, n := CaptureLogs(out)
	if !trunc || n != len(out) {
		t.Fatalf("truncated=%v originalBytes=%d, want true/%d", trunc, n, len(out))
	}
	joined := strings.Join(logs, "\n")
	if !utf8.ValidString(joined) {
		t.Fatal("the cut severed a rune — carried text is not valid UTF-8")
	}
	if strings.ContainsRune(joined, utf8.RuneError) {
		t.Fatal("carried text contains U+FFFD — a rune was broken")
	}
	if !strings.HasPrefix(joined, r) {
		t.Fatalf("the guard should have skipped exactly the partial rune; got prefix %q", joined[:6])
	}
}

// A LITERAL U+FFFD IN THE OUTPUT IS CONTENT, NOT DAMAGE. An atom may legitimately
// print the replacement character; the guard must keep it rather than eat it,
// which is why the test is "RuneError AND size 1", not "RuneError".
func TestALiteralReplacementCharacterIsKept(t *testing.T) {
	const r = "�" // 3 bytes, a VALID encoding of U+FFFD
	out := strings.Repeat(r, (AtomLogCap/len(r))+1024)
	logs, trunc, _ := CaptureLogs(out)
	if !trunc {
		t.Fatal("fixture should exceed the cap")
	}
	joined := strings.Join(logs, "\n")
	if !utf8.ValidString(joined) {
		t.Fatal("carried text is not valid UTF-8")
	}
	if !strings.Contains(joined, r) {
		t.Fatal("a legitimately-printed U+FFFD was eaten by the guard")
	}
}

// OUTPUT THAT IS ENTIRELY UNREADABLE ANSWERS NO LINES, NOT ONE EMPTY ONE — and
// above all it does not panic walking off the end of the string.
func TestEntirelyInvalidOutputAnswersNoLines(t *testing.T) {
	out := strings.Repeat("\x80", AtomLogCap+64) // continuation bytes: no rune starts anywhere
	if utf8.ValidString(out) {
		t.Fatal("fixture should be invalid UTF-8")
	}
	logs, trunc, n := CaptureLogs(out)
	if !trunc {
		t.Fatal("fixture should exceed the cap")
	}
	if n != len(out) {
		t.Fatalf("originalBytes = %d, want the TRUE size %d — the count survives even when the content does not", n, len(out))
	}
	if len(logs) != 0 {
		t.Fatalf("unreadable output must answer no lines, got %d: %q", len(logs), logs)
	}
	if logs == nil {
		t.Fatal("...and still not nil")
	}
}
