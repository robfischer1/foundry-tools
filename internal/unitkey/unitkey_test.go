package unitkey

import (
	"maps"
	"slices"
	"testing"
)

// goTree is a small Go repository: a root main package, two packages where
// internal/a imports internal/b, a testdata fixture, a non-package directory a
// test reads, a hidden directory, docs and the module files.
func goTree() []Entry {
	return []Entry{
		{"main.go", "m1"},
		{"go.mod", "gm"},
		{"go.sum", "gs"},
		{"justfile", "j1"},
		{"README.md", "r1"},
		{"docs/guide.txt", "d1"},
		{"LICENSE", "l1"},
		{"internal/a/a.go", "a1"},
		{"internal/a/a_test.go", "at"},
		{"internal/a/testdata/golden.json", "ag"},
		{"internal/a/testdata/deep/more.txt", "am"},
		{"internal/a/testdata/fixture/fixture.go", "af"},
		{"internal/b/b.go", "b1"},
		{"internal/c/c.go", "c1"},
		{"migrations/001.sql", "s1"},
		{".hidden/tool.go", "h1"},
		{"_scratch/x.go", "x1"},
		{"vendor/dep/dep.go", "v1"},
	}
}

// edit answers the tree with one file's blob changed (or added).
func edit(tree []Entry, p, blob string) []Entry {
	out := slices.Clone(tree)
	for i := range out {
		if out[i].Path == p {
			out[i].Blob = blob
			return out
		}
	}
	return append(out, Entry{p, blob})
}

func TestTheGoUnitsAreTheDirectoriesTheGoToolBuilds(t *testing.T) {
	got := slices.Sorted(maps.Keys(Units(Go, goTree())))
	want := []string{".", "internal/a", "internal/b", "internal/c"}
	if !slices.Equal(got, want) {
		t.Fatalf("units = %v, want %v", got, want)
	}
}

func TestTheRustUnitsAreTheCratesWithSources(t *testing.T) {
	tree := []Entry{
		{"Cargo.toml", "w"}, {"Cargo.lock", "l"},
		{"crates/a/Cargo.toml", "am"}, {"crates/a/src/lib.rs", "a"},
		{"crates/b/Cargo.toml", "bm"}, {"crates/b/src/deep/mod.rs", "b"},
		{"crates/notacrate/src/x.rs", "n"},
		{"tools/Cargo.toml", "tm"}, {"tools/bin/x.rs", "t"},
	}
	got := slices.Sorted(maps.Keys(Units(Rust, tree)))
	if want := []string{"crates/a", "crates/b"}; !slices.Equal(got, want) {
		t.Fatalf("units = %v, want %v (a virtual root, a src without a manifest and a manifest without a src are not units)", got, want)
	}
	single := []Entry{{"Cargo.toml", "m"}, {"src/main.rs", "s"}}
	if got := Units(Rust, single); !got["."] || len(got) != 1 {
		t.Fatalf("a single crate at the root: %v", got)
	}
	if got := Units(Lang("cobol"), goTree()); len(got) != 0 {
		t.Fatalf("an unknown language has no units: %v", got)
	}
}

func TestOwner(t *testing.T) {
	goUnits := Units(Go, goTree())
	rustUnits := map[string]bool{".": true, "crates/a": true}
	for _, c := range []struct {
		lang  Lang
		path  string
		units map[string]bool
		want  string
		owned bool
	}{
		{Go, "internal/a/a.go", goUnits, "internal/a", true},
		{Go, "internal/a/testdata/golden.json", goUnits, "internal/a", true},
		{Go, "internal/a/testdata/deep/more.txt", goUnits, "internal/a", true},
		{Go, "testdata/root.txt", goUnits, ".", true},
		{Go, "main.go", goUnits, ".", true},
		{Go, "justfile", goUnits, ".", true},
		{Go, "migrations/001.sql", goUnits, "", false},
		{Go, "go.mod", goUnits, "", false},
		{Go, "go.sum", goUnits, "", false},
		{Go, "tools/go.work", goUnits, "", false},
		{Go, "go.work", goUnits, "", false},
		{Rust, "crates/a/deep/er/x.rs", rustUnits, "crates/a", true},
		{Go, "go.work.sum", goUnits, "", false},
		{Go, "nopkg/testdata/x", goUnits, "", false},
		{Rust, "crates/a/src/lib.rs", rustUnits, "crates/a", true},
		{Rust, "crates/a/fixtures/x.json", rustUnits, "crates/a", true},
		{Rust, "src/main.rs", rustUnits, ".", true},
		{Rust, "tests/it.rs", rustUnits, ".", true},
		{Rust, "benches/b.rs", rustUnits, ".", true},
		{Rust, "examples/e.rs", rustUnits, ".", true},
		{Rust, "build.rs", rustUnits, ".", true},
		{Rust, "justfile", rustUnits, "", false},
		{Rust, "scripts/run.sh", rustUnits, "", false},
		{Rust, "src", rustUnits, "", false},
		{Rust, "Cargo.toml", rustUnits, "", false},
		{Rust, "crates/a/Cargo.lock", rustUnits, "", false},
		{Rust, "mutants.toml", rustUnits, "", false},
		{Rust, ".cargo/mutants.toml", rustUnits, "", false},
		{Rust, "rust-toolchain.toml", rustUnits, "", false},
		{Rust, "x/y.rs", map[string]bool{"crates/a": true}, "", false},
		{Lang("cobol"), "a/b", goUnits, "", false},
		{Go, "Cargo.lock", map[string]bool{".": true}, ".", true},
	} {
		got, owned := Owner(c.lang, c.path, c.units)
		if got != c.want || owned != c.owned {
			t.Errorf("Owner(%s, %s) = %q, %v; want %q, %v", c.lang, c.path, got, owned, c.want, c.owned)
		}
	}
}

func TestIsDoc(t *testing.T) {
	for p, want := range map[string]bool{
		"README.md": true, "internal/a/NOTES.md": true, "docs/x.png": true, "LICENSE": true,
		"sub/LICENSE-MIT": true, "main.go": false, "subdocs/x.txt": false, "md": false,
	} {
		if IsDoc(p) != want {
			t.Errorf("IsDoc(%s) = %v", p, !want)
		}
	}
}

// TestTheHashMovesExactlyWithItsClosure is the reuse rule's whole claim, for Go.
func TestTheHashMovesExactlyWithItsClosure(t *testing.T) {
	tree := goTree()
	closure := []string{"internal/a", "internal/b"}
	base := Hash(Go, tree, closure)
	if again := Hash(Go, slices.Clone(tree), []string{"internal/b", "./internal/a"}); again != base {
		t.Fatal("the same tree and closure, in another order, must hash the same")
	}
	reversed := slices.Clone(tree)
	slices.Reverse(reversed)
	if Hash(Go, reversed, closure) != base {
		t.Fatal("the entries' order must not matter")
	}
	for _, c := range []struct {
		why  string
		path string
		same bool
	}{
		{"a sibling file in the unit", "internal/a/a.go", false},
		{"a test of the unit", "internal/a/a_test.go", false},
		{"a new file in the unit", "internal/a/new.go", false},
		{"the unit's testdata", "internal/a/testdata/golden.json", false},
		{"a package the unit imports", "internal/b/b.go", false},
		{"an orphan the tests may read", "migrations/001.sql", false},
		{"go.mod", "go.mod", false},
		{"go.sum", "go.sum", false},
		{"a vendored dependency", "vendor/dep/dep.go", false},
		{"a package outside the closure", "internal/c/c.go", true},
		{"the root package, outside the closure", "main.go", true},
		{"the justfile, owned by the root package", "justfile", true},
		{"a README", "README.md", true},
		{"a doc in the unit", "internal/a/DESIGN.md", true},
		{"docs/", "docs/guide.txt", true},
		{"the LICENSE", "LICENSE", true},
	} {
		if got := Hash(Go, edit(tree, c.path, "changed"), closure) == base; got != c.same {
			t.Errorf("%s (%s): same hash = %v, want %v", c.why, c.path, got, c.same)
		}
	}
	if Hash(Rust, tree, closure) == base {
		t.Fatal("the language is part of the hash")
	}
}

func TestARustCrateHashesItsPathDependencies(t *testing.T) {
	tree := []Entry{
		{"Cargo.toml", "w"}, {"Cargo.lock", "l"}, {".cargo/config.toml", "c"}, {"justfile", "j"},
		{"a/Cargo.toml", "am"}, {"a/src/lib.rs", "a"},
		{"b/Cargo.toml", "bm"}, {"b/src/lib.rs", "b"}, {"b/tests/it.rs", "bt"},
	}
	closure := []string{"b", "a"}
	base := Hash(Rust, tree, closure)
	for p, same := range map[string]bool{
		"a/src/lib.rs": false, "b/tests/it.rs": false, "Cargo.lock": false, ".cargo/config.toml": false,
		"justfile": false, "b/README.md": true,
	} {
		if (Hash(Rust, edit(tree, p, "x"), closure) == base) != same {
			t.Errorf("%s: same = %v", p, !same)
		}
	}
	if Hash(Rust, edit(tree, "a/src/lib.rs", "x"), []string{"b"}) != Hash(Rust, tree, []string{"b"}) {
		t.Fatal("a crate outside the closure must not move the hash")
	}
}

func TestTheEngineIsEveryInputAndOnlyThem(t *testing.T) {
	in := map[string]string{"gomutants": "v0.6.1", "image": "golang@sha256:1", "workers": "4"}
	e := Engine(in)
	if Engine(maps.Clone(in)) != e {
		t.Fatal("the same inputs must answer the same engine")
	}
	for k := range in {
		moved := maps.Clone(in)
		moved[k] += "x"
		if Engine(moved) == e {
			t.Errorf("moving %s did not move the engine", k)
		}
	}
	more := maps.Clone(in)
	more["new"] = ""
	if Engine(more) == e {
		t.Fatal("a new input moves the engine")
	}
}
