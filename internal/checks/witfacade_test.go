package checks

import (
	"encoding/json"
	"strings"
	"testing"
)

// metaOf renders a `cargo metadata --no-deps` answer for a workspace at root
// whose members are dir -> feature names ("" dir is the root itself).
func metaOf(root string, members map[string][]string) []byte {
	type pkg struct {
		Name         string              `json:"name"`
		ID           string              `json:"id"`
		ManifestPath string              `json:"manifest_path"`
		Features     map[string][]string `json:"features"`
	}
	var packages []pkg
	var ids []string
	for dir, feats := range members {
		manifest := root + "/Cargo.toml"
		if dir != "" {
			manifest = root + "/" + dir + "/Cargo.toml"
		}
		f := map[string][]string{}
		for _, n := range feats {
			f[n] = []string{}
		}
		id := "path+file://" + manifest
		packages = append(packages, pkg{Name: "m-" + strings.ReplaceAll(dir, "/", "-"), ID: id, ManifestPath: manifest, Features: f})
		ids = append(ids, id)
	}
	out, _ := json.Marshal(map[string]any{"packages": packages, "workspace_members": ids, "workspace_root": root})
	return out
}

func TestIsVirtualWorkspace(t *testing.T) {
	for _, tc := range []struct {
		name, toml string
		want       bool
	}{
		{"virtual", "[workspace]\nmembers = [\"rust\"]\n", true},
		{"virtual with comment header", "[workspace] # root\nmembers = [\"rust\"]\n", true},
		{"package and workspace (today's stellar-core-rust)", "[package]\nname = \"stellar-core\"\n\n[workspace]\nmembers = [\"crates/*\"]\n", false},
		{"single package", "[package]\nname = \"x\"\n", false},
		{"workspace.package alone is still virtual", "[workspace]\nmembers = []\n\n[workspace.package]\nedition = \"2024\"\n", true},
		{"workspace.dependencies alone is a virtual workspace", "[workspace.dependencies]\nserde = \"1\"\n", true},
		{"empty", "", false},
	} {
		if got := IsVirtualWorkspace(tc.toml); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestWitFacadeDirFindsTheMemberDeclaringTheGuestFeatures(t *testing.T) {
	root := "/work/src"
	facade, found, err := WitFacadeDir(metaOf(root, map[string][]string{
		"rust":                 {"default", "wit-guest", "wit-guest-identity", "wit-guest-reader"},
		"rust/crates/receipt":  {"default"},
		"rust/crates/identity": {"default", "wit-guest-identity"},
	}))
	if err != nil || !found || facade != "rust" {
		t.Fatalf("got %q %v %v", facade, found, err)
	}
}

func TestWitFacadeDirPrefersTheMemberThatForwardsEveryWorld(t *testing.T) {
	// per-Class crates each declare their own world; the facade declares them all.
	root := "/w"
	facade, found, err := WitFacadeDir(metaOf(root, map[string][]string{
		"classes/netstring/rust": {"wit-guest-netstring"},
		"rust":                   {"wit-guest-identity", "wit-guest-netstring"},
		"rust/crates/identity":   {"wit-guest-identity"},
	}))
	if err != nil || !found || facade != "rust" {
		t.Fatalf("got %q %v %v", facade, found, err)
	}
}

func TestWitFacadeDirTiesBreakOnTheShortestThenSortedDir(t *testing.T) {
	root := "/w"
	facade, _, _ := WitFacadeDir(metaOf(root, map[string][]string{
		"b/x": {"wit-guest-a"},
		"a":   {"wit-guest-b"},
		"c":   {"wit-guest-c"},
	}))
	if facade != "a" {
		t.Errorf("got %q", facade)
	}
}

func TestWitFacadeDirNotFoundIsNotAnError(t *testing.T) {
	root := "/w"
	facade, found, err := WitFacadeDir(metaOf(root, map[string][]string{
		"rust": {"default", "wit"}, "rust/crates/a": {"default"},
	}))
	if err != nil || found || facade != "" {
		t.Fatalf("got %q %v %v", facade, found, err)
	}
}

func TestWitFacadeDirTheBareAliasAloneIsAWorld(t *testing.T) {
	root := "/w"
	facade, found, _ := WitFacadeDir(metaOf(root, map[string][]string{"rust": {"wit-guest"}}))
	if !found || facade != "rust" {
		t.Fatalf("got %q %v", facade, found)
	}
}

func TestWitFacadeDirAMemberAtTheRootIsTheEmptyDir(t *testing.T) {
	root := "/w"
	facade, found, _ := WitFacadeDir(metaOf(root, map[string][]string{"": {"wit-guest-x"}}))
	if !found || facade != "" {
		t.Fatalf("got %q %v", facade, found)
	}
}

func TestWitFacadeDirIgnoresPackagesThatAreNotWorkspaceMembers(t *testing.T) {
	root := "/w"
	raw := metaOf(root, map[string][]string{"rust": {"wit-guest-x"}})
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m["workspace_members"] = []string{}
	raw, _ = json.Marshal(m)
	if _, found, _ := WitFacadeDir(raw); found {
		t.Error("a package outside workspace_members was taken as the facade")
	}
}

func TestWitFacadeDirRefusesUnparseableMetadata(t *testing.T) {
	if _, _, err := WitFacadeDir([]byte("not json")); err == nil {
		t.Error("expected a parse error")
	}
}

func TestWitFacadeDirRefusesAManifestOutsideTheWorkspaceRoot(t *testing.T) {
	raw := metaOf("/elsewhere", map[string][]string{"rust": {"wit-guest-x"}})
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m["workspace_root"] = "/w"
	raw, _ = json.Marshal(m)
	if _, found, err := WitFacadeDir(raw); err == nil || found {
		t.Errorf("found=%v err=%v", found, err)
	}
}

func TestWitGuestWorldsOfNames(t *testing.T) {
	got := WitGuestWorldsOf([]string{"default", "wit-guest", "wit-guest-b", "wit-guest-a", "wit-guest-"})
	if strings.Join(got, ",") != "wit-guest-a,wit-guest-b" {
		t.Errorf("%v", got)
	}
	if got := WitGuestWorldsOf([]string{"wit-guest"}); len(got) != 1 || got[0] != "wit-guest" {
		t.Errorf("%v", got)
	}
	if got := WitGuestWorldsOf(nil); got != nil {
		t.Errorf("%v", got)
	}
}
