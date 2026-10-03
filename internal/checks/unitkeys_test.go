package checks

import (
	"errors"
	"path"
	"reflect"
	"testing"

	"dagger/foundry-tools/internal/unitkey"
)

func TestParseLsTreeKeepsOnlyBlobs(t *testing.T) {
	out := "100644 blob aaa\tgo.mod\x00160000 commit bbb\tvendor/sub\x00100755 blob ccc\tdir/with space.go\x00garbage\x00100644 blob\tshort\x00"
	want := []unitkey.Entry{{Path: "go.mod", Blob: "aaa"}, {Path: "dir/with space.go", Blob: "ccc"}}
	if got := ParseLsTree(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

// goListOut is `go list -e -deps -test -f GoListDepsFormat ./...` over a
// module at /src with packages a (importing b), b, c, an x-test of a that
// imports c, the standard library and a module-cache dependency.
const goListOut = "fmt\t/usr/local/go/src/fmt\terrors io\n" +
	"example.com/dep\t/root/go/pkg/mod/example.com/dep@v1\tfmt\n" +
	"m/internal/b\t/src/internal/b\tfmt example.com/dep\n" +
	"m/internal/c\t/src/internal/c\tfmt\n" +
	"m/internal/a\t/src/internal/a\tfmt m/internal/b\n" +
	"m/internal/a [m/internal/a.test]\t/src/internal/a\tfmt m/internal/b\n" +
	"m/internal/a_test [m/internal/a.test]\t/src/internal/a\tfmt m/internal/a [m/internal/a.test] m/internal/c\n" +
	"m/internal/a.test\t/src/internal/a\tfmt m/internal/a [m/internal/a.test] m/internal/a_test [m/internal/a.test] m/internal/b m/internal/c\n" +
	"m\t/src\tfmt\n" +
	"broken\t\t\n" +
	"not a record\n" +
	"m/internal/d\t/src/internal/d"

func TestGoClosuresAreEachPackagesTestBinary(t *testing.T) {
	got := GoClosures(goListOut, "/src")
	want := map[string][]string{
		"internal/a": {"internal/a", "internal/b", "internal/c"},
		"internal/b": {"internal/b"},
		"internal/c": {"internal/c"},
		"internal/d": {"internal/d"},
		".":          {"."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	if got := GoClosures(goListOut, "/elsewhere"); len(got) != 0 {
		t.Fatalf("no package under the root, no closure: %v", got)
	}
}

func TestUnitKeysKeyEveryChangedUnit(t *testing.T) {
	entries := []unitkey.Entry{
		{Path: "go.mod", Blob: "m"}, {Path: "main.go", Blob: "r"},
		{Path: "internal/a/a.go", Blob: "a"}, {Path: "internal/a/a_test.go", Blob: "at"},
		{Path: "internal/b/b.go", Blob: "b"}, {Path: "internal/c/c.go", Blob: "c"}, {Path: "migrations/1.sql", Blob: "s"},
	}
	closures := GoClosures(goListOut, "/src")
	diff := "+++ b/internal/a/a.go\n@@ -1 +1,2 @@\n+x\n+y\n+++ b/internal/c/c.go\n@@ -5 +5 @@\n+z\n"
	// An orphan and a unit already keyed are passed over, never the end of
	// the list.
	changed := []string{"migrations/1.sql", "internal/a/a.go", "internal/a/a_test.go", "internal/c/c.go"}
	keys, owner := UnitKeys(unitkey.Go, entries, diff, closures, changed, "")
	if len(keys) != 2 || keys[0].Unit != "internal/a" || keys[1].Unit != "internal/c" {
		t.Fatalf("keys = %+v (one per changed unit, in path order, an orphan keys nothing)", keys)
	}
	a := keys[0]
	if a.Err != "" || a.Hash != unitkey.Hash(unitkey.Go, entries, closures["internal/a"]) || !reflect.DeepEqual(a.Closure, closures["internal/a"]) ||
		a.Ranges != unitkey.Ranges(unitkey.Go, entries, "internal/a", unitkey.ParseRanges(diff)) {
		t.Fatalf("a = %+v", a)
	}
	if keys[1].Ranges == a.Ranges || keys[1].Hash == a.Hash {
		t.Fatal("two units, two keys")
	}
	if u, ok := owner("internal/b/b.go"); !ok || u != "internal/b" {
		t.Fatalf("owner = %q %v", u, ok)
	}
	if _, ok := owner("migrations/1.sql"); ok {
		t.Fatal("an orphan has no owner")
	}

	sorted, _ := UnitKeys(unitkey.Go, entries, diff, closures, []string{"internal/c/c.go", "internal/a/a.go"}, "")
	if len(sorted) != 2 || sorted[0].Unit != "internal/a" || sorted[1].Unit != "internal/c" {
		t.Fatalf("keys come in path order, whatever order the diff named them: %+v", sorted)
	}
	missing, _ := UnitKeys(unitkey.Go, entries, diff, map[string][]string{}, []string{"internal/a/a.go"}, "")
	if len(missing) != 1 || missing[0].Err == "" || missing[0].Hash != "" {
		t.Fatalf("a unit the toolchain did not list is unkeyable: %+v", missing)
	}
	failed, _ := UnitKeys(unitkey.Go, entries, diff, closures, []string{"internal/a/a.go"}, "go list exited 1")
	if len(failed) != 1 || failed[0].Err != "go list exited 1" || failed[0].Hash != "" || failed[0].Ranges != "" {
		t.Fatalf("a read that failed keys nothing: %+v", failed)
	}
	both, _ := UnitKeys(unitkey.Go, entries, diff, map[string][]string{}, []string{"internal/a/a.go"}, "git diff exited 128")
	if len(both) != 1 || both[0].Err != "git diff exited 128" {
		t.Fatalf("the read that failed is the reason, not the closure it left out: %+v", both)
	}
}

func TestInModule(t *testing.T) {
	if got := InModule("tools/forge", []string{"a.go", " ", "x/b.go\n"}); !reflect.DeepEqual(got, []string{"tools/forge/a.go", "tools/forge/x/b.go"}) {
		t.Fatalf("got %v", got)
	}
	if got := InModule(".", []string{"a.go"}); !reflect.DeepEqual(got, []string{"a.go"}) {
		t.Fatalf("root: %v", got)
	}
}

func TestReadFailed(t *testing.T) {
	if got := ReadFailed("go list", 0, nil); got != "" {
		t.Fatalf("an answered read: %q", got)
	}
	if got := ReadFailed("go list", 2, nil); got != "go list exited 2" {
		t.Fatalf("a non-zero exit: %q", got)
	}
	if got := ReadFailed("go list", 0, errors.New("engine gone")); got != "go list never ran: engine gone" {
		t.Fatalf("an engine error: %q", got)
	}
}

func TestGoGradingsMoveTheModuleOntoTheRoot(t *testing.T) {
	fresh := []ScoredMutant{{File: "x/a.go", Line: 1, Outcome: OutcomeLived}, {File: "x/a.go", Line: 2, Outcome: OutcomeKilled}}
	owner := func(f string) (string, bool) { return path.Dir(f), true }
	keys := []UnitKey{{Unit: "tools/forge/x", Hash: "h"}}
	got := GoGradings(fresh, "tools/forge", "E", true, keys, owner)
	if len(got) != 1 || got[0].Lang != "go" || got[0].Engine != "E" || !got[0].Reusable || got[0].Counts.Killed != 1 ||
		len(got[0].Mutants) != 1 || got[0].Mutants[0].File != "tools/forge/x/a.go" {
		t.Fatalf("got %+v", got)
	}
	if fresh[0].File != "x/a.go" {
		t.Fatal("the run's own mutants are left as they were")
	}
	none := GoGradings(nil, ".", "E", false, keys, owner)
	if len(none) != 1 || none[0].Counts.Generated != 0 || none[0].WhyNot != WhyNotUntrusted {
		t.Fatalf("no score: %+v", none)
	}
}
