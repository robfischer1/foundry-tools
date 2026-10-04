package checks

import (
	"regexp"
	"strings"
)

// THE WIT ATOMS' JUDGEMENTS, as pure functions over strings — whether a tree
// declares what each atom reads, and what the guest artifact is called. The
// atoms themselves (atoms_wit.go) only fetch, mount and point.
//
// WHY THESE TWO EXIST (rob/stellar-core#13761). An interface repo that carries
// only WIT and a tape has no language lane, so nothing graded its WIT: a file
// that does not parse landed on main and was found by whichever star replayed
// it next. And the Rust core exports that WIT as a wasm32 guest behind the
// `wit-guest` feature, which no gate built — the default-features test run
// never compiles the module, so a guest that stopped building, or stopped being
// a component, read green.

const (
	// WitGuestFeature is the cargo feature that builds the WIT export. A crate
	// that declares it has a wasm guest to build; one that does not has nothing
	// for rust:wit-guest to say.
	WitGuestFeature = "wit-guest"
	// WitGuestTarget is the target the guest is built for. It is the core-module
	// target: wit-bindgen embeds the component type in the module and
	// `wasm-tools component new` lifts it.
	WitGuestTarget = "wasm32-unknown-unknown"
	// WitGuestTargetDir is where the guest build writes, OUTSIDE the lane's
	// shared cargo-target volume: a wasm32 build in the volume the native
	// atoms use would only add artifacts to it, and the path is a fact the
	// atom needs to name the artifact.
	WitGuestTargetDir = "/tmp/wit-guest-target"
)

// WasmToolsVersion / WasmToolsURL fetch the Bytecode Alliance's wasm-tools
// from GitHub's release asset by its public name — the same shape as
// hadolint, with the same refusal: a component that was never validated is not
// a component that passed. The asset is a tarball with one directory in it
// (WasmToolsMember names the binary), so the atom extracts that member and
// nothing else.
//
// THE URLS ARE LITERALS, NOT JOINS, for the reason OpengrepURL is: a `+` in a
// const block is a declaration no coverage profile can mark, and the mutation
// lane reads it as NOT COVERED forever. TestWitPinsAgree holds the version and
// the URL in step instead.
//
// THE PIN IS THE ONE `just validate` WAS WRITTEN AGAINST, 1.261.0, measured
// 2026-10-03 against stellar-core's wit/aiws-identity.wit and the guest
// stellar-core-rust builds from it: `component wit` resolves the package, and
// `component new` + `validate` accept the guest.
const (
	// renovate: datasource=github-releases depName=bytecodealliance/wasm-tools extractVersion=^v(?<version>.*)$
	WasmToolsVersion = "1.261.0"
	WasmToolsURL     = "https://github.com/bytecodealliance/wasm-tools/releases/download/v1.261.0/wasm-tools-1.261.0-x86_64-linux.tar.gz"
	// WasmToolsMember is the binary inside that tarball.
	WasmToolsMember = "wasm-tools-1.261.0-x86_64-linux/wasm-tools"

	// JustVersion / JustURL fetch `just`, the runner the repository's own
	// validate recipe is written for. The musl build is static: it runs in the
	// fleet lane image and the rust one alike.
	// renovate: datasource=github-releases depName=casey/just
	JustVersion = "1.58.0"
	JustURL     = "https://github.com/casey/just/releases/download/1.58.0/just-1.58.0-x86_64-unknown-linux-musl.tar.gz"
	// JustMember is the binary inside that tarball (it sits at the root).
	JustMember = "just"
)

var (
	justValidateRecipe = regexp.MustCompile(`(?m)^validate(?:[ \t]+\S+)*[ \t]*:(?:[^=]|$)`)
	sectionHeader      = regexp.MustCompile(`^\[([^\]]+)\]\s*(?:#.*)?$`)
	keyLine            = regexp.MustCompile(`^([A-Za-z0-9_-]+)\s*=\s*(.*)$`)
)

// HasJustValidate reports whether a justfile defines a recipe named validate:
// the line starts at column 0 with the name, optional parameters, and a colon.
// A recipe line, not a mention — a comment or an indented body line that says
// "validate:" is neither.
func HasJustValidate(justfile string) bool {
	return justValidateRecipe.MatchString(justfile)
}

// HasWitFiles reports whether the population carries a WIT file under wit/.
func HasWitFiles(files []string) bool {
	for _, f := range files {
		if strings.HasPrefix(f, "wit/") && strings.HasSuffix(f, ".wit") {
			return true
		}
	}
	return false
}

// tomlValue is the string a `key = "value"` line holds, "" for anything else.
func tomlValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != '"' {
		return ""
	}
	end := strings.IndexByte(raw[1:], '"')
	if end < 0 {
		return ""
	}
	return raw[1 : 1+end]
}

// cargoSection answers the keys of one table of a Cargo.toml, as written.
// Line-oriented on purpose: the two questions asked of the manifest are a
// feature name and a crate name, and a TOML parser is a dependency for less
// than the twenty lines this is.
func cargoSection(cargoToml, table string) map[string]string {
	keys := map[string]string{}
	in := false
	for _, line := range strings.Split(cargoToml, "\n") {
		line = strings.TrimSpace(line)
		if m := sectionHeader.FindStringSubmatch(line); m != nil {
			in = m[1] == table
			continue
		}
		if !in {
			continue
		}
		if m := keyLine.FindStringSubmatch(line); m != nil {
			keys[m[1]] = m[2]
		}
	}
	return keys
}

// HasCargoFeature reports whether the manifest's [features] table declares the
// named feature.
func HasCargoFeature(cargoToml, feature string) bool {
	_, ok := cargoSection(cargoToml, "features")[feature]
	return ok
}

// WitGuestArtifact answers the file name cargo writes for the crate's cdylib
// on wasm32: the [lib] name when the manifest sets one, otherwise the package
// name, with every `-` an `_` — cargo's own rule. "" when the manifest names
// neither.
func WitGuestArtifact(cargoToml string) string {
	name := tomlValue(cargoSection(cargoToml, "lib")["name"])
	if name == "" {
		name = tomlValue(cargoSection(cargoToml, "package")["name"])
	}
	if name == "" {
		return ""
	}
	return strings.ReplaceAll(name, "-", "_") + ".wasm"
}
