package regenlane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

const (
	rgCommit = "bbf2825c710bca00df747d477865f256e14abea7"
	rgCore   = "cccccccccccccccccccccccccccccccccccccccc"
)

var (
	guestBytes = []byte{0x00, 'a', 's', 'm', 0xff, 0xfe}
	compBytes  = []byte{0x00, 'a', 's', 'm', 0x01, 0xfe, 0xff}
)

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// the star's two inner scripts, as far as the atom reads them: what they name
// at a container's root, and the one line that compiles wasm-tools.
const (
	coreScript = `#!/bin/sh
set -eux
rustup target add wasm32-unknown-unknown
cargo install wasm-tools --locked --version "$WASM_TOOLS_VERSION"
mkdir -p /out
cp "$CARGO_TARGET_DIR/wasm32-unknown-unknown/release/stellar_core.wasm" /out/guest.wasm
`
	transpileScript = `#!/bin/sh
set -eux
npm install -g "@bytecodealliance/jco@$JCO_VERSION"
mkdir -p /out
jco transpile /in/WORLD.component.wasm --name WORLD-core.config -o /out
`
)

// fake is the programs the atom execs, answering as the real ones would and
// writing what they would leave.
type fake struct {
	t     *testing.T
	mu    sync.Mutex
	calls []atoms.Cmd
	// bodies are the scripts as they were laid, by file name
	bodies map[string]string
	// fail answers a program line that contains the key with the text and the
	// exit in exit (1 when absent)
	fail map[string]string
	exit map[string]int
	// omit names files the build and the transpile do not leave
	omit map[string]bool
	// coreWit is the core's own copy of the WIT; "-" leaves none
	coreWit string
	diff    string
	diffEx  int
	rustc   string
	wasmVer string
	jco     string
	busy    int // "text file busy" answers before a script runs
}

const (
	stdManifest = "file:lib/rustlib/wasm32-unknown-unknown/lib/libcore-0.rlib\n"
	toolchain   = "1.90.0-x86_64-unknown-linux-gnu"
)

func newFake(t *testing.T) *fake {
	return &fake{t: t, bodies: map[string]string{}, fail: map[string]string{}, exit: map[string]int{}, omit: map[string]bool{},
		coreWit: "wit text", rustc: "rustc 1.90.0 (1159e78c4 2025-09-14)\n", wasmVer: "wasm-tools 1.250.0\n", jco: "1.35.0\n"}
}

func (f *fake) line(c atoms.Cmd) string {
	return strings.Join(append([]string{c.Name}, c.Args...), " ")
}

var (
	mkdirRe = regexp.MustCompile(`mkdir -p (\S+)`)
	outRe   = regexp.MustCompile(`-o (\S+)`)
	worldRe = regexp.MustCompile(`ts-regen/([a-z0-9]+)/`)
)

func (f *fake) run(_ context.Context, c atoms.Cmd) (string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	line := f.line(c)
	for key, out := range f.fail {
		if strings.Contains(line, key) {
			if code, ok := f.exit[key]; ok {
				return out, code
			}
			return out, 1
		}
	}
	switch {
	case strings.HasPrefix(line, "git show "):
		return f.show(line), 0
	case strings.HasPrefix(line, "git archive "):
		return "", write(f.t, strings.TrimPrefix(c.Args[2], "--output="), "tar")
	case strings.HasPrefix(line, "tar -xf "):
		f.unpackCore(c.Args[3])
	case strings.HasPrefix(line, "cp -a ") && strings.HasSuffix(c.Args[2], "/rustup"):
		f.homeSkeleton(c.Args[2])
	case strings.HasPrefix(line, "tar -xzf ") && strings.HasSuffix(c.Args[3], checks.RustStdWasmComponent):
		write(f.t, filepath.Join(c.Args[3], "lib/rustlib/wasm32-unknown-unknown/lib/libcore-0.rlib"), "rlib")
		if !f.omit["manifest.in"] {
			write(f.t, filepath.Join(c.Args[3], "manifest.in"), stdManifest)
		}
	case strings.HasPrefix(line, "tar -xzf "):
		name := "wasm-tools"
		if strings.HasSuffix(c.Args[3], "node") {
			name = "bin/node"
		}
		return "", write(f.t, filepath.Join(c.Args[3], name), "bin")
	case strings.HasSuffix(c.Name, "-inner.sh"):
		return f.script(c)
	case c.Name == "diff":
		return f.diff, f.diffEx
	}
	return "", 0
}

// homeSkeleton is what the copy of the image's rustup home holds that the std
// is registered in: the one toolchain, its component list and rustup's config.
func (f *fake) homeSkeleton(home string) {
	if f.omit["toolchain"] {
		return
	}
	rl := filepath.Join(home, "toolchains", toolchain, "lib", "rustlib")
	if f.omit["rustlib"] {
		if err := os.MkdirAll(filepath.Join(home, "toolchains", toolchain), 0o755); err != nil {
			f.t.Fatal(err)
		}
		return
	}
	if f.omit["components-dir"] {
		if err := os.MkdirAll(filepath.Join(rl, "components"), 0o755); err != nil {
			f.t.Fatal(err)
		}
	} else if !f.omit["components"] {
		write(f.t, filepath.Join(rl, "components"), "cargo-x\nrust-std-x\nrustc-x\n")
	}
	if !f.omit["multirust-config.toml"] {
		write(f.t, filepath.Join(rl, "multirust-config.toml"), "config_version = \"1\"\n")
	}
}

// show is `git show <commit>:<path>`: the text the provenance was made from.
func (f *fake) show(line string) string {
	switch {
	case strings.Contains(line, ":wit/aiws-result.wit"):
		return "result text"
	case strings.Contains(line, ":wit/aiws-"):
		return "wit text"
	}
	return "tape text"
}

// unpackCore is the core's tree: the WIT the guest is built against.
func (f *fake) unpackCore(dir string) {
	w := worldRe.FindStringSubmatch(dir)[1]
	if f.coreWit == "-" {
		return
	}
	write(f.t, filepath.Join(dir, "wit/aiws-"+w+".wit"), f.coreWit)
	write(f.t, filepath.Join(dir, "wit/aiws-result.wit"), "result text")
}

func write(t *testing.T, path, body string) int {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return 0
}

// leave writes what a script leaves, but for the files omit names.
func (f *fake) leave(dir, name, body string) {
	if !f.omit[name] {
		write(f.t, filepath.Join(dir, name), body)
	}
}

// script runs a laid inner script as its text says: the build leaves its files
// where the script's `mkdir -p` names, the transpile where its `-o` does.
func (f *fake) script(c atoms.Cmd) (string, int) {
	if f.busy > 0 {
		f.busy--
		return "fork/exec " + c.Name + ": text file busy", -1
	}
	raw, err := os.ReadFile(c.Name)
	if err != nil {
		f.t.Fatal(err)
	}
	body, base := string(raw), filepath.Base(c.Name)
	f.bodies[base] = body
	if w, ok := strings.CutPrefix(base, "transpile-"); ok {
		w = strings.TrimSuffix(w, "-inner.sh")
		dir := outRe.FindStringSubmatch(body)[1]
		f.leave(dir, ".jco-version", f.jco)
		f.leave(dir, w+"-core.config.js", "js")
		return "", 0
	}
	w := strings.TrimSuffix(base, "-core-inner.sh")
	dir := mkdirRe.FindStringSubmatch(body)[1]
	f.leave(dir, "wasm-tools.version", f.wasmVer)
	f.leave(dir, "rustc.version", f.rustc)
	f.leave(dir, "guest.wasm", string(guestBytes))
	f.leave(dir, w+".component.wasm", string(compBytes))
	return "", 0
}

// star lays a tree whose justfile regenerates the worlds, and a provenance for
// each that agrees with what the fake leaves.
func star(t *testing.T, ver string, worlds ...string) atomkit.In {
	t.Helper()
	root := t.TempDir()
	var just string
	for _, w := range worlds {
		just += "regen-check:\n    scripts/regen-check.sh " + w + "\n"
		prov := fmt.Sprintf(`{
 "wit":{"commit":%q,"path":"wit/aiws-%s.wit","sha256":%q},
 "wit_result":{"commit":%q,"path":"wit/aiws-result.wit","sha256":%q},
 "tape":{"commit":%q,"path":"conformance/tapes/%s.json","sha256":%q},
 "core":{"commit":%q,"toolchain":"rustc 1.90.0 (1159e78c4 2025-09-14)"},
 "tools":{"wasm-tools":%q,"jco":"1.35.0"},
 "guest_wasm":{"sha256":%q},"component_wasm":{"sha256":%q}}`,
			rgCommit, w, checks.SHA256Hex("wit text"), rgCommit, checks.SHA256Hex("result text"),
			rgCommit, w, checks.SHA256Hex("tape text"), rgCore, ver, sum(guestBytes), sum(compBytes))
		put(t, root, "src/"+w+"core-gen/provenance.json", prov)
		put(t, root, "src/"+w+"core-gen/stamp-core.config.js", "js")
		put(t, root, "scripts/"+w+"-core-inner.sh", coreScript)
		put(t, root, "scripts/transpile-"+w+"-inner.sh", strings.ReplaceAll(transpileScript, "WORLD", w))
	}
	put(t, root, "justfile", just)
	return atomkit.In{Root: root}
}

func put(t *testing.T, root, rel, body string) {
	t.Helper()
	write(t, filepath.Join(root, rel), body)
}

// withFake points the atom at the fake and at a work directory of its own, and
// answers the fetches with a file.
func withFake(t *testing.T, in atomkit.In) (*fake, atomkit.In, *[]string) {
	t.Helper()
	f := newFake(t)
	t.Setenv("ATOM_WORK", t.TempDir())
	t.Setenv("RUSTUP_HOME", "")
	t.Setenv("CARGO_TARGET_DIR", "")
	t.Setenv("CARGO_HOME", "")
	var fetched []string
	var fm sync.Mutex
	old := fetchPinned
	fetchPinned = func(_ context.Context, _ *http.Client, sources []string, sum, dest string) error {
		fm.Lock()
		fetched = append(fetched, sources[len(sources)-1])
		fm.Unlock()
		if sum == "" {
			t.Error("a fetch without a pin")
		}
		return os.WriteFile(dest, []byte("tgz"), 0o644)
	}
	t.Cleanup(func() { fetchPinned = old })
	in.Exec = func(ctx context.Context, c atoms.Cmd) (string, int) { return f.run(ctx, c) }
	return f, in, &fetched
}

// normalized is each call as "dir: program args", the work directory and the
// tree written as $W and $R.
func normalized(f *fake, in atomkit.In) []string {
	work := os.Getenv("ATOM_WORK")
	var out []string
	for _, c := range f.calls {
		l := strings.NewReplacer(work, "$W", in.Root, "$R").Replace(f.line(c))
		d := strings.NewReplacer(work, "$W", in.Root, "$R").Replace(c.Dir)
		out = append(out, d+": "+l)
	}
	return out
}

// THE GOLDEN OF EVERY STEP ts:regen RUNS FOR ONE WORLD, in order: no shell, no
// inline script, each an argv.
func TestTSRegenRunsEachStepAsAProgramWithItsArguments(t *testing.T) {
	f, in, fetched := withFake(t, star(t, "1.250.0", "stamp"))
	v := Check(t.Context(), in)
	if v.State != 0 || v.Result != "pass" {
		t.Fatalf("%+v", v)
	}
	w := "$W/ts-regen/stamp"
	want := []string{
		"$W/ts-regen/stamp: git init --quiet " + w + "/stellar-core",
		w + "/stellar-core: git fetch --quiet --depth 1 " + checks.TSRegenWitGit + " " + rgCommit,
		w + "/stellar-core: git show " + rgCommit + ":wit/aiws-stamp.wit",
		w + "/stellar-core: git show " + rgCommit + ":wit/aiws-result.wit",
		w + "/stellar-core: git show " + rgCommit + ":conformance/tapes/stamp.json",
		w + ": git init --quiet " + w + "/stellar-core-rust",
		w + "/stellar-core-rust: git fetch --quiet --depth 1 " + checks.TSRegenCoreGit + " " + rgCore,
		w + "/stellar-core-rust: git archive --format=tar --output=" + w + "/core.tar " + rgCore,
		w + ": tar -xf " + w + "/core.tar -C " + w + "/core",
		os.TempDir() + ": cp -a /usr/local/rustup/. $W/ts-regen-tools/rustup",
		os.TempDir() + ": tar -xzf $W/ts-regen-tools/rust-std-wasm32-unknown-unknown.tar.gz -C $W/ts-regen-tools/rust-std-wasm32-unknown-unknown --strip-components=2 " + checks.RustStdWasmMember,
		os.TempDir() + ": cp -a $W/ts-regen-tools/rust-std-wasm32-unknown-unknown/lib/. $W/ts-regen-tools/rustup/toolchains/" + toolchain + "/lib",
		os.TempDir() + ": tar -xzf $W/ts-regen-tools/wasm-tools-1.250.0.tar.gz -C $W/ts-regen-tools/wasm-tools-1.250.0 --strip-components=1 wasm-tools-1.250.0-x86_64-linux/wasm-tools",
		os.TempDir() + ": $W/ts-regen-tools/wasm-tools-1.250.0/wasm-tools --version",
		w + "/core: " + w + "/stamp-core-inner.sh",
		os.TempDir() + ": tar -xzf $W/ts-regen-tools/node.tar.gz -C $W/ts-regen-tools/node --strip-components=1",
		os.TempDir() + ": $W/ts-regen-tools/node/bin/node --version",
		w + ": " + w + "/transpile-stamp-inner.sh",
		"$R: diff -r -q -x provenance.json -x .jco-version " + w + "/gen $R/src/stampcore-gen",
	}
	got := normalized(f, in)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the steps:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(*fetched) != 3 || (*fetched)[0] != checks.RustStdWasmURL || !strings.Contains((*fetched)[1], "wasm-tools-1.250.0") || (*fetched)[2] != checks.NodeRegenURL {
		t.Errorf("fetched %v", *fetched)
	}
	for _, c := range f.calls {
		if c.Name == "sh" || c.Name == "bash" || c.Name == "sed" {
			t.Errorf("a shell or an editor was exec'd: %v", c)
		}
	}
}

// THE ENVIRONMENT OF THE TWO SCRIPTS, and what is laid for them.
func TestTSRegenGivesTheScriptsTheirPinsAndTheirOwnDirectories(t *testing.T) {
	f, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
	target := t.TempDir()
	t.Setenv("CARGO_TARGET_DIR", target)
	t.Setenv("CARGO_HOME", "/cargo")
	t.Setenv("RUSTUP_HOME", "/rustup-in-image")
	t.Setenv("PATH", "/usr/bin")
	work := os.Getenv("ATOM_WORK")
	if v := Check(t.Context(), in); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	env := func(c atoms.Cmd) map[string]string {
		m := map[string]string{}
		for _, e := range c.Env {
			k, v, _ := strings.Cut(e, "=")
			m[k] = v
		}
		return m
	}
	var build, transpile, clone atoms.Cmd
	for _, c := range f.calls {
		switch {
		case strings.HasSuffix(c.Name, "stamp-core-inner.sh"):
			build = c
		case strings.HasSuffix(c.Name, "transpile-stamp-inner.sh"):
			transpile = c
		case c.Name == "git" && c.Args[0] == "fetch":
			clone = c
		}
	}
	w := work + "/ts-regen/stamp"
	b, tr := env(build), env(transpile)
	if b["WASM_TOOLS_VERSION"] != "1.250.0" || b["CARGO_TARGET_DIR"] != target+"/ts-regen-stamp" || b["RUSTUP_HOME"] != work+"/ts-regen-tools/rustup" ||
		b["PATH"] != work+"/ts-regen-tools/wasm-tools-1.250.0:/cargo/bin:/usr/bin" || build.Dir != w+"/core" {
		t.Errorf("build env %v dir %s", b, build.Dir)
	}
	if want := "--remap-path-prefix=" + w + "/core=/src --remap-path-prefix=" + target + "/ts-regen-stamp=/tmp/target --remap-path-prefix=/cargo=/usr/local/cargo --remap-path-prefix=" + work + "/ts-regen-tools/rustup=/usr/local/rustup"; b["CARGO_BUILD_RUSTFLAGS"] != want {
		t.Errorf("the guest is built as if it were where it was recorded: %q", b["CARGO_BUILD_RUSTFLAGS"])
	}
	if tr["JCO_VERSION"] != "1.35.0" || tr["NODE_EXTRA_CA_CERTS"] != "/etc/ssl/certs/ca-certificates.crt" || tr["npm_config_prefix"] != w+"/npm" ||
		tr["npm_config_cache"] != work+"/ts-regen-tools/npm-cache" || tr["PATH"] != w+"/npm/bin:"+work+"/ts-regen-tools/node/bin:/usr/bin" {
		t.Errorf("transpile env %v", tr)
	}
	if len(clone.Env) != 1 || clone.Env[0] != "GIT_TERMINAL_PROMPT=0" {
		t.Errorf("git must never prompt: %v", clone.Env)
	}
	if _, err := os.Stat(w); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the world's directory is removed: %v", err)
	}
}

// THE POD'S TRUST: the executor pod reaches the registry through the fleet cache,
// whose certificate the cache CA signs, and daedalus names that CA in the
// container's environment. The transpile (npm install -g jco) must carry the
// names through as given, not pin the system bundle over them; unset, it keeps
// the system bundle as the Dagger container has it.
func TestTSRegenTranspileCarriesTheContainersTrust(t *testing.T) {
	trust := map[string]string{
		"NODE_EXTRA_CA_CERTS": "/etc/ssl/fleet/ca.crt", "SSL_CERT_FILE": "/etc/ssl/fleet/ca.crt",
		"REQUESTS_CA_BUNDLE": "/etc/ssl/fleet/ca.crt", "PIP_CERT": "/etc/ssl/fleet/ca.crt", "UV_NATIVE_TLS": "1",
		"CARGO_HTTP_CAINFO": "/etc/ssl/fleet/ca.crt", "GIT_SSL_CAINFO": "/etc/ssl/fleet/ca.crt",
	}
	for _, tc := range []struct {
		name string
		set  map[string]string
		want map[string]string
	}{
		{"set by the pod", trust, trust},
		{"unset, the system bundle", map[string]string{}, map[string]string{"NODE_EXTRA_CA_CERTS": "/etc/ssl/certs/ca-certificates.crt"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
			for k := range trust {
				t.Setenv(k, tc.set[k])
				if tc.set[k] == "" {
					os.Unsetenv(k)
				}
			}
			if v := Check(t.Context(), in); v.State != 0 {
				t.Fatalf("%+v", v)
			}
			var transpile atoms.Cmd
			for _, c := range f.calls {
				if strings.HasSuffix(c.Name, "transpile-stamp-inner.sh") {
					transpile = c
				}
			}
			got := map[string]string{}
			for _, e := range transpile.Env {
				k, v, _ := strings.Cut(e, "=")
				got[k] = v
			}
			for k := range trust {
				if got[k] != tc.want[k] {
					t.Errorf("%s = %q, want %q (env %v)", k, got[k], tc.want[k], transpile.Env)
				}
			}
		})
	}
}

const installLine = `cargo install wasm-tools --locked --version "$WASM_TOOLS_VERSION"`

// THE SCRIPT AS LAID: the star's own, on this run's directories. With the
// release binary on PATH its compile of wasm-tools is dropped.
func TestTSRegenLaysTheStarsScriptsOnItsOwnDirectories(t *testing.T) {
	f, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
	if v := Check(t.Context(), in); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	w := os.Getenv("ATOM_WORK") + "/ts-regen/stamp"
	build, transpile := f.bodies["stamp-core-inner.sh"], f.bodies["transpile-stamp-inner.sh"]
	if strings.Contains(build, installLine) || !strings.Contains(build, "rustup target add wasm32-unknown-unknown") ||
		!strings.Contains(build, "mkdir -p "+w+"/build\n") || !strings.Contains(build, " "+w+"/build/guest.wasm\n") {
		t.Errorf("build script:\n%s", build)
	}
	if !strings.Contains(transpile, w+"/build/stamp.component.wasm --name stamp-core.config -o "+w+"/gen\n") ||
		!strings.Contains(transpile, "mkdir -p "+w+"/gen\n") {
		t.Errorf("transpile script:\n%s", transpile)
	}
}

// A wasm-tools with no pinned release is compiled by the script, untouched.
func TestTSRegenWithoutAPinnedReleaseLeavesTheScriptToCompileWasmTools(t *testing.T) {
	f, in, fetched := withFake(t, star(t, "1.249.0", "stamp"))
	f.wasmVer = "wasm-tools 1.249.0\n"
	if v := Check(t.Context(), in); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	if !strings.Contains(f.bodies["stamp-core-inner.sh"], installLine) {
		t.Errorf("the script's own install must stay:\n%s", f.bodies["stamp-core-inner.sh"])
	}
	for _, c := range f.calls {
		if c.Name == "tar" && strings.Contains(strings.Join(c.Args, " "), "wasm-tools") {
			t.Errorf("an unpinned version is not unpacked: %v", c)
		}
		if strings.HasSuffix(c.Name, "stamp-core-inner.sh") && strings.Contains(strings.Join(c.Env, " "), "wasm-tools-1.249.0") {
			t.Errorf("no release directory is put on PATH: %v", c.Env)
		}
	}
	if len(*fetched) != 2 || (*fetched)[0] != checks.RustStdWasmURL || (*fetched)[1] != checks.NodeRegenURL {
		t.Errorf("only the std and node are fetched: %v", *fetched)
	}
}

// A release that will not arrive, unpack or run is the compile, not a failure.
func TestTSRegenFallsBackToTheCompileWhenTheReleaseWillNotArrive(t *testing.T) {
	for name, fail := range map[string]string{
		"unpack": "wasm-tools-1.250.0-x86_64-linux/wasm-tools",
		"probe":  "wasm-tools-1.250.0/wasm-tools --version",
	} {
		f, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
		f.fail[fail] = "it broke"
		if v := Check(t.Context(), in); v.State != 0 {
			t.Errorf("%s: %+v", name, v)
		}
		if !strings.Contains(f.bodies["stamp-core-inner.sh"], installLine) {
			t.Errorf("%s: the script must compile what the release did not give", name)
		}
	}
	_, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
	good := fetchPinned
	fetchPinned = func(ctx context.Context, c *http.Client, sources []string, sum, dest string) error {
		if strings.Contains(sources[len(sources)-1], "wasm-tools") {
			return errors.New("github is down")
		}
		return good(ctx, c, sources, sum, dest)
	}
	if v := Check(t.Context(), in); v.State != 0 {
		t.Errorf("a download that failed is the compile too: %+v", v)
	}
}

func TestTSRegenNamesEveryPinThatMoved(t *testing.T) {
	f, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
	f.rustc = "rustc 1.91.0\n"
	put(t, in.Root, "src/stampcore-gen/provenance.json", strings.Replace(mustRead(t, in.Root, "src/stampcore-gen/provenance.json"), sum(guestBytes), strings.Repeat("a", 64), 1))
	v := Check(t.Context(), in)
	for _, want := range []string{
		"ts:regen: FINDINGS - a core does not regenerate byte-identical",
		"regen-check(stamp): FAIL: guest.wasm: got " + sum(guestBytes) + ", pinned " + strings.Repeat("a", 64),
		"regen-check(stamp): FAIL: rustc: got rustc 1.91.0, pinned rustc 1.90.0 (1159e78c4 2025-09-14)",
		"regen-check(stamp): ok  stamp.component.wasm " + sum(compBytes),
	} {
		if v.State != 1 || !strings.Contains(v.Reason+strings.Join(v.Logs, "\n"), want) {
			t.Errorf("missing %q in %+v", want, v)
		}
	}
}

func mustRead(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTSRegenFilesAGeneratedFileThatDiffers(t *testing.T) {
	f, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
	f.diffEx = 1
	f.diff = "Files " + os.Getenv("ATOM_WORK") + "/ts-regen/stamp/gen/stamp-core.config.js and " + in.Root + "/src/stampcore-gen/stamp-core.config.js differ"
	v := Check(t.Context(), in)
	text := v.Reason + strings.Join(v.Logs, "\n")
	if v.State != 1 || !strings.Contains(text, "regenerated output differs from src/stampcore-gen:") ||
		!strings.Contains(text, "Files /gen/stamp-core.config.js and /shipped/stamp-core.config.js differ") {
		t.Errorf("%+v", v)
	}
}

func TestTSRegenIsAbsentWithoutARegenCheckRecipe(t *testing.T) {
	_, in, _ := withFake(t, atomkit.In{Root: t.TempDir()})
	put(t, in.Root, "justfile", "test:\n    bun test\n")
	v := Check(t.Context(), in)
	if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "ABSENT") {
		t.Errorf("%+v", v)
	}
	if v := Check(t.Context(), atomkit.In{Root: t.TempDir()}); v.Result != "absent" {
		t.Errorf("no justfile at all is absent too: %+v", v)
	}
}

func TestTSRegenCannotRunWhenTheJustfileWillNotRead(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "justfile"), 0o755); err != nil {
		t.Fatal(err)
	}
	v := Check(t.Context(), atomkit.In{Root: root})
	if v.State != 2 || !strings.Contains(v.Reason, "ts:regen: CANNOT RUN - the justfile would not read") {
		t.Errorf("%+v", v)
	}
}

func TestTSRegenRefusesAWorldItCannotReadPinsFor(t *testing.T) {
	_, in, _ := withFake(t, atomkit.In{Root: t.TempDir()})
	put(t, in.Root, "justfile", "r:\n    scripts/regen-check.sh stamp\n")
	if v := Check(t.Context(), in); v.State != 1 || !strings.Contains(v.Reason+strings.Join(v.Logs, ""), "regen-check(stamp): FAIL: no src/stampcore-gen/provenance.json") {
		t.Errorf("%+v", v)
	}
	put(t, in.Root, "src/stampcore-gen/provenance.json", `{"wit":{}}`)
	if v := Check(t.Context(), in); v.State != 1 || !strings.Contains(v.Reason+strings.Join(v.Logs, ""), "regen-check(stamp): FAIL: provenance.json pins no wit.path") {
		t.Errorf("%+v", v)
	}
	// a provenance that cannot be read at all is the world's could-not-run
	if err := os.Remove(filepath.Join(in.Root, "src/stampcore-gen/provenance.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(in.Root, "src/stampcore-gen/provenance.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if v := Check(t.Context(), in); v.State != 2 || !strings.Contains(v.Reason+strings.Join(v.Logs, ""), "CANNOT RUN") {
		t.Errorf("%+v", v)
	}
}

func TestTSRegenRunsEveryWorldTheRecipeNames(t *testing.T) {
	in := star(t, "1.250.0", "stamp", "fade")
	if err := os.RemoveAll(filepath.Join(in.Root, "src/fadecore-gen")); err != nil {
		t.Fatal(err)
	}
	_, in, _ = withFake(t, in)
	v := Check(t.Context(), in)
	text := v.Reason + strings.Join(v.Logs, "\n")
	if v.State != 1 || !strings.Contains(text, "regen-check(stamp): ok") || !strings.Contains(text, "regen-check(fade): FAIL: no src/fadecore-gen/provenance.json") {
		t.Errorf("%+v", v)
	}
}

// Two worlds that record the same wasm-tools share ONE download, and the
// rustup home and node are made once however many worlds there are.
func TestTSRegenProvisionsOnceForManyWorlds(t *testing.T) {
	f, in, fetched := withFake(t, star(t, "1.250.0", "stamp", "stamp2"))
	// the second world builds with the same fake files
	if v := Check(t.Context(), in); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	if len(*fetched) != 3 {
		t.Errorf("fetched %v; one std, one wasm-tools and one node", *fetched)
	}
	var copies, nodes int
	for _, c := range f.calls {
		if c.Name == "cp" && strings.HasSuffix(c.Args[2], "/rustup") {
			copies++
		}
		if strings.HasSuffix(c.Name, "node") {
			nodes++
		}
	}
	if copies != 1 || nodes != 1 {
		t.Errorf("rustup copied %d times, node probed %d", copies, nodes)
	}
}

// A script written a moment ago can be busy in a sibling's fork; that, and only
// that, is tried again.
func TestTSRegenRetriesAScriptTheKernelCalledBusy(t *testing.T) {
	f, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
	f.busy = 2
	if v := Check(t.Context(), in); v.State != 0 {
		t.Errorf("%+v", v)
	}
	f, in, _ = withFake(t, star(t, "1.250.0", "stamp"))
	f.busy = 99
	if v := Check(t.Context(), in); v.State != 2 {
		t.Errorf("a script that stays busy is a could-not-run: %+v", v)
	}
	f, in, _ = withFake(t, star(t, "1.250.0", "stamp"))
	f.busy = 99
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if v := Check(ctx, in); v.State != 2 {
		t.Errorf("a cancelled run does not wait: %+v", v)
	}
}

// EVERY STEP THAT CAN FAIL IS A COULD-NOT-RUN, never a finding about the tree.
func TestTSRegenCannotRunWhenAStepWillNotRun(t *testing.T) {
	for _, c := range []struct{ name, match, want string }{
		{"stellar-core init", "git init --quiet", "git init"},
		{"stellar-core fetch", "git fetch --quiet --depth 1 " + checks.TSRegenWitGit, "wit/aiws-stamp.wit@" + rgCommit},
		{"a pin", "git show " + rgCommit + ":conformance", "conformance/tapes/stamp.json@" + rgCommit},
		{"core fetch", "git fetch --quiet --depth 1 " + checks.TSRegenCoreGit, "core @" + rgCore},
		{"core archive", "git archive", "core @" + rgCore},
		{"core unpack", "tar -xf", "core @" + rgCore},
		{"rustup", "cp -a", "rustup home"},
		{"std unpack", "rust-std-wasm32-unknown-unknown.tar.gz", "rustup home"},
		{"std copy", "wasm32-unknown-unknown/lib/.", "rustup home"},
		{"build", "stamp-core-inner.sh", "the build of stamp"},
		{"node", "node --version", "node 24.21.0"},
		{"transpile", "transpile-stamp-inner.sh", "the transpile of stamp"},
	} {
		f, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
		f.fail[c.match] = "it broke"
		v := Check(t.Context(), in)
		if v.State != 2 || !strings.Contains(v.Reason, "CANNOT RUN") || !strings.Contains(v.Reason, c.want) {
			t.Errorf("%s: %+v", c.name, v)
		}
	}
}

func TestTSRegenCannotRunWhenAScriptIsNotInTheTree(t *testing.T) {
	for _, name := range []string{"stamp-core-inner.sh", "transpile-stamp-inner.sh"} {
		_, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
		if err := os.Remove(filepath.Join(in.Root, "scripts", name)); err != nil {
			t.Fatal(err)
		}
		if v := Check(t.Context(), in); v.State != 2 || !strings.Contains(v.Reason, "scripts/"+name) {
			t.Errorf("%s: %+v", name, v)
		}
	}
}

func TestTSRegenReportsEveryOutputTheBuildLeftOut(t *testing.T) {
	f, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
	f.omit = map[string]bool{"wasm-tools.version": true, "rustc.version": true, "guest.wasm": true}
	v := Check(t.Context(), in)
	if v.State != 2 || !strings.Contains(v.Reason, "the build's wasm-tools.version") || !strings.Contains(v.Reason, "the build's rustc.version") ||
		!strings.Contains(v.Reason, "the build's guest.wasm") {
		t.Errorf("%+v", v)
	}
}

func TestTSRegenCannotRunWhenTheTranspileLeavesNoJcoVersion(t *testing.T) {
	f, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
	f.omit = map[string]bool{".jco-version": true}
	if v := Check(t.Context(), in); v.State != 2 || !strings.Contains(v.Reason, "the transpile's .jco-version") {
		t.Errorf("%+v", v)
	}
}

func TestTSRegenCannotRunWhenDiffWillNotStart(t *testing.T) {
	f, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
	f.fail["diff -r"], f.exit["diff -r"] = "executable file not found", -1
	if v := Check(t.Context(), in); v.State != 2 || !strings.Contains(v.Reason, "diff: executable file not found") {
		t.Errorf("%+v", v)
	}
}

func TestTSRegenHoldsTheCoresOwnCopyOfTheWitToThePin(t *testing.T) {
	f, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
	f.coreWit = "a drifted stamp wit"
	v := Check(t.Context(), in)
	text := v.Reason + strings.Join(v.Logs, "\n")
	if v.State != 1 || !strings.Contains(text, "FAIL: core wit/aiws-stamp.wit (guest's own copy): got "+checks.SHA256Hex("a drifted stamp wit")) ||
		!strings.Contains(text, "ok  wit wit/aiws-stamp.wit@"+rgCommit) {
		t.Errorf("%+v", v)
	}
}

func TestTSRegenCannotRunWhenTheCoreCarriesNoCopyOfTheWit(t *testing.T) {
	f, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
	f.coreWit = "-"
	if v := Check(t.Context(), in); v.State != 2 || !strings.Contains(v.Reason, "core wit/aiws-stamp.wit@"+rgCore) {
		t.Errorf("%+v", v)
	}
}

func TestTailKeepsTheLastLinesOfAProgramsAnswer(t *testing.T) {
	var lines []string
	for i := range 10 {
		lines = append(lines, fmt.Sprint("line", i))
	}
	if got := tail(strings.Join(lines, "\n") + "\n"); got != "line4 | line5 | line6 | line7 | line8 | line9" {
		t.Errorf("%q", got)
	}
	if got := tail("one"); got != "one" {
		t.Errorf("%q", got)
	}
}

func TestPathWithPutsTheNamedDirectoriesFirstAndSkipsTheEmpty(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	if got := pathWith("", "/a", "", "/b"); got != "/a:/b:/usr/bin" {
		t.Errorf("%q", got)
	}
	t.Setenv("CARGO_HOME", "")
	if cargoBin() != "" {
		t.Error("no CARGO_HOME, no cargo bin")
	}
}

func TestTheTargetIsThePodsOrTheAtomsOwn(t *testing.T) {
	t.Setenv("ATOM_WORK", "/work/x")
	t.Setenv("CARGO_TARGET_DIR", "")
	w := &world{name: "w", p: newProvision(atomkit.In{})}
	if got := w.target(); got != "/work/x/ts-regen-tools/target/ts-regen-w" {
		t.Errorf("%q", got)
	}
}

func TestWrapNamesWhatWasRead(t *testing.T) {
	if wrap("x", nil) != nil || wrap("the build's x", errors.New("gone")).Error() != "the build's x: gone" {
		t.Error("wrap")
	}
}

// A work directory that cannot be made is a could-not-run, wherever it is first needed.
func TestTSRegenCannotRunWhenItsDirectoriesCannotBeMade(t *testing.T) {
	for name, block := range map[string]string{
		"world":  "ts-regen",
		"core":   "ts-regen/stamp/core",
		"rustup": "ts-regen-tools/rustup",
		"std":    "ts-regen-tools/rust-std-wasm32-unknown-unknown",
		"node":   "ts-regen-tools/node",
	} {
		_, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
		work := os.Getenv("ATOM_WORK")
		if name == "core" {
			g := in.Exec
			in.Exec = func(ctx context.Context, c atoms.Cmd) (string, int) {
				if c.Name == "git" && c.Args[0] == "show" && strings.HasSuffix(c.Args[1], "tapes/stamp.json") {
					put(t, work, block, "in the way")
				}
				return g(ctx, c)
			}
		} else {
			put(t, work, block, "in the way")
		}
		if v := Check(t.Context(), in); v.State != 2 || !strings.Contains(v.Reason, "CANNOT RUN") {
			t.Errorf("%s: %+v", name, v)
		}
	}
}

func TestTSRegenCannotRunWhenNodeWillNotArrive(t *testing.T) {
	_, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
	good := fetchPinned
	fetchPinned = func(ctx context.Context, c *http.Client, sources []string, sum, dest string) error {
		if strings.Contains(sources[0], "nodejs") || strings.Contains(sources[len(sources)-1], "nodejs") {
			return errors.New("nodejs.org is down")
		}
		return good(ctx, c, sources, sum, dest)
	}
	if v := Check(t.Context(), in); v.State != 2 || !strings.Contains(v.Reason, "node 24.21.0: nodejs.org is down") {
		t.Errorf("%+v", v)
	}
}

func TestTSRegenFallsBackToTheCompileWhenTheReleaseDirectoryCannotBeMade(t *testing.T) {
	f, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
	put(t, os.Getenv("ATOM_WORK"), "ts-regen-tools/wasm-tools-1.250.0", "in the way")
	if v := Check(t.Context(), in); v.State != 0 {
		t.Errorf("%+v", v)
	}
	if !strings.Contains(f.bodies["stamp-core-inner.sh"], installLine) {
		t.Error("the script must compile what could not be placed")
	}
}

func TestAnAssetWithNoPinIsNeverFetched(t *testing.T) {
	_, in, _ := withFake(t, atomkit.In{})
	if err := newProvision(in).fetch(t.Context(), "https://example.com/unpinned.tar.gz", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("an asset nothing vouches for must not be fetched")
	}
}

// THE STD IS REGISTERED, not only laid: the star's own `rustup target add` must
// find the component installed, because a pod has no network to fetch it.
func TestTSRegenRegistersTheStdWithRustup(t *testing.T) {
	_, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
	if v := Check(t.Context(), in); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	rl := filepath.Join(os.Getenv("ATOM_WORK"), "ts-regen-tools", "rustup", "toolchains", toolchain, "lib", "rustlib")
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(rl, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := read("manifest-" + checks.RustStdWasmComponent); got != stdManifest {
		t.Errorf("manifest %q", got)
	}
	if got := read("components"); got != "cargo-x\nrust-std-x\nrustc-x\n"+checks.RustStdWasmComponent+"\n" {
		t.Errorf("components %q", got)
	}
	want := "config_version = \"1\"\n\n[[components]]\npkg = \"rust-std\"\ntarget = \"wasm32-unknown-unknown\"\nis_extension = false\n"
	if got := read("multirust-config.toml"); got != want {
		t.Errorf("config %q", got)
	}
}

func TestTSRegenCannotRunWhenTheStdWillNotRegister(t *testing.T) {
	for _, c := range []struct{ name, omit, want string }{
		{"no toolchain", "toolchain", "rustup home: open "},
		{"no component list", "components", "rustup home: chmod "},
		{"no rustup config", "multirust-config.toml", "rustup home: chmod "},
		{"no manifest", "manifest.in", "rustup home: open "},
		{"no rustlib", "rustlib", "rustup home: open "},
		{"a component list that is a directory", "components-dir", "rustup home: open "},
	} {
		f, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
		f.omit[c.omit] = true
		if v := Check(t.Context(), in); v.State != 2 || !strings.Contains(v.Reason, c.want) {
			t.Errorf("%s: %+v", c.name, v)
		}
	}
}

func TestTSRegenCannotRunWhenTheStdWillNotArrive(t *testing.T) {
	_, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
	good := fetchPinned
	fetchPinned = func(ctx context.Context, c *http.Client, sources []string, sum, dest string) error {
		if strings.Contains(sources[len(sources)-1], "rust-std") {
			return errors.New("static.rust-lang.org is down")
		}
		return good(ctx, c, sources, sum, dest)
	}
	if v := Check(t.Context(), in); v.State != 2 || !strings.Contains(v.Reason, "rust-std 1.90.0: static.rust-lang.org is down") {
		t.Errorf("%+v", v)
	}
}

func TestTheToolchainIsTheOneTheHomeHolds(t *testing.T) {
	home := t.TempDir()
	if _, err := toolchainOf(home); err == nil {
		t.Error("a home with no toolchains directory")
	}
	put(t, home, "toolchains/a/x", "")
	if got, err := toolchainOf(home); err != nil || got != filepath.Join(home, "toolchains", "a") {
		t.Errorf("%q %v", got, err)
	}
	put(t, home, "toolchains/b/x", "")
	if _, err := toolchainOf(home); err == nil || !strings.Contains(err.Error(), "2 toolchains") {
		t.Errorf("two toolchains: %v", err)
	}
}
