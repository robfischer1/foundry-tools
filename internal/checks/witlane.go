package checks

import (
	"path"
	"regexp"
	"slices"
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
	// WitGuestAlias is the bare feature name that predates per-world features.
	// It is an ALIAS for one world, never a world of its own once any
	// wit-guest-<world> feature exists (see WitGuestWorlds).
	WitGuestAlias = "wit-guest"
	// WitGuestPrefix marks a per-world feature: one build = one world.
	WitGuestPrefix = "wit-guest-"
	// WitGuestTarget is the target the guest is built for. It is the core-module
	// target: wit-bindgen embeds the component type in the module and
	// `wasm-tools component new` lifts it.
	WitGuestTarget = "wasm32-unknown-unknown"
	// WitGuestTargetDir is where the guest build writes: the repository's
	// release cache volume (ReleaseCacheFor), OUTSIDE the gate's debug
	// cargo-target — a wasm32 release build there would only add artifacts the
	// native atoms never read. cargo lays the guest under
	// <dir>/wasm32-unknown-unknown/release/, apart from the host's release/.
	// The build copies the module out in the same exec, and wasm-tools reads
	// the copy.
	WitGuestTargetDir = ReleaseCachePath
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

	// WacVersion / WacURL fetch `wac`, the composer tools/compose/compose.sh
	// drives. The release asset is the bare static binary (no tarball), so the
	// atom places the file itself. The pin is the one compose.sh's header was
	// verified against (faraday37-038), with wasm-tools 1.261.0.
	// renovate: datasource=github-releases depName=bytecodealliance/wac
	WacVersion = "0.12.0"
	WacURL     = "https://github.com/bytecodealliance/wac/releases/download/v0.12.0/wac-cli-x86_64-unknown-linux-musl"

	// WitComposeScript / WitReplayManifest are the two files rust:wit-compose
	// needs the tree to track; WitComposedPath is where the composition lands
	// and WitReplayTargetDir where the replay host builds, in the release
	// volume but apart from the guest's target dir (the host is a workspace of
	// its own). WitReplayHost is where that build copies the host out to.
	WitComposeScript   = "tools/compose/compose.sh"
	WitReplayManifest  = "tools/replay/Cargo.toml"
	WitComposedPath    = "/tmp/fleet.component.wasm"
	WitReplayTargetDir = "/cache/cargo-release/replay-host"
	WitReplayHost      = "/tmp/replay-host/replay"
	// WitRedGlob is where a tree's red tapes live (F18): cases marked
	// "expect": "red", replayed through the same composition. tools/replay
	// judges them as red - a violated case passes only while the component
	// answers its `today` - so they are handed to it beside the world tapes.
	WitRedGlob = "tests/red/*.json"
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
	rest, ok := strings.CutPrefix(raw, `"`)
	if !ok {
		return ""
	}
	value, _, closed := strings.Cut(rest, `"`)
	if !closed {
		return ""
	}
	return value
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
		} else if m := keyLine.FindStringSubmatch(line); m != nil && in {
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

// WitGuestWorlds answers the features rust:wit-guest grades, sorted: every
// [features] key with the wit-guest- prefix, one world each, so a new world is
// picked up with no lane edit (rob/stellar-core-rust#14205).
//
// THE BARE ALIAS IS SKIPPED WHEN A WORLD EXISTS: it resolves to one of them, so
// building it too would grade that world twice, and a star that drops the alias
// must not go inert. THE ALIAS ALONE, WITH NO wit-guest-<world> FEATURE, IS
// ITSELF THE ONE WORLD (behaves as before the split): such a crate has a guest
// the old atom graded, and going inert would silently stop grading it - the
// exact blind spot this discovery exists to close. A crate with neither is
// inert (nil).
func WitGuestWorlds(cargoToml string) []string {
	var worlds []string
	for name := range cargoSection(cargoToml, "features") {
		if strings.HasPrefix(name, WitGuestPrefix) && len(name) > len(WitGuestPrefix) {
			worlds = append(worlds, name)
		}
	}
	slices.Sort(worlds)
	if len(worlds) == 0 && HasCargoFeature(cargoToml, WitGuestAlias) {
		return []string{WitGuestAlias}
	}
	return worlds
}

// WitComposeTapes answers the tapes rust:wit-compose replays, in world order:
// tests/tapes/<world>.json for each world the manifest declares that the tree
// carries one for. The bare alias names a world already named by its own
// feature, so it is not a tape of its own.
func WitComposeTapes(cargoToml string, files []string) []string {
	var tapes []string
	for _, feature := range WitGuestWorlds(cargoToml) {
		world := strings.TrimPrefix(feature, WitGuestPrefix)
		if feature == WitGuestAlias {
			continue
		}
		if tape := "tests/tapes/" + world + ".json"; slices.Contains(files, tape) {
			tapes = append(tapes, tape)
		}
	}
	return tapes
}

// WitComposeRed answers the red tapes rust:wit-compose replays after the world
// tapes: every tests/red/*.json the tree tracks, sorted. They are named by
// package, not by world, so none is filtered by the manifest: tools/replay
// calls only a red tape's violated cases and holds each to its `today`.
func WitComposeRed(files []string) []string {
	var red []string
	for _, f := range files {
		if dir, name := path.Split(f); dir == "tests/red/" && strings.HasSuffix(name, ".json") {
			red = append(red, f)
		}
	}
	slices.Sort(red)
	return red
}
