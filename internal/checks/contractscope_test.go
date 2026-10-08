package checks

import (
	"errors"
	"reflect"
	"testing"
)

const scopeManifest = `
[contracts.a]
authority = "dies/a.json"
[[contracts.a.copies]]
name = "dies/a.json"
source = { local = "schema/a.json" }
[[contracts.a.copies]]
name = "star-a/a.json"
source = { repo = "rob/star-a", path = "vendor/a.json" }
[[contracts.a.copies]]
name = "star-b/a.json"
source = { repo = "rob/star-b", path = "vendor/a.json" }

[contracts.wit]
authority = "dies/r.wit"
[[contracts.wit.copies]]
name = "core/r.wit"
source = { repo = "rob/core", path = "wit/r.wit" }
[[contracts.wit.copies]]
name = "core-rust/r.wit"
source = { repo = "foundry/core-rust", path = "wit/r.wit" }

[contracts.malformed]
authority = "dies/m.json"
[[contracts.malformed.copies]]
name = "ghost/m.json"
source = { repo = "rob/ghost" }
[[contracts.malformed.copies]]
name = "ghost2/m.json"
source = { path = "m.json" }

[contracts.disjoint]
authority = "dies/d.json"
[[contracts.disjoint.copies]]
name = "small/d.json"
source = { repo = "rob/small", path = "d/one.json" }
[[contracts.disjoint.copies]]
name = "big/d.json"
source = { repo = "rob/big", path = "d/two.json" }
[[contracts.disjoint.copies]]
name = "big/e.json"
source = { repo = "rob/big", path = "d/three.json" }

[contracts.rust-only]
authority = "dies/x.json"
[[contracts.rust-only.copies]]
name = "core-rust/x.json"
source = { repo = "foundry/core-rust", path = "schema/x.json" }
`

func treeHas(paths ...string) func(string) (bool, error) {
	set := map[string]bool{}
	for _, p := range paths {
		set[p] = true
	}
	return func(p string) (bool, error) { return set[p], nil }
}

func TestContractTreeNamesReadsTheTreeOffTheCopiesItHolds(t *testing.T) {
	for _, tc := range []struct {
		name string
		has  []string
		want []string
	}{
		{"a star holding its copy", []string{"vendor/a.json"}, []string{"star-a", "star-b"}},
		{"a larger set that is NOT a superset does not displace a smaller one", []string{"d/one.json", "d/two.json", "d/three.json"}, []string{"big", "small"}},
		{"a path-less or repo-less copy names no tree", []string{"", "m.json"}, nil},
		{"no copy held", []string{"README.md"}, nil},
		{"a tree missing one of its repo's copies is not that repo", []string{"schema/x.json"}, nil},
		{"the larger of two nested sets wins", []string{"wit/r.wit", "schema/x.json"}, []string{"core-rust"}},
		{"the smaller set alone", []string{"wit/r.wit"}, []string{"core", "core-rust"}[:1]},
	} {
		got, err := ContractTreeNames(scopeManifest, treeHas(tc.has...))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestContractCopyFilesAreTheCheckerItsImportAndTheManifest(t *testing.T) {
	want := []string{"tools/check_contracts.py", "tools/schema_stamp.py", "contracts/contracts.toml"}
	if got := ContractCopyFiles(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestContractTreeNamesPropagatesALookupFailure(t *testing.T) {
	boom := errors.New("boom")
	_, err := ContractTreeNames(scopeManifest, func(string) (bool, error) { return false, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("a tree that could not be looked at is not a tree with no copies: %v", err)
	}
	if _, err := ContractTreeNames("not = [toml", treeHas()); err == nil {
		t.Fatal("an unparseable manifest names no tree")
	}
}
