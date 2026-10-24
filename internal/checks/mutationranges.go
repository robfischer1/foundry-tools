package checks

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/unitkey"
)

// THE EXTRA SCOPE A NIGHTLY MUTATION RUN IS HANDED (Rob, 2026-10-23: "cumulative
// since last green, but re-test since last run"). The atom stays stateless: it
// mutates what changed since its base, as it always has, PLUS the line ranges the
// runner names here — the survivors it rolled up on earlier nights. Daedalus owns
// the rollup; the atom only widens its diff by it.
//
// THE RANGES ARE IN THE BASE'S COORDINATES, because that is the commit the runner
// last graded and so the only one its survivors' lines are numbered against. The
// atom maps them to HEAD through `git diff -M --unified=0 base HEAD`
// (MapMutationRanges): a line no hunk touched moves by the lines added and
// removed above it; a line a hunk rewrote or removed is dropped, because the
// night's own diff mutates whatever replaced it.
//
// THE WIRE IS ONE ENV VALUE, `path:start-end[,path:start-end...]`, repo-relative
// and 1-indexed — argv/env only, no file and no shell (Rob's atom law). A path is
// split from its range at its LAST colon, so a colon in a path survives; a comma
// cannot travel, and the runner refuses such a path before it asks.

// EnvMutationRanges names the extra ranges' variable.
const EnvMutationRanges = "ATOM_MUTATION_RANGES"

// MutationCarriedCause is the cause of the finding a carried line that moved
// leaves, so the runner can follow a survivor across a shift: subject the
// line's HEAD position, detail "from <base position>".
const MutationCarriedCause = "mutation-carried"

// ParseMutationRanges reads EnvMutationRanges' value into sorted, merged ranges
// per file. "" is no extra scope.
func ParseMutationRanges(s string) (map[string][]unitkey.Range, error) {
	out := map[string][]unitkey.Range{}
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		cut := strings.LastIndexByte(item, ':')
		if cut <= 0 {
			return nil, fmt.Errorf("%s: %q has no line range", EnvMutationRanges, item)
		}
		file, span := item[:cut], item[cut+1:]
		if path.IsAbs(file) || path.Clean(file) != file || file == ".." || strings.HasPrefix(file, "../") {
			return nil, fmt.Errorf("%s: %q is not a clean repo-relative path", EnvMutationRanges, file)
		}
		r, ok := parseSpan(span)
		if !ok {
			return nil, fmt.Errorf("%s: %q in %q is not a range start-end of lines from 1", EnvMutationRanges, span, item)
		}
		out[file] = append(out[file], r)
	}
	for f, rs := range out {
		out[f] = mergeRanges(rs)
	}
	return out, nil
}

// parseSpan reads `start[-end]`, 1 <= start <= end.
func parseSpan(s string) (unitkey.Range, bool) {
	a, b, has := strings.Cut(s, "-")
	if !has {
		b = a
	}
	start, err1 := strconv.Atoi(a)
	end, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || start < 1 || end < start {
		return unitkey.Range{}, false
	}
	return unitkey.Range{Start: start, End: end}, true
}

// mergeRanges sorts ranges and joins the ones that overlap or touch.
func mergeRanges(rs []unitkey.Range) []unitkey.Range {
	rs = slices.Clone(rs)
	slices.SortFunc(rs, func(x, y unitkey.Range) int { return x.Start - y.Start })
	var out []unitkey.Range
	for _, r := range rs {
		if n := len(out); n > 0 && r.Start <= out[n-1].End+1 {
			out[n-1].End = max(out[n-1].End, r.End)
			continue
		}
		out = append(out, r)
	}
	return out
}

// UnionMutationRanges is every line either side names, merged per file.
func UnionMutationRanges(a, b map[string][]unitkey.Range) map[string][]unitkey.Range {
	out := map[string][]unitkey.Range{}
	for _, m := range []map[string][]unitkey.Range{a, b} {
		for f, rs := range m {
			out[f] = append(out[f], rs...)
		}
	}
	for f, rs := range out {
		out[f] = mergeRanges(rs)
	}
	return out
}

// ModuleMutationRanges keeps a module's own files, named from the module's
// directory ("." keeps every file as it is).
func ModuleMutationRanges(r map[string][]unitkey.Range, dir string) map[string][]unitkey.Range {
	if dir == "." || dir == "" {
		return r
	}
	out := map[string][]unitkey.Range{}
	for f, rs := range r {
		if rel, ok := strings.CutPrefix(f, dir+"/"); ok {
			out[rel] = rs
		}
	}
	return out
}

// MutationRangesArg renders ranges as EnvMutationRanges' value, files sorted.
func MutationRangesArg(r map[string][]unitkey.Range) string {
	var parts []string
	for _, f := range slices.Sorted(maps.Keys(r)) {
		for _, x := range r[f] {
			parts = append(parts, fmt.Sprintf("%s:%d-%d", f, x.Start, x.End))
		}
	}
	return strings.Join(parts, ",")
}

// RenderMutationRanges writes ranges as a unified diff that adds exactly those
// lines: one `+++ b/<file>` section per file and one `@@ -0,0 +start,count @@`
// hunk per range, files sorted. gomutants reads only the headers;
// unitkey.ParseRanges counts the `+` lines; cargo-mutants checks each `+` line
// against the tree, so text, when set, answers line n of file (nil leaves the
// lines empty).
func RenderMutationRanges(r map[string][]unitkey.Range, text func(file string, n int) string) string {
	var b strings.Builder
	for _, f := range slices.Sorted(maps.Keys(r)) {
		if len(r[f]) == 0 {
			continue
		}
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n", f, f, f, f)
		for _, x := range r[f] {
			fmt.Fprintf(&b, "@@ -0,0 +%d,%d @@\n", x.Start, x.End-x.Start+1)
			for n := x.Start; n <= x.End; n++ {
				line := ""
				if text != nil {
					line = text(f, n)
				}
				b.WriteString("+" + line + "\n")
			}
		}
	}
	return b.String()
}
