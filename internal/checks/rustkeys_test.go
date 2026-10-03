package checks

import (
	"reflect"
	"testing"
)

// rustMeta is a workspace at /src: c depends on b, b on a, a on nothing; d is
// a root crate depending on a crate outside the repository and a registry one.
const rustMeta = `{"packages":[
 {"name":"x","manifest_path":"/other/x/Cargo.toml","dependencies":[]},
 {"name":"a","manifest_path":"/src/crates/a/Cargo.toml","dependencies":[]},
 {"name":"b","manifest_path":"/src/crates/b/Cargo.toml","dependencies":[{"name":"a","path":"/src/crates/a"},{"name":"serde"}]},
 {"name":"c","manifest_path":"/src/crates/c/Cargo.toml","dependencies":[{"name":"b","path":"/src/crates/b/"}]},
 {"name":"d","manifest_path":"/src/Cargo.toml","dependencies":[{"name":"far","path":"/elsewhere/far"},{"name":"c","path":"/src/crates/c"}]}
]}`

func TestRustClosuresAreEachCrateAndItsPathDependencies(t *testing.T) {
	got, err := RustClosures([]byte(rustMeta), "/src")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"crates/a": {"crates/a"},
		"crates/b": {"crates/a", "crates/b"},
		"crates/c": {"crates/a", "crates/b", "crates/c"},
		".":        {".", "crates/a", "crates/b", "crates/c"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	cyclic := `{"packages":[{"manifest_path":"/src/p/Cargo.toml","dependencies":[{"path":"/src/q"}]},{"manifest_path":"/src/q/Cargo.toml","dependencies":[{"path":"/src/p"}]}]}`
	if got, err := RustClosures([]byte(cyclic), "/src"); err != nil || !reflect.DeepEqual(got["p"], []string{"p", "q"}) {
		t.Fatalf("a dependency cycle closes: %v %v", got, err)
	}
	if _, err := RustClosures([]byte("not json"), "/src"); err == nil {
		t.Fatal("metadata that does not parse is an error")
	}
}

func TestAWideTestScopeMakesEveryClosureTheWorkspace(t *testing.T) {
	for cfg, want := range map[string]bool{
		"exclude_re = []\n": false, "test_workspace = true\n": true, "test_package = [\"b\"]\n": true, "": false,
	} {
		if RustTestScopeWide(cfg) != want {
			t.Errorf("RustTestScopeWide(%q) = %v", cfg, !want)
		}
	}
	got := Widened(map[string][]string{"a": {"a"}, "b": {"a", "b"}, "c": {"c"}})
	all := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, map[string][]string{"a": all, "b": all, "c": all}) {
		t.Fatalf("widened = %v", got)
	}
}
