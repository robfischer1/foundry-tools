package goregen

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

const (
	commitA   = "f6106f92ff44a4860b12e1c3cbbaaa971a2f29d7"
	commitB   = "775128e3b0751f8be4ae0286aa5b47915ffb7219"
	gravRev   = "aeb03bfd4a6f14e29b1a91dcdfad05e02695db39"
	lineNat   = "rustc 1.97.0 (2d8144b78 2026-07-07)"
	line1971  = "rustc 1.97.1 (8bab26f4f 2026-07-14)"
	line1990  = "rustc 1.99.0 (b940084d7 2026-09-28)"
	cargoToml = "[package]\nname = \"stellar-core\"\n\n[lib]\ncrate-type = [\"rlib\", \"cdylib\"]\n\n[features]\nwit-guest-alarm = []\nwit-guest-sqlport = []\nwit-guest-door = []\nwit-guest-identity = []\n"
)

// fake is the programs the atom execs, answering as the real ones would and
// writing what they would leave. Every call is kept.
type fake struct {
	t  *testing.T
	mu sync.Mutex
	// calls are the programs run, in order.
	calls []atoms.Cmd
	// native is the container's rustc line; laid maps a version to the line the
	// rustc laid for it prints.
	native string
	laid   map[string]string
	// tree is the stellar-core-rust tree every unpack writes, by path.
	tree map[string]string
	// guests and gens are what the build leaves and gravity writes, by core.
	guests map[string][]byte
	gens   map[string]map[string][]byte
	// lsRemote is what git ls-remote answers.
	lsRemote string
	// fail answers a command line containing the key with the text and exit 1.
	fail map[string]string
	// gravityVersion is what the staged gravity prints for --version.
	gravityVersion string
	// toolVersions are what wasm-tools and wac print; laidWasm is what a wasm-tools
	// laid from a release prints.
	toolVersions map[string]string
	laidWasm     string
	// remapped is what a build under another cargo home leaves, by core: the
	// guest a build with a --remap-path-prefix flag gives instead of guests[core].
	remapped map[string][]byte
	// onCall, when set, runs first on every program, to break the world under it.
	onCall func(c atoms.Cmd)
	// afterGravity runs when a generator run has written its files.
	afterGravity func()
	// artifactDir makes cargo leave a directory where the artifact goes.
	artifactDir bool
	// noCompose makes compose.sh exit 0 and write nothing.
	noCompose bool
}

func newFake(t *testing.T) *fake {
	return &fake{
		t: t, native: lineNat, laid: map[string]string{"1.97.1": line1971, "1.99.0": line1990},
		tree:   map[string]string{"Cargo.toml": cargoToml, "tools/compose/compose.sh": "#!/usr/bin/env bash\n"},
		guests: map[string][]byte{}, gens: map[string]map[string][]byte{}, fail: map[string]string{}, remapped: map[string][]byte{}, laidWasm: "wasm-tools 1.261.0 (abc 2026-08-01)",
		gravityVersion: "gravity 0.1.0 (" + gravRev + ")",
		toolVersions:   map[string]string{"wasm-tools": "wasm-tools 1.262.0 (abc 2026-09-01)", "wac": "wac-cli 0.12.0"},
	}
}

func line(c atoms.Cmd) string { return strings.Join(append([]string{c.Name}, c.Args...), " ") }

// has says whether a call's line contains text.
func (f *fake) has(text string) bool {
	for _, c := range f.calls {
		if strings.Contains(line(c), text) {
			return true
		}
	}
	return false
}

// count is how many calls' lines contain text.
func (f *fake) count(text string) int {
	n := 0
	for _, c := range f.calls {
		if strings.Contains(line(c), text) {
			n++
		}
	}
	return n
}

// call is the first call whose line contains text.
func (f *fake) call(text string) atoms.Cmd {
	for _, c := range f.calls {
		if strings.Contains(line(c), text) {
			return c
		}
	}
	f.t.Fatalf("no call contains %q in:\n%s", text, f.dump())
	return atoms.Cmd{}
}

func (f *fake) dump() string {
	var b strings.Builder
	for _, c := range f.calls {
		b.WriteString(line(c) + "\n")
	}
	return b.String()
}

func envOf(c atoms.Cmd, key string) (string, bool) {
	for _, kv := range c.Env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v, true
		}
	}
	return "", false
}

func writeFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// coreOf is the core a build's target directory belongs to: .../<core>/target.
func coreOf(c atoms.Cmd) string {
	target, _ := envOf(c, "CARGO_TARGET_DIR")
	return filepath.Base(filepath.Dir(target))
}

func (f *fake) run(_ context.Context, c atoms.Cmd) (string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	if f.onCall != nil {
		f.onCall(c)
	}
	l := line(c)
	for key, out := range f.fail {
		if strings.Contains(l, key) {
			return out, 1
		}
	}
	name := filepath.Base(c.Name)
	switch {
	case name == "rustc":
		if strings.Contains(c.Name, "/rust-") {
			for v, out := range f.laid {
				if strings.Contains(c.Name, "/rust-"+v+"/") {
					return out, 0
				}
			}
		}
		return f.native, 0
	case name == "rustup", name == "cargo" && len(c.Args) > 0 && c.Args[0] != "build":
		return "", 0
	case name == "wasm-tools" && strings.Contains(c.Name, "/wasm-tools-"):
		return f.laidWasm, 0
	case name == "wasm-tools" || name == "wac":
		return f.toolVersions[name], 0
	case name == "git" && c.Args[0] == "archive":
		writeFile(f.t, strings.TrimPrefix(c.Args[2], "--output="), []byte("tar"))
	case name == "git" && c.Args[0] == "ls-remote":
		return f.lsRemote, 0
	case name == "tar" && c.Args[0] == "-xf":
		for path, body := range f.tree {
			writeFile(f.t, filepath.Join(c.Args[3], path), []byte(body))
		}
	case name == "tar" && c.Args[0] == "-xzf":
		base := strings.TrimSuffix(filepath.Base(c.Args[1]), ".tar.gz")
		writeFile(f.t, filepath.Join(c.Args[3], base, "install.sh"), []byte("#!/bin/sh\n"))
	case name == "sh" && strings.HasSuffix(c.Args[0], "install.sh"):
		prefix := strings.TrimPrefix(c.Args[1], "--prefix=")
		for _, b := range []string{"rustc", "cargo"} {
			writeFile(f.t, filepath.Join(prefix, "bin", b), []byte("bin"))
		}
	case name == "cargo":
		core := coreOf(c)
		target, _ := envOf(c, "CARGO_TARGET_DIR")
		if g, ok := f.buildOf(c, core); ok && f.artifactDir {
			_ = g
			if err := os.MkdirAll(filepath.Join(target, "wasm32-unknown-unknown", "release", "stellar_core.wasm"), 0o755); err != nil {
				f.t.Fatal(err)
			}
		} else if ok {
			writeFile(f.t, filepath.Join(target, "wasm32-unknown-unknown", "release", "stellar_core.wasm"), g)
		}
	case name == "compose.sh":
		if !f.noCompose {
			g, _ := f.buildOf(c, coreOf(c))
			writeFile(f.t, c.Args[0], g)
		}
	case strings.HasPrefix(name, "gravity"):
		return f.gravity(c)
	}
	return "", 0
}

// buildOf is what a build of a core leaves: the remapped guest when the build
// was told to remap the cargo home and the scene has one, else the core's guest.
func (f *fake) buildOf(c atoms.Cmd, core string) ([]byte, bool) {
	if flags, set := envOf(c, "CARGO_ENCODED_RUSTFLAGS"); set {
		if g, ok := f.remapped[core]; ok && strings.HasSuffix(flags, "="+workstationCargoHome) {
			return g, true
		}
	}
	g, ok := f.guests[core]
	return g, ok
}

// gravity answers --version and writes what the recorded command generates: the
// bindings at --output, and the modules beside them.
func (f *fake) gravity(c atoms.Cmd) (string, int) {
	if len(c.Args) == 1 && c.Args[0] == "--version" {
		return f.gravityVersion, 0
	}
	var out, core string
	for i, a := range c.Args {
		switch a {
		case "--output":
			out = c.Args[i+1]
		case "--package":
			core = c.Args[i+1]
		}
	}
	if gofmt, _ := envOf(c, "GOFMT"); gofmt == "" {
		f.t.Errorf("gravity ran with no GOFMT: %v", c.Env)
	}
	for file, body := range f.gens[core] {
		writeFile(f.t, filepath.Join(filepath.Dir(out), file), body)
	}
	if f.afterGravity != nil {
		f.afterGravity()
	}
	return "", 0
}

// stagedFake points the generator and the formatter at stand-ins.
func stagedFake(t *testing.T) string {
	t.Helper()
	gravity := filepath.Join(t.TempDir(), "gravity-aeb03bf")
	writeFile(t, gravity, []byte("#!/bin/sh\n"))
	if err := os.Chmod(gravity, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GRAVITY", gravity)
	old := lookPath
	lookPath = func(name string) (string, error) {
		if name == "gofmt" {
			return "/opt/go/bin/gofmt", nil
		}
		return old(name)
	}
	t.Cleanup(func() { lookPath = old })
	return gravity
}

// fetchRecorder replaces the checksummed download with one that writes a stub
// and remembers what was asked for.
func fetchRecorder(t *testing.T) *[]string {
	t.Helper()
	var urls []string
	old := fetchPinned
	fetchPinned = func(_ context.Context, _ *http.Client, sources []string, sum, dest string) error {
		urls = append(urls, sources[len(sources)-1])
		if sum != checks.ToolSHA256[sources[len(sources)-1]] || !strings.Contains(sources[0], sum) {
			t.Errorf("fetch of %v held to %s", sources, sum)
		}
		writeFile(t, dest, []byte("tgz"))
		return nil
	}
	t.Cleanup(func() { fetchPinned = old })
	return &urls
}

// scene is a tree of cores, the programs it runs under, and the input to run.
type scene struct {
	t    *testing.T
	root string
	f    *fake
}

func newScene(t *testing.T) *scene {
	t.Helper()
	t.Setenv("ATOM_WORK", t.TempDir())
	t.Setenv("CARGO_HOME", "/usr/local/cargo")
	stagedFake(t)
	return &scene{t: t, root: t.TempDir(), f: newFake(t)}
}

func (s *scene) check(args ...string) checks.Verdict {
	s.t.Helper()
	in := atomkit.In{Root: s.root, Args: args, Exec: s.f.run}
	return Check(s.t.Context(), in)
}

func (s *scene) read(rel string) string {
	s.t.Helper()
	b, err := os.ReadFile(filepath.Join(s.root, rel))
	if err != nil {
		s.t.Fatal(err)
	}
	return string(b)
}

func (s *scene) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(s.root, rel))
	return err == nil
}

// coreSpec is a core to lay in the tree.
type coreSpec struct {
	name     string
	commit   string
	toolline string // core.toolchain's rustc line
	compose  []string
	// modules are the embedded files gravity writes, by provenance key.
	modules  map[string]string
	bindings bool
	// registry is a cargo home to plant in the recorded guest's bytes.
	registry string
}

// add lays a core whose record agrees with what the fake will build.
func (s *scene) add(c coreSpec) {
	t := s.t
	if c.commit == "" {
		c.commit = commitA
	}
	if c.toolline == "" {
		c.toolline = lineNat
	}
	if c.modules == nil {
		c.modules = map[string]string{"embedded_wasm": c.name + "_core.wasm"}
	}
	guest := []byte("guest-" + c.name)
	if c.registry != "" {
		guest = []byte("\x00" + c.registry + "/registry/src/index.crates.io-1949cf8c6b5b557f/serde-1/src/lib.rs\x00")
	}
	gens := map[string][]byte{"zz_generated.go": []byte("package " + c.name + "\n")}
	for _, file := range c.modules {
		gens[file] = []byte("module " + file)
	}
	s.f.guests[c.name], s.f.gens[c.name] = guest, gens
	dir := filepath.Join(s.root, c.name)
	writeFile(t, filepath.Join(dir, "guest.wasm"), guest)
	for file, b := range gens {
		writeFile(t, filepath.Join(dir, file), b)
	}
	build, script := "cargo build --locked --release --no-default-features --features wit-guest-"+c.name+" --target wasm32-unknown-unknown",
		"(cd \"$TMP/src\" && cargo build --locked --release --no-default-features --features wit-guest-"+c.name+" --target wasm32-unknown-unknown)\ncp \"$TMP/src/target/wasm32-unknown-unknown/release/stellar_core.wasm\" \"$OUT\"\n"
	if c.compose != nil {
		build = "tools/compose/compose.sh OUT.wasm " + strings.Join(c.compose, " ") + " (cargo build, wasm-tools component new, wac compose)"
		script = "(cd \"$TMP/src\" && tools/compose/compose.sh \"$TMP/composed.wasm\" " + strings.Join(c.compose, " ") + " >/dev/null)\ncp \"$TMP/composed.wasm\" \"$OUT\"\n"
	}
	writeFile(t, filepath.Join(dir, "build-guest.sh"), []byte("#!/bin/sh\nset -eu\nREV="+c.commit+"\n"+script))
	rec := func(key, file string, b []byte) string {
		return fmt.Sprintf("  %q: {\n    \"file\": %q,\n    \"sha256\": %q,\n    \"bytes\": %d\n  }", key, file, sum(b), len(b))
	}
	blocks := []string{
		"  \"core\": {\n    \"commit\": \"" + c.commit + "\",\n    \"build\": \"" + build + "\",\n    \"toolchain\": \"" + c.toolline + ", wasm-tools 1.261.0, wac 0.12.0\"\n  }",
		"  \"generator\": {\n    \"commit\": \"" + gravRev + "\",\n    \"command\": \"gravity --name " + c.name + "-core --package " + c.name + " --gofmt --output ./zz_generated.go ./guest.wasm\"\n  }",
		rec("guest_wasm", "guest.wasm", guest),
	}
	for key, file := range c.modules {
		blocks = append(blocks, rec(key, file, gens[file]))
	}
	if c.bindings {
		blocks = append(blocks, rec("bindings", "zz_generated.go", gens["zz_generated.go"]))
	}
	writeFile(t, filepath.Join(dir, "provenance.json"), []byte("{\n"+strings.Join(blocks, ",\n")+"\n}\n"))
}
