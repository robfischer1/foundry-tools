package checks

import (
	"reflect"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/unitkey"
)

func TestMutationCoveredCapReadsTheKnob(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want int
	}{{"", DefaultMutationCoveredCap}, {"x", DefaultMutationCoveredCap}, {"7", 7}, {"0", 0}} {
		t.Setenv(EnvMutationCoveredCap, tc.env)
		if got := MutationCoveredCap(); got != tc.want {
			t.Errorf("%q: %d, want %d", tc.env, got, tc.want)
		}
	}
}

func TestRustMutationCoveredCapIsItsOwnKnob(t *testing.T) {
	t.Setenv(EnvMutationCoveredCap, "7")
	t.Setenv(EnvMutationCoveredCapRust, "")
	if RustMutationCoveredCap() != DefaultMutationCoveredCapRust || DefaultMutationCoveredCapRust != 500 {
		t.Fatal("rust's default")
	}
	t.Setenv(EnvMutationCoveredCapRust, "9")
	if RustMutationCoveredCap() != 9 {
		t.Fatal("rust's knob")
	}
}

func TestCoveredScopeSaysWhatJoinedAndWhatWasCut(t *testing.T) {
	t.Setenv(EnvMutationCoveredCap, "2")
	kept, say := CoveredScope("p", "they run", map[string][]unitkey.Range{"a": {{Start: 1, End: 2}}, "b": {{Start: 4, End: 4}}})
	if len(kept) != 1 || say != "p: changed tests: 2 source line(s) in 1 file(s) they run joined the scope; 1 line(s) in 1 file(s) cut at the "+EnvMutationCoveredCap+" bound (b)" {
		t.Fatalf("%v %q", kept, say)
	}
}

func TestLineRangesAndWholeFile(t *testing.T) {
	if got := LineRanges([]int{3, 0, 1, 2, 7}); !reflect.DeepEqual(got, []unitkey.Range{{Start: 1, End: 3}, {Start: 7, End: 7}}) {
		t.Errorf("LineRanges %v", got)
	}
	for src, want := range map[string][]unitkey.Range{"": nil, "a": {{Start: 1, End: 1}}, "a\nb\n": {{Start: 1, End: 2}}} {
		if got := WholeFile(src); !reflect.DeepEqual(got, want) {
			t.Errorf("WholeFile(%q) = %v", src, got)
		}
	}
	if RangeLines(map[string][]unitkey.Range{"a": {{Start: 2, End: 4}}, "b": {{Start: 1, End: 1}}}) != 4 {
		t.Error("RangeLines")
	}
}

func TestKeepCoveredDropsWhatKeepRefuses(t *testing.T) {
	got := KeepCovered(map[string][]unitkey.Range{"a": {{Start: 1, End: 1}}, "b": {{Start: 2, End: 2}}}, func(f string) bool { return f == "a" })
	if !reflect.DeepEqual(got, map[string][]unitkey.Range{"a": {{Start: 1, End: 1}}}) {
		t.Fatalf("%v", got)
	}
}

func TestUnderAny(t *testing.T) {
	for f, want := range map[string]bool{"src/a.py": true, "src": true, "srcx/a.py": false, "lib/a.py": false} {
		if UnderAny(f, []string{"src/"}) != want {
			t.Errorf("%s", f)
		}
	}
	if !UnderAny("x", []string{"."}) {
		t.Error("the root holds everything")
	}
}

func TestTestFilePredicates(t *testing.T) {
	for p, want := range map[string]bool{"test_a.py": true, "pkg/a_test.py": true, "tests/helpers.py": true, "a/tests/conftest.py": true, "src/a.py": false, "tests/data.json": false} {
		if PythonTestFile(p) != want {
			t.Errorf("PythonTestFile(%s)", p)
		}
	}
	if !PythonTestModule("tests/test_a.py") || PythonTestModule("tests/helpers.py") || !PythonTestModule("a_test.py") {
		t.Error("PythonTestModule")
	}
	for p, want := range map[string]bool{"a.test.ts": true, "b.spec.tsx": true, "c.test.mjs": true, "__tests__/d.ts": true, "__tests__/d.json": false, "src/e.ts": false} {
		if TSTestFile(p) != want {
			t.Errorf("TSTestFile(%s)", p)
		}
	}
	for p, want := range map[string]bool{"src/a.ts": true, "a.tsx": true, "a.test.ts": false, "vite.config.ts": false, "a.d.ts": false, "node_modules/x/a.ts": false, "a.js": false} {
		if TSUndeclaredSource(p) != want {
			t.Errorf("TSUndeclaredSource(%s)", p)
		}
	}
	for p, want := range map[string]bool{"tests/it.rs": true, "crates/a/benches/b.rs": true, "src/lib.rs": false, "tests/x.txt": false} {
		if RustTestFile(p) != want {
			t.Errorf("RustTestFile(%s)", p)
		}
	}
}

func TestPythonCoveredRangesNamesFromTheTree(t *testing.T) {
	got, err := PythonCoveredRanges(`{"files":{"a.py":{"executed_lines":[1,2]},"/t/b.py":{"executed_lines":[3]},"/other/c.py":{"executed_lines":[1]},"../d.py":{"executed_lines":[1]},"e.py":{"executed_lines":[]}}}`, "/t")
	want := map[string][]unitkey.Range{"a.py": {{Start: 1, End: 2}}, "b.py": {{Start: 3, End: 3}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := PythonCoveredRanges("not json", "/t"); err == nil || !strings.Contains(err.Error(), "did not parse") {
		t.Fatal(err)
	}
}

func TestLCOVRangesNamesFromTheTree(t *testing.T) {
	lcov := "TN:\nSF:src/a.ts\nDA:1,1\nDA:2,0\nDA:3,4,abc\nDA:x,1\nend_of_record\nSF:/t/pkg/b.ts\nDA:5,1\nSF:/elsewhere/c.ts\nDA:1,1\n"
	got := LCOVRanges(lcov, "/t", "pkg")
	want := map[string][]unitkey.Range{"pkg/src/a.ts": {{Start: 1, End: 1}, {Start: 3, End: 3}}, "pkg/b.ts": {{Start: 5, End: 5}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestStrykerOwnerIsTheNearestConfig(t *testing.T) {
	dirs := map[string]bool{".": true, "apps/a": true}
	if StrykerOwner("apps/a/src/x.ts", dirs) != "apps/a" || StrykerOwner("lib/x.ts", dirs) != "." || StrykerOwner("x.ts", map[string]bool{}) != "" {
		t.Fatal("owner")
	}
}

func TestRustTestModSpans(t *testing.T) {
	src := strings.Join([]string{
		"fn a() {}",                  // 1
		"#[cfg(test)]",               // 2
		"mod tests {",                // 3
		`    const S: &str = "}";`,   // 4
		"    const C: char = '}';",   // 5
		"    // a } in a comment",    // 6
		`    const E: &str = "\"}";`, // 7
		"    fn t() { if true { } }", // 8
		"}",                          // 9
		"fn b() {}",                  // 10
		"#[cfg(test)]",               // 11
		"mod more;",                  // 12
		"#[cfg(test)]",               // 13
		"fn helper() {}",             // 14
		"#[cfg(test)]",               // 15
		"fn open() {",                // 16
	}, "\n")
	want := []unitkey.Range{{Start: 2, End: 9}, {Start: 11, End: 12}, {Start: 13, End: 14}, {Start: 15, End: 16}}
	if got := RustTestModSpans(src); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}
