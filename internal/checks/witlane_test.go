package checks

import (
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
	if got := WitGuestArtifact("[package]\nname = \"broken\n"); got != "" {
		t.Errorf("an unterminated name is not read: got %q", got)
	}
}

func TestWitPinsAgree(t *testing.T) {
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
