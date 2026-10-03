package unitkey

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Range is a closed interval of 1-indexed lines on the NEW side of a diff.
type Range struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// ParseRanges reads a unified diff into the added lines of each file, as
// ranges, keyed by the new path. Deleted files and deletion-only hunks add
// nothing: there is nothing on the new side to mutate.
//
// THE ADDED LINES, NOT THE HUNK SPANS, so a diff with context answers the same
// ranges as the same diff at -U0 — which is what gomutants reads (`git diff
// --unified=0 --merge-base`) and what cargo-mutants' -D takes from a diff with
// context (only the `+` lines are changed). The hunk's new-side count decides
// where it ends, so an added line that happens to start with "++ " is content.
func ParseRanges(diff string) map[string][]Range {
	out := map[string][]Range{}
	file := ""
	newLeft, line := 0, 0
	for _, ln := range strings.Split(diff, "\n") {
		if newLeft > 0 {
			switch {
			case strings.HasPrefix(ln, "+"):
				out[file] = addLine(out[file], line)
				line++
				newLeft--
			case strings.HasPrefix(ln, "-"), strings.HasPrefix(ln, `\`):
			default:
				line++
				newLeft--
			}
			continue
		}
		if p, ok := strings.CutPrefix(ln, "+++ "); ok {
			file = newPath(p)
			continue
		}
		if start, nc, ok := hunkHeader(ln); ok {
			line, newLeft = start, nc
		}
	}
	return out
}

// addLine appends one line to a file's ranges, extending the last range when
// the line follows it.
func addLine(rs []Range, line int) []Range {
	if n := len(rs); n > 0 && rs[n-1].End+1 == line {
		rs[n-1].End = line
		return rs
	}
	return append(rs, Range{Start: line, End: line})
}

// newPath is a `+++ ` line's path: its metadata after a tab dropped and git's
// b/ prefix stripped. A deleted file's /dev/null never collects a line — its
// only hunk adds none.
func newPath(p string) string {
	p, _, _ = strings.Cut(p, "\t")
	return strings.TrimPrefix(p, "b/")
}

// hunkHeader reads `@@ -a[,b] +c[,d] @@` into the new side's start and count.
// The old side is checked for shape and otherwise unread: removed lines and
// "\ No newline" markers start with neither "+" nor "@@", so the new side's
// count alone says where a hunk ends.
func hunkHeader(ln string) (start, newCount int, ok bool) {
	f := strings.Fields(ln)
	if len(f) < 4 || f[0] != "@@" || !strings.HasPrefix(f[1], "-") || !strings.HasPrefix(f[2], "+") {
		return 0, 0, false
	}
	_, _, ok1 := span(f[1][1:])
	start, nc, ok2 := span(f[2][1:])
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return start, nc, true
}

// span reads `start[,count]`; a missing count is one line.
func span(s string) (start, count int, ok bool) {
	a, b, hasCount := strings.Cut(s, ",")
	start, err := strconv.Atoi(a)
	if err != nil {
		return 0, 0, false
	}
	count = 1
	if hasCount {
		if count, err = strconv.Atoi(b); err != nil {
			return 0, 0, false
		}
	}
	return start, count, true
}

// Ranges is R: sha256 over the changed ranges of the files unit owns, sorted
// by path. A unit with no changed file hashes the empty list — a grading over
// nothing is still a grading of that scope.
func Ranges(lang Lang, entries []Entry, unit string, changed map[string][]Range) string {
	units := Units(lang, entries)
	files := make([]string, 0, len(changed))
	for f := range changed {
		if own, _ := Owner(lang, f, units); own == unit {
			files = append(files, f)
		}
	}
	slices.Sort(files)
	h := sha256.New()
	fmt.Fprintf(h, "%s ranges\n", Version)
	for _, f := range files {
		for _, r := range changed[f] {
			fmt.Fprintf(h, "%s\x00%d-%d\n", f, r.Start, r.End)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
