package goregen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// BuildGuest is what a core's build-guest.sh does, read off the script: the
// stellar-core-rust commit it builds, and either the worlds it composes or the
// cargo build it runs. The script is the authority for what runs; provenance.json
// records the same in prose, and the two are held to each other.
type BuildGuest struct {
	Rev string
	// Compose is the worlds handed to tools/compose/compose.sh; nil for a core
	// built with cargo alone.
	Compose []string
	// Cargo is the cargo argv (a plain core), and Artifact the file it leaves
	// under the target directory: wasm32-unknown-unknown/release/stellar_core.wasm.
	Cargo    []string
	Artifact string
}

var (
	revLine    = regexp.MustCompile(`(?m)^REV=([0-9a-f]{40})[ \t]*$`)
	revAny     = regexp.MustCompile(`(?m)^REV=`)
	composeRun = regexp.MustCompile(`tools/compose/compose\.sh\s+"[^"]*"\s*([^>)]*)`)
	cargoRun   = regexp.MustCompile(`(cargo build [^)]*)\)`)
	copyArt    = regexp.MustCompile(`cp "\$TMP/src/target/([^"]+)" "\$OUT"`)
	worldWord  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// ParseBuildGuest reads a core's build-guest.sh. Comment lines are not read.
func ParseBuildGuest(text string) (BuildGuest, error) {
	var code []string
	for _, l := range strings.Split(text, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			code = append(code, l)
		}
	}
	body := strings.Join(code, "\n")
	revs := revAny.FindAllString(body, -1)
	m := revLine.FindStringSubmatch(body)
	switch {
	case len(revs) > 1:
		return BuildGuest{}, errors.New("build-guest.sh sets two REV= lines")
	case m == nil:
		return BuildGuest{}, errors.New("build-guest.sh has no REV=<40-hex commit> line")
	}
	g := BuildGuest{Rev: m[1]}
	compose, cargo := composeRun.FindStringSubmatch(body), cargoRun.FindStringSubmatch(body)
	switch {
	case compose != nil && cargo != nil:
		return BuildGuest{}, errors.New("build-guest.sh runs both tools/compose/compose.sh and cargo build")
	case compose != nil:
		for _, w := range strings.Fields(compose[1]) {
			if !worldWord.MatchString(w) {
				break
			}
			g.Compose = append(g.Compose, w)
		}
		if len(g.Compose) == 0 {
			return BuildGuest{}, errors.New("build-guest.sh's tools/compose/compose.sh line names no world")
		}
	case cargo != nil:
		g.Cargo = strings.Fields(cargo[1])
		art := copyArt.FindStringSubmatch(body)
		if art == nil {
			return BuildGuest{}, errors.New("build-guest.sh runs cargo build and copies no artifact out of its target")
		}
		g.Artifact = art[1]
	default:
		return BuildGuest{}, errors.New("build-guest.sh runs neither tools/compose/compose.sh nor cargo build")
	}
	return g, nil
}

// RenderBuildGuest is the build-guest.sh of a new composed core, in the form
// classescore's is: one world through the revision's own compose.sh, under the
// rustc provenance.json records.
func RenderBuildGuest(w World, commit, wasmTools, wac string) string {
	// WHERE THE KIT IS comes from the one setting (checks.RustKit): a kit in a
	// subdirectory is built from there.
	kitDir := "$TMP/src"
	if checks.RustKit.Subdir != "" {
		kitDir += "/" + checks.RustKit.Subdir
	}
	return fmt.Sprintf(`#!/bin/sh
# Build the component this package embeds: stellar-core-rust at the pinned
# commit, the aiws:%[1]s world (feature wit-guest-%[1]s) made a component and
# composed through tools/compose/compose.sh (one world in the composition), so
# the module is the one a composed fleet component carries. Its exports are
# those of aiws:%[1]s/%[2]s.
# Usage: build-guest.sh OUTPUT.wasm
# Needs fleet git access, the wasm32 target (rustup target add
# wasm32-unknown-unknown), wasm-tools %[4]s and wac %[5]s. A guest is a
# cdylib, so it is built from the crate's own tree rather than as a dependency
# of a wrapper crate.
set -eu
# Build with the rustc provenance.json records, not whatever the host defaults to.
PIN_RUSTC_PROVENANCE="$(dirname "$0")/provenance.json"
. "$(dirname "$0")/../tools/pin-rustc.sh"
REV=%[3]s
OUT=$(realpath -m "$1")
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
git clone --quiet %[6]s "$TMP/src"
git -C "$TMP/src" checkout --quiet "$REV"
(cd "%[7]s" && tools/compose/compose.sh "$TMP/composed.wasm" %[1]s >/dev/null)
cp "$TMP/composed.wasm" "$OUT"
`, w.Pkg, w.Name, commit, wasmTools, wac, checks.RustKit.GitURL(), kitDir)
}

// GeneratorCommand is the gravity command a composed core records: the world is
// `root` in a composition, so the bindings are named after the world instead.
func GeneratorCommand(core string, w World) string {
	return "gravity --name " + w.Name + " --package " + core + " --gofmt --output ./zz_generated.go ./guest.wasm"
}

// ComposeBuild is the core.build prose of a composed core.
func ComposeBuild(w World) string {
	return "tools/compose/compose.sh OUT.wasm " + w.Pkg + " (cargo build --locked --release --no-default-features --features wit-guest-" + w.Pkg +
		" --target wasm32-unknown-unknown, wasm-tools component new, wac compose)"
}

// NewProv is everything a first provenance.json records.
type NewProv struct {
	Core, Rev, Commit, Toolchain, GenCommit, WitSHA256 string
	World                                              World
	Guest, Embedded, Bindings                          Rec
}

// RenderProvenance writes a new core's first provenance.json in the shape the
// composed cores use. It records what regeneration measured: the core, the
// compiler, the generator and the three files. The wit block carries what the
// rust tree's own copy of the WIT gives (its path and its sha256) and a commit
// left empty, because the revision of rob/stellar-core that WIT came from is in
// no tree this reads; and there is NO tape block, because the tape is vendored
// into stellar-core-go by a person. LeftForAHuman names both.
func RenderProvenance(p NewProv) string {
	var b strings.Builder
	block := func(name string, fields [][2]string, last bool) {
		fmt.Fprintf(&b, "  %s: {\n", q(name))
		for i, f := range fields {
			sep := ","
			if i == len(fields)-1 {
				sep = ""
			}
			fmt.Fprintf(&b, "    %s: %s%s\n", q(f[0]), f[1], sep)
		}
		if last {
			b.WriteString("  }\n")
		} else {
			b.WriteString("  },\n")
		}
	}
	rec := func(key string, r Rec, note string, last bool) {
		fields := [][2]string{{"file", q(r.File)}, {"sha256", q(r.SHA256)}, {"bytes", fmt.Sprint(r.Bytes)}}
		if note != "" {
			fields = append(fields, [2]string{"note", q(note)})
		}
		block(key, fields, last)
	}
	pkg := p.World.Pkg
	b.WriteString("{\n")
	block("wit", [][2]string{
		{"repo", q("rob/stellar-core")}, {"path", q("wit/aiws-" + pkg + ".wit")}, {"commit", q("")}, {"sha256", q(p.WitSHA256)},
		{"note", q("written by dev:go-regen --new: the sha256 is of the WIT vendored in stellar-core-rust " + p.Rev + " (aiws:" + pkg + "@" + p.World.Version + "); the rob/stellar-core commit it was pinned at is for a person to fill")},
	}, false)
	core := [][2]string{{"repo", q(checks.RustKit.Repo)}}
	if checks.RustKit.Subdir != "" {
		core = append(core, [2]string{"subdir", q(checks.RustKit.Subdir)})
	}
	block("core", append(core,
		[2]string{"commit", q(p.Commit)}, [2]string{"build", q(ComposeBuild(p.World))}, [2]string{"toolchain", q(p.Toolchain)},
		[2]string{"note", q("stellar-core-rust " + p.Rev + " (" + p.Commit[:7] + "), aiws:" + pkg + "@" + p.World.Version + ", world " + p.World.Name + "; written by dev:go-regen --new")}), false)
	block("generator", [][2]string{
		{"repo", q("forks/gravity")}, {"commit", q(p.GenCommit)}, {"command", q(GeneratorCommand(p.Core, p.World))},
	}, false)
	rec("guest_wasm", p.Guest, "", false)
	rec("embedded_wasm", p.Embedded, "", false)
	rec("bindings", p.Bindings, "what the pinned generator writes from guest.wasm; dev:go-regen regenerates it and refreshes every hash above", true)
	b.WriteString("}\n")
	return b.String()
}

// q is a JSON string, with no HTML escaping: provenance.json is read by people.
func q(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // a string always encodes
	return strings.TrimSuffix(buf.String(), "\n")
}

var witUse = regexp.MustCompile(`aiws:(%?[a-z][a-z0-9-]*)[/@]`)

// LeftForAHuman names the provenance blocks a first regeneration cannot fill,
// in the order a person should take them: the WIT's commit, the tape, and one
// wit_<package> block per other package the WIT uses.
func LeftForAHuman(wit, pkg string) []string {
	left := []string{"wit.commit", "tape"}
	seen := map[string]bool{pkg: true}
	for _, m := range witUse.FindAllStringSubmatch(wit, -1) {
		other := m[1]
		if seen[other] {
			continue
		}
		seen[other] = true
		left = append(left, "wit_"+strings.ReplaceAll(strings.TrimPrefix(other, "%"), "-", "_")+" (the world uses aiws:"+other+")")
	}
	return left
}
