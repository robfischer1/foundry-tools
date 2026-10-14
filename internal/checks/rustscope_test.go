package checks

import (
	"reflect"
	"testing"
)

// A workspace shaped like stellar-core-rust after the split: the facade at the
// root, the cores under crates/, the generator under tools/.
const scopeMeta = `{
 "workspace_members": ["root","circ","read","conf","gen"],
 "packages": [
  {"name":"stellar-core","id":"root","manifest_path":"/src/Cargo.toml"},
  {"name":"stellar-core-circuit","id":"circ","manifest_path":"/src/crates/circuit/Cargo.toml"},
  {"name":"stellar-core-reader","id":"read","manifest_path":"/src/crates/reader/Cargo.toml"},
  {"name":"stellar-core-conformance","id":"conf","manifest_path":"/src/crates/conformance/Cargo.toml"},
  {"name":"kafka-topics-gen","id":"gen","manifest_path":"/src/tools/kafka-topics-gen/Cargo.toml"},
  {"name":"serde","id":"dep","manifest_path":"/reg/serde/Cargo.toml"}
 ]}`

func TestRustScopeNamesTheMembersTheFilesBelongTo(t *testing.T) {
	got, whole, err := RustScope([]byte(scopeMeta), "/src", []string{
		"crates/circuit/src/lib.rs", "crates/circuit/src/quorum.rs", "crates/conformance/tests/circuit_conformance.rs", "README.md",
	})
	if err != nil || whole {
		t.Fatalf("whole=%v err=%v", whole, err)
	}
	if want := []string{"stellar-core-circuit", "stellar-core-conformance"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRustScopeGradesWholeWhenARootFileMoves(t *testing.T) {
	for _, f := range []string{"Cargo.toml", "Cargo.lock", "src/lib.rs", "tests/tapes/classes.json", ".cargo/mutants.toml", "wit/aiws-result.wit", "tools/compose/compose.sh"} {
		_, whole, err := RustScope([]byte(scopeMeta), "/src", []string{"crates/circuit/src/lib.rs", f})
		if err != nil || !whole {
			t.Errorf("%s: whole=%v err=%v, want whole", f, whole, err)
		}
	}
}

func TestRustScopeIgnoresWhatNoCargoCommandCanFeel(t *testing.T) {
	got, whole, err := RustScope([]byte(scopeMeta), "/src", []string{"README.md", "CLAUDE-INIT.md", "justfile", "hooks/pre-commit", "crates/circuit/README.md"})
	if err != nil || whole || len(got) != 1 || got[0] != "stellar-core-circuit" {
		t.Errorf("got %v whole=%v err=%v: only the crate's own README belongs to a member", got, whole, err)
	}
	got, whole, _ = RustScope([]byte(scopeMeta), "/src", []string{"README.md"})
	if whole || len(got) != 0 {
		t.Errorf("a prose-only pull has nothing to grade, got %v whole=%v", got, whole)
	}
}

func TestRustScopeIsTheWholeWorkspaceForASinglePackageAndForBadMetadata(t *testing.T) {
	one := `{"workspace_members":["a"],"packages":[{"name":"a","id":"a","manifest_path":"/src/Cargo.toml"}]}`
	if _, whole, err := RustScope([]byte(one), "/src", []string{"src/lib.rs"}); err != nil || !whole {
		t.Errorf("a one-package workspace is graded whole: whole=%v err=%v", whole, err)
	}
	if _, whole, err := RustScope([]byte("not json"), "/src", nil); err == nil || !whole {
		t.Errorf("metadata that does not parse is an error and whole: whole=%v err=%v", whole, err)
	}
}

func TestRustScopeAVirtualWorkspaceRootHoldsNoCrate(t *testing.T) {
	v := `{"workspace_members":["a","b"],"packages":[
	 {"name":"a","id":"a","manifest_path":"/src/crates/a/Cargo.toml"},
	 {"name":"b","id":"b","manifest_path":"/src/crates/b/Cargo.toml"}]}`
	if _, whole, _ := RustScope([]byte(v), "/src", []string{"crates/a/src/lib.rs", "Cargo.lock"}); !whole {
		t.Error("the lock belongs to no member of a virtual workspace: whole")
	}
	got, whole, _ := RustScope([]byte(v), "/src", []string{"crates/a/src/lib.rs", "README.md"})
	if whole || !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("got %v whole=%v", got, whole)
	}
}

func TestRustScopeArgsKeepsEachLineAsAnArgumentSet(t *testing.T) {
	got := RustScopeArgs("-p a -p b\n\n-p c --test t1 --test t2\n")
	want := [][]string{{"-p", "a", "-p", "b"}, {"-p", "c", "--test", "t1", "--test", "t2"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
	if len(RustScopeArgs("\n \n")) != 0 {
		t.Error("blank lines name no set")
	}
}

func TestRustScopeEveryInertSpellingAndTheFileOutsideTheTree(t *testing.T) {
	inert := []string{"LICENSE", "cosign.pub", "cliff.toml", "CHANGELOG.md", ".gitignore", ".editorconfig", "justfile", "stages.just", "dev.just", "hooks/pre-commit", ".claude/settings.json", ""}
	got, whole, err := RustScope([]byte(scopeMeta), "/src", append([]string{"crates/circuit/src/lib.rs"}, inert...))
	if err != nil || whole || !reflect.DeepEqual(got, []string{"stellar-core-circuit"}) {
		t.Errorf("furniture changes no crate: got %v whole=%v err=%v", got, whole, err)
	}
	// A path that climbs out of the workspace belongs to nobody, and nobody's
	// change is not one the scope can account for.
	if _, whole, _ := RustScope([]byte(scopeMeta), "/src", []string{"../elsewhere/x.rs"}); !whole {
		t.Error("a file outside every member is graded whole")
	}
}
