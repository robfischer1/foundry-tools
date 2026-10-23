package checks

import (
	"reflect"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/unitkey"
)

func TestParseMutationRanges(t *testing.T) {
	for _, c := range []struct {
		name, in string
		want     map[string][]unitkey.Range
		err      string
	}{
		{"empty is none", "", map[string][]unitkey.Range{}, ""},
		{"one", "a/b.go:3-5", map[string][]unitkey.Range{"a/b.go": {{Start: 3, End: 5}}}, ""},
		{"single line", "x.go:7", map[string][]unitkey.Range{"x.go": {{Start: 7, End: 7}}}, ""},
		{"merged and sorted", "x.go:9-9, x.go:2-3,x.go:4-4,y.go:1-1", map[string][]unitkey.Range{
			"x.go": {{Start: 2, End: 4}, {Start: 9, End: 9}}, "y.go": {{Start: 1, End: 1}}}, ""},
		{"a colon in the path splits at the last", "a:b.go:2-2", map[string][]unitkey.Range{"a:b.go": {{Start: 2, End: 2}}}, ""},
		{"no line", "x.go", nil, "no line range"},
		{"backwards", "x.go:5-2", nil, "is not a range"},
		{"zero", "x.go:0-2", nil, "is not a range"},
		{"not a number", "x.go:a-2", nil, "is not a range"},
		{"absolute", "/etc/x.go:1-1", nil, "repo-relative"},
		{"escapes", "../x.go:1-1", nil, "repo-relative"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseMutationRanges(c.in)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err = %v, want %q", err, c.err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, %v; want %v", got, err, c.want)
			}
		})
	}
}

// diffU0 is `git diff -M --unified=0 base HEAD` over three files: a.go gains
// two lines after old line 2 and has old line 10 rewritten; old.go is renamed
// to new.go with one line removed at old line 1; gone.go is deleted.
const diffU0 = `diff --git a/a.go b/a.go
index 1..2 100644
--- a/a.go
+++ b/a.go
@@ -2,0 +3,2 @@ func f() {
+	x := 1
+	y := 2
@@ -10 +12 @@ func g() {
-	return a
+	return b
diff --git a/old.go b/new.go
similarity index 90%
rename from old.go
rename to new.go
--- a/old.go
+++ b/new.go
@@ -1 +0,0 @@
-// gone
diff --git a/gone.go b/gone.go
deleted file mode 100644
--- a/gone.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package x
-
-var y = 1
`

func TestMapMutationRangesCarriesUntouchedLinesAndDropsChangedOnes(t *testing.T) {
	extra := map[string][]unitkey.Range{
		"a.go":      {{Start: 1, End: 2}, {Start: 5, End: 5}, {Start: 10, End: 11}},
		"old.go":    {{Start: 4, End: 4}},
		"gone.go":   {{Start: 2, End: 2}},
		"steady.go": {{Start: 8, End: 8}},
	}
	got, moved := MapMutationRanges(extra, diffU0)
	want := map[string][]unitkey.Range{
		// 1-2 sit before the insertion; 5 shifts by 2; 10 was rewritten (tonight's
		// diff re-mutates it) and 11 shifts by 2 (10 replaced by one line, +2 above).
		"a.go":      {{Start: 1, End: 2}, {Start: 7, End: 7}, {Start: 13, End: 13}},
		"new.go":    {{Start: 3, End: 3}},
		"steady.go": {{Start: 8, End: 8}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mapped = %v, want %v", got, want)
	}
	var subjects []string
	for _, f := range moved {
		if f.Verdict != "holds" || f.Cause != MutationCarriedCause {
			t.Fatalf("a carried line is a holds/%s finding, got %+v", MutationCarriedCause, f)
		}
		subjects = append(subjects, f.Subject+" "+f.Detail)
	}
	wantMoved := []string{"a.go:7 from a.go:5", "a.go:13 from a.go:11", "new.go:3 from old.go:4"}
	if !reflect.DeepEqual(subjects, wantMoved) {
		t.Fatalf("moved = %q, want %q", subjects, wantMoved)
	}
}

func TestUnionAndRenderRangesRoundTripThroughParseRanges(t *testing.T) {
	a := map[string][]unitkey.Range{"x.go": {{Start: 1, End: 2}}, "y.go": {{Start: 4, End: 4}}}
	b := map[string][]unitkey.Range{"x.go": {{Start: 3, End: 3}, {Start: 9, End: 9}}}
	u := UnionMutationRanges(a, b)
	want := map[string][]unitkey.Range{"x.go": {{Start: 1, End: 3}, {Start: 9, End: 9}}, "y.go": {{Start: 4, End: 4}}}
	if !reflect.DeepEqual(u, want) {
		t.Fatalf("union = %v, want %v", u, want)
	}
	diff := RenderMutationRanges(u, nil)
	if got := unitkey.ParseRanges(diff); !reflect.DeepEqual(got, want) {
		t.Fatalf("render did not round-trip: %v\n%s", got, diff)
	}
	if !strings.Contains(diff, "+++ b/x.go\n@@ -0,0 +1,3 @@\n") {
		t.Fatalf("render is not a unified diff gomutants reads:\n%s", diff)
	}
	withText := RenderMutationRanges(map[string][]unitkey.Range{"y.go": {{Start: 4, End: 4}}}, func(file string, n int) string {
		return file + "#" + string(rune('0'+n))
	})
	if !strings.Contains(withText, "+y.go#4\n") {
		t.Fatalf("a line source's text is the added line:\n%s", withText)
	}
	if MutationRangesArg(want) != "x.go:1-3,x.go:9-9,y.go:4-4" {
		t.Fatalf("arg = %q", MutationRangesArg(want))
	}
}

func TestPrefixMutationRangesKeepsTheModulesOwnFiles(t *testing.T) {
	r := map[string][]unitkey.Range{"svc/a.go": {{Start: 1, End: 1}}, "other/b.go": {{Start: 2, End: 2}}, "c.go": {{Start: 3, End: 3}}}
	if got := ModuleMutationRanges(r, "svc"); !reflect.DeepEqual(got, map[string][]unitkey.Range{"a.go": {{Start: 1, End: 1}}}) {
		t.Fatalf("svc: %v", got)
	}
	if got := ModuleMutationRanges(r, "."); !reflect.DeepEqual(got, r) {
		t.Fatalf("root keeps all: %v", got)
	}
}
