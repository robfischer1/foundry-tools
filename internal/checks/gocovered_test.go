package checks

import (
	"reflect"
	"testing"

	"dagger/foundry-tools/internal/unitkey"
)

const testSrc = `package x

import "testing"

func helper() int { return 1 }

func TestA(t *testing.T) {
	_ = helper()
}

func TestB(t *testing.T) {
}

func (s suite) TestMethod() {}

func Example_b() {}
`

func TestGoChangedTests(t *testing.T) {
	for _, c := range []struct {
		name    string
		changed []unitkey.Range
		want    []string
	}{
		{"inside one test", []unitkey.Range{{Start: 8, End: 8}}, []string{"TestA"}},
		{"across two", []unitkey.Range{{Start: 9, End: 9}, {Start: 11, End: 11}}, []string{"TestA", "TestB"}},
		{"a helper changes every test of the file", []unitkey.Range{{Start: 5, End: 5}}, []string{"Example_b", "TestA", "TestB"}},
		{"a method is not a test", []unitkey.Range{{Start: 14, End: 14}}, []string{"Example_b", "TestA", "TestB"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := GoChangedTests([]byte(testSrc), c.changed)
			if !ok || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v %v, want %v", got, ok, c.want)
			}
		})
	}
	if _, ok := GoChangedTests([]byte("not go"), nil); ok {
		t.Fatal("a file that does not parse answers ok")
	}
	if p := GoTestRunPattern([]string{"TestA", "Example_b"}); p != "^(TestA|Example_b)$" {
		t.Fatalf("pattern %q", p)
	}
}

func TestGoCoveredRangesReadsTheBlocksThatRan(t *testing.T) {
	profile := "mode: set\n" +
		"m/x/a.go:3.10,5.2 2 1\n" +
		"m/x/a.go:6.1,6.20 1 0\n" +
		"m/x/a.go:7.1,8.2 1 3\n" +
		"m/a_test.go:1.1,2.2 1 1\n" +
		"other/z.go:1.1,2.2 1 1\n" +
		"garbage\n"
	got := GoCoveredRanges(profile, "m")
	want := map[string][]unitkey.Range{"x/a.go": {{Start: 3, End: 5}, {Start: 7, End: 8}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCapMutationRangesKeepsWholeFilesAndSaysWhatItCut(t *testing.T) {
	r := map[string][]unitkey.Range{"a.go": {{Start: 1, End: 3}}, "b.go": {{Start: 1, End: 5}}, "c.go": {{Start: 9, End: 9}}}
	kept, lines, files := CapMutationRanges(r, 4)
	if !reflect.DeepEqual(kept, map[string][]unitkey.Range{"a.go": {{Start: 1, End: 3}}, "c.go": {{Start: 9, End: 9}}}) || lines != 5 || !reflect.DeepEqual(files, []string{"b.go"}) {
		t.Fatalf("kept %v cut %d %v", kept, lines, files)
	}
	if kept, lines, _ := CapMutationRanges(r, 0); len(kept) != 3 || lines != 0 {
		t.Fatal("no limit keeps all")
	}
}
