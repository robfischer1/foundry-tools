package checks

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/unitkey"
)

// diffHunk is one `@@ -os,oc +ns,nc @@` header of a -U0 diff.
type diffHunk struct{ os, oc, ns, nc int }

// diffFile is one file's section of a diff: where it was, where it is (""
// for /dev/null) and its hunks.
type diffFile struct {
	old, new string
	hunks    []diffHunk
}

// parseDiffFiles reads a `git diff -M --unified=0` into its file sections,
// keyed by the OLD path. A pure rename has no hunks and still moves the file.
func parseDiffFiles(diff string) map[string]*diffFile {
	out := map[string]*diffFile{}
	var cur *diffFile
	for _, ln := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(ln, "diff --git "):
			a, b, ok := strings.Cut(strings.TrimPrefix(ln, "diff --git "), " b/")
			if !ok {
				cur = nil
				continue
			}
			cur = &diffFile{old: strings.TrimPrefix(a, "a/"), new: b}
			out[cur.old] = cur
		case cur == nil:
		case strings.HasPrefix(ln, "rename to "):
			cur.new = strings.TrimPrefix(ln, "rename to ")
		case strings.HasPrefix(ln, "+++ "):
			if p := strings.TrimPrefix(ln, "+++ "); p == "/dev/null" {
				cur.new = ""
			} else {
				cur.new = strings.TrimPrefix(p, "b/")
			}
		case strings.HasPrefix(ln, "@@ "):
			if h, ok := parseHunk(ln); ok {
				cur.hunks = append(cur.hunks, h)
			}
		}
	}
	return out
}

// parseHunk reads both sides of a hunk header.
func parseHunk(ln string) (diffHunk, bool) {
	f := strings.Fields(ln)
	if len(f) < 4 || !strings.HasPrefix(f[1], "-") || !strings.HasPrefix(f[2], "+") {
		return diffHunk{}, false
	}
	os, oc, ok1 := hunkSpan(f[1][1:])
	ns, nc, ok2 := hunkSpan(f[2][1:])
	return diffHunk{os, oc, ns, nc}, ok1 && ok2
}

// hunkSpan reads `start[,count]`; a missing count is one line.
func hunkSpan(s string) (start, count int, ok bool) {
	a, b, has := strings.Cut(s, ",")
	if _, err := fmt.Sscanf(a, "%d", &start); err != nil {
		return 0, 0, false
	}
	count = 1
	if has {
		if _, err := fmt.Sscanf(b, "%d", &count); err != nil {
			return 0, 0, false
		}
	}
	return start, count, true
}

// mapLine is where old line l sits after the file's hunks, or false when a
// hunk rewrote or removed it.
func mapLine(hunks []diffHunk, l int) (int, bool) {
	delta := 0
	for _, h := range hunks {
		if h.oc > 0 && h.os <= l && l <= h.os+h.oc-1 {
			return 0, false
		}
		if (h.oc > 0 && h.os+h.oc-1 < l) || (h.oc == 0 && h.os < l) {
			delta += h.nc - h.oc
		}
	}
	return l + delta, true
}

// MapMutationRanges moves extra ranges from the base's lines to HEAD's through
// diff (`git diff -M --unified=0 base HEAD`), and answers a holds finding for
// every carried line whose position changed. A file the diff does not name is
// carried as it is; a deleted file carries nothing.
func MapMutationRanges(extra map[string][]unitkey.Range, diff string) (map[string][]unitkey.Range, []Finding) {
	files := parseDiffFiles(diff)
	out := map[string][]unitkey.Range{}
	var moved []Finding
	for _, f := range slices.Sorted(maps.Keys(extra)) {
		sec, changed := files[f]
		if !changed {
			out[f] = append(out[f], extra[f]...)
			continue
		}
		if sec.new == "" {
			continue
		}
		for _, r := range extra[f] {
			for l := r.Start; l <= r.End; l++ {
				to, ok := mapLine(sec.hunks, l)
				if !ok {
					continue
				}
				out[sec.new] = append(out[sec.new], unitkey.Range{Start: to, End: to})
				if to != l || sec.new != f {
					moved = append(moved, Finding{Verdict: "holds", Subject: fmt.Sprintf("%s:%d", sec.new, to),
						Cause: MutationCarriedCause, Detail: fmt.Sprintf("from %s:%d", f, l)})
				}
			}
		}
	}
	for f, rs := range out {
		out[f] = mergeRanges(rs)
	}
	return out, moved
}
