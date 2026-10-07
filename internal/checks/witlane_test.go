package checks

import (
	"slices"
	"strings"
	"testing"
)

func TestHasJustValidate(t *testing.T) {
	for _, c := range []struct {
		name, justfile string
		want           bool
	}{
		{"a recipe", "default:\n    @just --list\n\nvalidate: wit\n\nwit:\n    true\n", true},
		{"a recipe with no deps", "validate:\n    true\n", true},
		{"a recipe with a parameter", "validate target='all':\n    true\n", true},
		{"a doc attribute before it", "[doc('x')]\nvalidate:\n    true\n", true},
		{"no recipe", "default:\n    @just --list\n", false},
		{"a longer name", "validate-all:\n    true\n", false},
		{"a comment", "# validate: the one that parses\ndefault:\n    true\n", false},
		{"an indented body line", "default:\n    validate: not a recipe\n", false},
		{"an assignment", "validate := 'x'\n", false},
		{"empty", "", false},
	} {
		if got := HasJustValidate(c.justfile); got != c.want {
			t.Errorf("%s: HasJustValidate = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestHasWitFiles(t *testing.T) {
	for _, c := range []struct {
		name  string
		files []string
		want  bool
	}{
		{"a wit file", []string{"README.md", "wit/aiws-identity.wit"}, true},
		{"nested under wit", []string{"wit/deps/x.wit"}, true},
		{"none", []string{"README.md", "justfile"}, false},
		{"wit outside wit/", []string{"docs/x.wit"}, false},
		{"a non-wit file under wit/", []string{"wit/README.md"}, false},
		{"empty", nil, false},
	} {
		if got := HasWitFiles(c.files); got != c.want {
			t.Errorf("%s: HasWitFiles = %v, want %v", c.name, got, c.want)
		}
	}
}

const witManifest = `[package]
name = "stellar-core"
version = "0.20.0"

[lib]
crate-type = ["rlib", "cdylib"]

[dependencies]
wit-bindgen = { version = "=0.62.0", optional = true }

[features]
default = ["telemetry"]
telemetry = []
wit-guest = ["dep:wit-bindgen"]
`

func TestHasCargoFeature(t *testing.T) {
	if !HasCargoFeature(witManifest, "wit-guest") {
		t.Error("the [features] table declares wit-guest")
	}
	if HasCargoFeature(witManifest, "wit") {
		t.Error("a prefix of a feature name is not the feature")
	}
	// A dependency NAMED like the feature is not the feature: only [features]
	// declares one.
	if HasCargoFeature("[dependencies]\nwit-guest = \"1\"\n", "wit-guest") {
		t.Error("a dependency is not a feature")
	}
	// The table ends at the next header.
	if HasCargoFeature("[features]\ndefault = []\n\n[dependencies]\nwit-guest = \"1\"\n", "wit-guest") {
		t.Error("a key after the [features] table is not in it")
	}
	if !HasCargoFeature("  [features]\r\n  wit-guest = []\r\n", "wit-guest") {
		t.Error("indentation and CRLF do not hide a feature")
	}
	if HasCargoFeature("", "wit-guest") {
		t.Error("an empty manifest declares nothing")
	}
}

func TestWitGuestArtifact(t *testing.T) {
	if got := WitGuestArtifact(witManifest); got != "stellar_core.wasm" {
		t.Errorf("package name, dashes to underscores: got %q", got)
	}
	withLib := strings.Replace(witManifest, "[lib]\n", "[lib]\nname = \"core-guest\"\n", 1)
	if got := WitGuestArtifact(withLib); got != "core_guest.wasm" {
		t.Errorf("[lib] name wins over the package name: got %q", got)
	}
	if got := WitGuestArtifact("[workspace]\nmembers = []\n"); got != "" {
		t.Errorf("no name, no artifact: got %q", got)
	}
	if got := WitGuestArtifact("[package]\nname = unquoted\n"); got != "" {
		t.Errorf("an unquoted name is not read: got %q", got)
	}
	if got := WitGuestArtifact("[package]\nname = x\"y\"\n"); got != "" {
		t.Errorf("a value that does not open with a quote is not read: got %q", got)
	}
	if got := WitGuestArtifact("[package]\nname = \"broken\n"); got != "" {
		t.Errorf("an unterminated name is not read: got %q", got)
	}
}

func TestWitPinsAgree(t *testing.T) {
	if want := "https://github.com/bytecodealliance/wac/releases/download/v" + WacVersion + "/wac-cli-x86_64-unknown-linux-musl"; WacURL != want {
		t.Errorf("WacURL %q does not follow WacVersion: want %q", WacURL, want)
	}
	if want := "https://github.com/bytecodealliance/wasm-tools/releases/download/v" + WasmToolsVersion + "/wasm-tools-" + WasmToolsVersion + "-x86_64-linux.tar.gz"; WasmToolsURL != want {
		t.Errorf("WasmToolsURL %q does not follow WasmToolsVersion: want %q", WasmToolsURL, want)
	}
	if want := "wasm-tools-" + WasmToolsVersion + "-x86_64-linux/wasm-tools"; WasmToolsMember != want {
		t.Errorf("WasmToolsMember %q, want %q", WasmToolsMember, want)
	}
	if want := "https://github.com/casey/just/releases/download/" + JustVersion + "/just-" + JustVersion + "-x86_64-unknown-linux-musl.tar.gz"; JustURL != want {
		t.Errorf("JustURL %q does not follow JustVersion: want %q", JustURL, want)
	}
}

func TestWitGuestWorlds(t *testing.T) {
	for _, c := range []struct {
		name, manifest string
		want           []string
	}{
		{"two worlds and the alias", "[features]\nwit-guest = [\"wit-guest-identity\"]\nwit-guest-reader = []\nwit-guest-identity = []\ndefault = []\n", []string{"wit-guest-identity", "wit-guest-reader"}},
		{"worlds without the alias", "[features]\nwit-guest-reader = []\n", []string{"wit-guest-reader"}},
		{"the alias alone is the one world", "[features]\nwit-guest = []\n", []string{"wit-guest"}},
		{"neither", "[features]\ndefault = []\nwit = []\n", nil},
		{"a bare prefix is not a world", "[features]\nwit-guest- = []\n", nil},
		{"a dependency is not a feature", "[dependencies]\nwit-guest-x = \"1\"\n", nil},
		{"a long name without the prefix is not a world", "[features]\nunrelated-feature-name-longer-than-the-prefix = []\n", nil},
		{"empty", "", nil},
	} {
		got := WitGuestWorlds(c.manifest)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: WitGuestWorlds = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestWitComposeTapes(t *testing.T) {
	manifest := "[features]\nwit-guest = [\"wit-guest-identity\"]\nwit-guest-reader = []\nwit-guest-identity = []\nwit-guest-door = []\n"
	files := []string{"tests/tapes/identity.json", "tests/tapes/reader.json", "tests/tapes/promote.json", "tests/tapes/wit-guest.json"}
	got := WitComposeTapes(manifest, files)
	if want := []string{"tests/tapes/identity.json", "tests/tapes/reader.json"}; !slices.Equal(got, want) {
		t.Errorf("tapes are the discovered worlds' own, in world order, and only those the tree has: got %v want %v", got, want)
	}
	if got := WitComposeTapes("[features]\nwit-guest = []\n", files); len(got) != 0 {
		t.Errorf("the lone alias names no tape: got %v", got)
	}
}
