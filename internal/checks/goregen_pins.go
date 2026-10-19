package checks

import (
	"fmt"
	"slices"
	"strings"
)

// THE PINS OF dev:go-regen (atoms/dev/go-regen): the Go formatter gravity execs
// and the rust compilers a core may be pinned to.
//
// GOFMT. Gravity's `--gofmt` execs $GOFMT, else `gofmt` on PATH (forks/gravity
// cmd/gravity/src/gofmt.rs at aeb03bfd), and that is the ONLY Go the regeneration
// needs. The official linux-amd64 release is staged as a url tool (tools.toml
// [tool.gofmt]) at the version of the Go toolchain image (ImageGo, go1.27.2), so
// a regenerated binding is formatted by the formatter the gate's go:gofmt checks
// it with. The sum is go.dev's own (https://go.dev/dl/?mode=json), equal to the
// downloaded bytes. The tarball unpacks to go/, whose bin/ holds gofmt (and go).
const (
	GofmtGoRelease = "1.27.2"
	GofmtURL       = "https://go.dev/dl/go1.27.2.linux-amd64.tar.gz"
	// GofmtMember is the formatter inside that tarball.
	GofmtMember = "go/bin/gofmt"
)

// A CORE'S GUEST IS A FUNCTION OF ITS COMPILER (provenance.json core.toolchain,
// "rustc X.Y.Z (hash date)"): the rust container carries one rustc (the
// ImageRust tag, RustComponentsTag) and a core pinned to another is built with
// that one, laid by the atom from static.rust-lang.org's own dist tarballs: the
// compiler, cargo, and the standard library for the host and for wasm32. Each is
// served mirror-first and held to the sum below (ToolSources), and each version
// is also a url tool in tools.toml so the mirror holds it. The sums are the
// .sha256 files beside the tarballs, equal to the downloaded bytes.
//
// A version with no line here is CANNOT RUN (RegenRustLine), never the nearest
// one. 1.97.1 is what most cores of stellar-core-go record, 1.99.0 what the
// newest do; the image's own 1.97.0 needs nothing laid.
const (
	RustDist1971Tag = "1.97.1"
	RustDist1990Tag = "1.99.0"

	RustDist1971RustcURL   = "https://static.rust-lang.org/dist/rustc-1.97.1-x86_64-unknown-linux-gnu.tar.gz"
	RustDist1971CargoURL   = "https://static.rust-lang.org/dist/cargo-1.97.1-x86_64-unknown-linux-gnu.tar.gz"
	RustDist1971StdURL     = "https://static.rust-lang.org/dist/rust-std-1.97.1-x86_64-unknown-linux-gnu.tar.gz"
	RustDist1971StdWasmURL = "https://static.rust-lang.org/dist/rust-std-1.97.1-wasm32-unknown-unknown.tar.gz"

	RustDist1990RustcURL   = "https://static.rust-lang.org/dist/rustc-1.99.0-x86_64-unknown-linux-gnu.tar.gz"
	RustDist1990CargoURL   = "https://static.rust-lang.org/dist/cargo-1.99.0-x86_64-unknown-linux-gnu.tar.gz"
	RustDist1990StdURL     = "https://static.rust-lang.org/dist/rust-std-1.99.0-x86_64-unknown-linux-gnu.tar.gz"
	RustDist1990StdWasmURL = "https://static.rust-lang.org/dist/rust-std-1.99.0-wasm32-unknown-unknown.tar.gz"
)

// RegenNewRustTag is the compiler a core brought into being by `--new` is built
// with: the newest this atom can lay, which is what the newest cores of
// stellar-core-go record (classescore and observabilityapi, 1.99.0).
const RegenNewRustTag = RustDist1990Tag

// RustDist is the four dist tarballs one laid toolchain is made of, in install
// order: the compiler, cargo, the host standard library, the wasm32 one.
type RustDist struct {
	Version string
	URLs    []string
}

// RustDists is every toolchain the atom can lay, by version.
var RustDists = []RustDist{
	{RustDist1971Tag, []string{RustDist1971RustcURL, RustDist1971CargoURL, RustDist1971StdURL, RustDist1971StdWasmURL}},
	{RustDist1990Tag, []string{RustDist1990RustcURL, RustDist1990CargoURL, RustDist1990StdURL, RustDist1990StdWasmURL}},
}

// RegenRustLine reads the rustc a provenance.json records ("rustc 1.99.0
// (b940084d7 2026-09-28), wasm-tools 1.261.0, ..."): the whole version line
// `rustc --version` must print, and the bare version. ok is false for a record
// that names no rustc.
func RegenRustLine(toolchain string) (line, version string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(toolchain), "rustc ")
	if !found {
		return "", "", false
	}
	version, tail, _ := strings.Cut(rest, " ")
	open, closed := strings.HasPrefix(tail, "("), strings.Index(tail, ")")
	if version == "" || !open || closed < 0 {
		return "", "", false
	}
	return "rustc " + version + " " + tail[:closed+1], version, true
}

// RegenRustDist answers the tarballs for a version the image does not carry, or
// an error naming the version asked for and every one that could be laid
// (pinned against available), the image's own among them.
func RegenRustDist(version, image string) (RustDist, error) {
	var have []string
	for _, d := range RustDists {
		if d.Version == version {
			return d, nil
		}
		have = append(have, d.Version)
	}
	slices.Sort(have)
	return RustDist{}, fmt.Errorf("core.toolchain pins rustc %s but this atom carries rustc %s and can lay only %s",
		version, image, strings.Join(have, ", "))
}
