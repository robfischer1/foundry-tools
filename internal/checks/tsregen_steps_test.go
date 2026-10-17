package checks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// THE GOLDEN OF EACH STEP'S ARGV. ts:regen was one `sh /inner.sh` per container;
// every step is now a program and its arguments, and these are the arguments.
func TestRegenStepArgvGolden(t *testing.T) {
	for _, c := range []struct {
		name string
		got  []string
		want []string
	}{
		{"init a clone", RegenGitInit("/w/sc"), []string{"git", "init", "--quiet", "/w/sc"}},
		{"fetch one commit", RegenGitFetch(TSRegenWitGit, c40), []string{"git", "fetch", "--quiet", "--depth", "1", TSRegenWitGit, c40}},
		{"read a pin", RegenGitShow(c40, "wit/a.wit"), []string{"git", "show", c40 + ":wit/a.wit"}},
		{"archive the core", RegenGitArchive("/w/core.tar", c40), []string{"git", "archive", "--format=tar", "--output=/w/core.tar", c40}},
		{"unpack the core", RegenTar("/w/core.tar", "/w/core", false, 0, ""), []string{"tar", "-xf", "/w/core.tar", "-C", "/w/core"}},
		{"unpack a release member", RegenTar("/w/t.tgz", "/w/bin", true, 1, "wasm-tools-1.250.0-x86_64-linux/wasm-tools"),
			[]string{"tar", "-xzf", "/w/t.tgz", "-C", "/w/bin", "--strip-components=1", "wasm-tools-1.250.0-x86_64-linux/wasm-tools"}},
		{"unpack node whole", RegenTar("/w/n.tgz", "/w/node", true, 1, ""), []string{"tar", "-xzf", "/w/n.tgz", "-C", "/w/node", "--strip-components=1"}},
		{"copy rustup", RegenCopyTree("/usr/local/rustup", "/w/rustup"), []string{"cp", "-a", "/usr/local/rustup/.", "/w/rustup"}},
		{"add the target", RegenRustupTarget(), []string{"rustup", "target", "add", "wasm32-unknown-unknown"}},
		{"diff", RegenDiff("/w/gen", "/src/src/xcore-gen"), []string{"diff", "-r", "-q", "-x", "provenance.json", "-x", ".jco-version", "/w/gen", "/src/src/xcore-gen"}},
	} {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, c.got, c.want)
		}
	}
}

// A star's inner script says /out and /in because it ran at the root of a
// container; the pod's root is read-only, so the same script is laid with the
// directories it was given.
func TestRegenScriptLaysTheStarsOwnScriptOnTheDirectoriesItWasGiven(t *testing.T) {
	body := strings.Join([]string{
		"#!/bin/sh",
		"# Runs INSIDE the engine container.",
		"set -eux",
		"rustup target add wasm32-unknown-unknown",
		`cargo install wasm-tools --locked --version "$WASM_TOOLS_VERSION"`,
		"mkdir -p /out",
		`cp "$CARGO_TARGET_DIR/wasm32-unknown-unknown/release/stellar_core.wasm" /out/guest.wasm`,
		"wasm-tools component new /out/guest.wasm -o /out/classes.component.wasm",
		"jco transpile /in/classes.component.wasm --name classes-core.config -o /out",
		"find /out/interfaces -name '*.d.ts' -exec sed -i 's/a/b/' {} +",
		`const p = "/out/classes-core.config.core.wasm";`,
		"ls -lR /out",
		"/out /out",
		"echo /output /index /inside",
		"",
	}, "\n")
	got := RegenScript(body, "/w/out", "/w/in", false)
	for _, want := range []string{
		"mkdir -p /w/out\n", "/w/out/guest.wasm\n", "-o /w/out/classes.component.wasm\n",
		"/w/in/classes.component.wasm --name classes-core.config -o /w/out\n",
		"find /w/out/interfaces", `"/w/out/classes-core.config.core.wasm"`, "ls -lR /w/out\n", "/w/out /w/out\n",
		"echo /output /index /inside\n", "rustup target add wasm32-unknown-unknown\n",
		`cargo install wasm-tools --locked --version "$WASM_TOOLS_VERSION"` + "\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the script lacks %q:\n%s", want, got)
		}
	}
	if regexp.MustCompile(`(?m)(^|\s)/(out|in)(\s|/|$)`).MatchString(strings.ReplaceAll(got, "/output", "")) {
		t.Errorf("a root path is left in the script:\n%s", got)
	}
}

// A release binary on PATH makes the script's own install line a compile over
// it, so that line, exactly, is dropped; a line that is not exactly it stays.
func TestRegenScriptDropsOnlyTheExactWasmToolsInstallLine(t *testing.T) {
	line := `cargo install wasm-tools --locked --version "$WASM_TOOLS_VERSION"`
	body := "a\n" + line + "\nb\n" + line + " --force\n  " + line + "\nc\n"
	got := RegenScript(body, "/o", "/i", true)
	if want := "a\nb\n" + line + " --force\n  " + line + "\nc\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if kept := RegenScript(body, "/o", "/i", false); kept != body {
		t.Errorf("a compile (no release) must leave the script as the star wrote it: %q", kept)
	}
}

// diff printed /gen and /shipped when the two directories were mounts there; the
// reason a finding carries still reads that way.
func TestRegenRelabelDiffKeepsTheMountNamesTheRecordAlwaysSaid(t *testing.T) {
	out := "Files /w/gen/a.js and /src/src/xcore-gen/a.js differ\nOnly in /w/gen/interfaces: b.d.ts\nOnly in /src/src/xcore-gen: c.js"
	want := "Files /gen/a.js and /shipped/a.js differ\nOnly in /gen/interfaces: b.d.ts\nOnly in /shipped: c.js"
	if got := RegenRelabelDiff(out, "/w/gen", "/src/src/xcore-gen"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNodeRegenPinAgrees(t *testing.T) {
	if !strings.Contains(NodeRegenURL, "/v"+Node24Version+"/node-v"+Node24Version+"-linux-x64.tar.gz") {
		t.Errorf("%s does not name node %s", NodeRegenURL, Node24Version)
	}
	if !strings.HasPrefix(NodeRegenURL, "https://nodejs.org/dist/") {
		t.Errorf("%s", NodeRegenURL)
	}
	if !sha256Hex.MatchString(ToolSHA256[NodeRegenURL]) {
		t.Errorf("node has no checksum: %q", ToolSHA256[NodeRegenURL])
	}
}

func sumOf(b string) string { h := sha256.Sum256([]byte(b)); return hex.EncodeToString(h[:]) }

// THE FETCH TRIES EACH SOURCE IN ORDER AND HOLDS EVERY ONE TO THE SAME SUM: a
// mirror that is missing or serves other bytes is a failed source, never an
// install.
func TestFetchPinnedFallsThroughABadSourceAndVerifiesTheSum(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("asset")) }))
	defer good.Close()
	missing := httptest.NewServer(http.NotFoundHandler())
	defer missing.Close()
	wrong := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("tampered")) }))
	defer wrong.Close()

	dest := filepath.Join(t.TempDir(), "a", "asset")
	if err := FetchPinned(context.Background(), nil, []string{missing.URL, wrong.URL, good.URL}, sumOf("asset"), dest); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(dest); err != nil || string(b) != "asset" {
		t.Fatalf("%q, %v", b, err)
	}

	err := FetchPinned(context.Background(), nil, []string{missing.URL, wrong.URL}, sumOf("asset"), filepath.Join(t.TempDir(), "x"))
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "sha256") {
		t.Errorf("every source's cause is named: %v", err)
	}
	if err := FetchPinned(context.Background(), nil, nil, sumOf("asset"), filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("no source is not a fetch")
	}
	if err := FetchPinned(context.Background(), nil, []string{"http://127.0.0.1:1/x"}, sumOf("a"), filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("an unreachable source is an error")
	}
	// a destination that cannot be made is the fetch's failure too
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := FetchPinned(context.Background(), nil, []string{good.URL}, sumOf("asset"), filepath.Join(file, "sub", "x")); err == nil {
		t.Error("an unwritable destination is an error")
	}
}

func TestSHA256FileIsSha256sum(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte{0xff, 0xfe, 0x00, 'a'}, 0o644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte{0xff, 0xfe, 0x00, 'a'})
	if got, err := SHA256File(p); err != nil || got != hex.EncodeToString(h[:]) {
		t.Errorf("a binary hashes as its bytes: %q, %v", got, err)
	}
	if _, err := SHA256File(p + ".gone"); err == nil {
		t.Error("a missing file has no digest")
	}
}

func TestFetchPinnedRefusesWhatCannotBeRequestedOrWritten(t *testing.T) {
	if err := FetchPinned(context.Background(), nil, []string{"http://[::1"}, sumOf("a"), filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("a source that is not a URL is an error")
	}
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("asset")) }))
	defer good.Close()
	dir := t.TempDir()
	if err := FetchPinned(context.Background(), nil, []string{good.URL}, sumOf("asset"), dir); err == nil {
		t.Error("a destination that is a directory cannot be written")
	}
	// a body that ends before its length is a failed source, and leaves nothing
	cut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}))
	defer cut.Close()
	dest := filepath.Join(t.TempDir(), "x")
	if err := FetchPinned(context.Background(), nil, []string{cut.URL}, sumOf("asset"), dest); err == nil {
		t.Error("a truncated body is an error")
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("a failed fetch leaves no file")
	}
}

func TestSHA256FileOfADirectoryHasNoDigest(t *testing.T) {
	if _, err := SHA256File(t.TempDir()); err == nil {
		t.Error("a directory has no bytes to hash")
	}
}

func TestRegenRemapFlagsPutTheGuestBackWhereItWasRecorded(t *testing.T) {
	got := RegenRemapFlags("/w/core", "/w/t", "/cache/cargo-home", "/w/rustup")
	want := "--remap-path-prefix=/w/core=/src --remap-path-prefix=/w/t=/tmp/target --remap-path-prefix=/cache/cargo-home=/usr/local/cargo --remap-path-prefix=/w/rustup=/usr/local/rustup"
	if got != want {
		t.Errorf("got %q", got)
	}
	if got := RegenRemapFlags("/src", "/tmp/target", "", "/usr/local/rustup"); got != "" {
		t.Errorf("a directory already where it was recorded is not remapped: %q", got)
	}
}
