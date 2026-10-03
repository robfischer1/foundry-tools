package unitkey

import (
	"reflect"
	"testing"
)

// zeroContext is `git diff --unified=0` over three files: an edit, a new file,
// a deletion, a deletion-only hunk and a rename.
const zeroContext = `diff --git a/internal/a/a.go b/internal/a/a.go
index 1..2 100644
--- a/internal/a/a.go
+++ b/internal/a/a.go
@@ -3 +3,2 @@ func A() {
-	return 1
+	x := 2
+	return x
@@ -10,2 +11,0 @@ func B() {
-	gone()
-	gone()
@@ -20,0 +20 @@ func C() {
+	added()
diff --git a/new.go b/new.go
new file mode 100644
--- /dev/null
+++ b/new.go	2026-10-03 00:00:00
@@ -0,0 +1,3 @@
+package main
+
+func N() {}
diff --git a/old.go b/old.go
deleted file mode 100644
--- a/old.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package main
-func O() {}
`

func TestParseRangesReadsTheAddedLines(t *testing.T) {
	want := map[string][]Range{
		"internal/a/a.go": {{3, 4}, {20, 20}},
		"new.go":          {{1, 3}},
	}
	if got := ParseRanges(zeroContext); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

// TestContextDoesNotWidenTheRanges: the same change with three lines of
// context answers the same ranges, and a content line that looks like a header
// is content.
func TestContextDoesNotWidenTheRanges(t *testing.T) {
	diff := `--- a/x.rs
+++ b/x.rs
@@ -1,6 +1,7 @@
 fn a() {}
 fn b() {}
-fn c() {}
+fn c() { 1 }
++++ weird
 fn d() {}
 fn e() {}
-fn f() {}
\ No newline at end of file
+fn f() { 2 }
\ No newline at end of file
@@ -20,3 +21,3 @@ x
 one
-two
+deux
 three
`
	want := map[string][]Range{"x.rs": {{3, 4}, {7, 7}, {22, 22}}}
	if got := ParseRanges(diff); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestHunkHeader(t *testing.T) {
	for _, c := range []struct {
		in        string
		start, nc int
		ok        bool
	}{
		{"@@ -3 +3,2 @@ f", 3, 2, true},
		{"@@ -3,4 +5 @@", 5, 1, true},
		{"@@ -1 +1", 0, 0, false},
		{"x -1 +1 @@", 0, 0, false},
		{"@@ 1 +1 @@", 0, 0, false},
		{"@@ -1 1 @@", 0, 0, false},
		{"@@ -x +1 @@", 0, 0, false},
		{"@@ -1 +y @@", 0, 0, false},
		{"@@ -1,z +1 @@", 0, 0, false},
		{"@@ -1 +1,z @@", 0, 0, false},
		{"@@ 11 +1 @@", 0, 0, false},
		{"@@ -1 11 @@", 0, 0, false},
	} {
		start, nc, ok := hunkHeader(c.in)
		if start != c.start || nc != c.nc || ok != c.ok {
			t.Errorf("hunkHeader(%q) = %d %d %v", c.in, start, nc, ok)
		}
	}
}

func TestRangesHashesOnlyTheUnitsFiles(t *testing.T) {
	tree := goTree()
	changed := map[string][]Range{
		"internal/a/a.go":    {{3, 4}},
		"internal/b/b.go":    {{1, 1}},
		"migrations/001.sql": {{1, 1}},
	}
	base := Ranges(Go, tree, "internal/a", changed)
	if Ranges(Go, tree, "internal/a", map[string][]Range{"internal/a/a.go": {{3, 4}}}) != base {
		t.Fatal("another unit's or an orphan's ranges must not move this unit's R")
	}
	if Ranges(Go, tree, "internal/a", map[string][]Range{"internal/a/a.go": {{3, 5}}}) == base {
		t.Fatal("a wider range must move R")
	}
	if Ranges(Go, tree, "internal/a", map[string][]Range{"internal/a/a_test.go": {{3, 4}}}) == base {
		t.Fatal("the same range in another file must move R")
	}
	if Ranges(Go, tree, "internal/a", map[string][]Range{"internal/a/a.go": {{3, 4}, {9, 9}}}) == base {
		t.Fatal("a second range must move R")
	}
	if Ranges(Go, tree, "internal/a", nil) == base || Ranges(Go, tree, "internal/a", nil) != Ranges(Go, tree, "internal/c", nil) {
		t.Fatal("no changed file is one fixed R")
	}
	two := map[string][]Range{"internal/a/a.go": {{1, 1}}, "internal/a/z.go": {{2, 2}}, "internal/a/b.go": {{3, 3}}}
	first := Ranges(Go, tree, "internal/a", two)
	for range 20 {
		if Ranges(Go, tree, "internal/a", two) != first {
			t.Fatal("R must not depend on map order")
		}
	}
}
