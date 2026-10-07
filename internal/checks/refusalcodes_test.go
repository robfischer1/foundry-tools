package checks

import (
	"reflect"
	"testing"
)

func TestFirstRegistryUseIsTheFirstUseThatNamesARepo(t *testing.T) {
	reg := `[[uses]]
owner = "a"
source = { local = "x" }
extract = { kind = "k" }

[[uses]]
owner = "b"
source = { repo = "rob/stellar-core", path = "wit/x.wit" }
extract = { kind = "k" }
`
	repo, path, ok := FirstRegistryUse(reg)
	if !ok || repo != "rob/stellar-core" || path != "wit/x.wit" {
		t.Fatalf("got %q %q %v", repo, path, ok)
	}
	half := "[[uses]]\nsource = { repo = \"rob/only-repo\" }\n\n[[uses]]\nsource = { path = \"only/path\" }\n\n[[uses]]\nsource = { repo = \"rob/ok\", path = \"p\" }\n"
	if repo, path, ok := FirstRegistryUse(half); !ok || repo != "rob/ok" || path != "p" {
		t.Fatalf("a use needs BOTH a repo and a path: got %q %q %v", repo, path, ok)
	}
	broken := "[[uses]]\nsource = { repo = \"rob/a\", path = \"p\" }\n\n[[uses\n"
	if _, _, ok := FirstRegistryUse(broken); ok {
		t.Fatal("an unparseable registry has no first use, even after a valid one")
	}
	if _, _, ok := FirstRegistryUse("not = [toml"); ok {
		t.Fatal("an unparseable registry has no first use; the checker is the judge of it")
	}
	if _, _, ok := FirstRegistryUse("[codes.x]\nowners=[]\n"); ok {
		t.Fatal("a registry with no remote use has nothing to probe")
	}
}

// The checker's inputs are the checker, what it imports, and the registry:
// dropping one is an import error in a lane nobody watches.
func TestRefusalCheckerFilesCarryTheCheckersImports(t *testing.T) {
	want := []string{"tools/check_refusal_codes.py", "tools/check_contracts.py", "tools/schema_stamp.py", "contracts/refusal-codes.toml"}
	if got := RefusalCheckerFiles(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if got := RefusalTreeFlag("stellar-core"); got != "stellar-core=." {
		t.Fatal(got)
	}
	m := RefusalCoreMarkers()
	m[0] = "mutated"
	if RefusalCoreMarkers()[0] == "mutated" {
		t.Fatal("the marker pair must not be editable by a caller")
	}
}
