package regenlane

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/regenkit"
)

// gitEnv keeps git from asking a person for a credential nobody holds: both
// repositories are read without one.
var gitEnv = regenkit.GitEnv

// pins reads the WIT, the result WIT and the tape at their recorded commits of
// stellar-core, and answers each with the pin it is held to. One shallow fetch
// per distinct commit; a file is read with `git show`, as regen-check.sh does.
func (w *world) pins() ([]checks.RegenProbe, error) {
	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return nil, err
	}
	repo := filepath.Join(w.dir, "stellar-core")
	if _, err := w.do(w.dir, nil, checks.RegenGitInit(repo), false); err != nil {
		return nil, err
	}
	var fetched []string
	for _, lp := range w.prov.Pins() {
		if slices.Contains(fetched, lp.Pin.Commit) {
			continue
		}
		fetched = append(fetched, lp.Pin.Commit)
		if _, err := w.do(repo, gitEnv, checks.RegenGitFetch(checks.TSRegenWitGit, lp.Pin.Commit), false); err != nil {
			return nil, fmt.Errorf("%s@%s: %v", lp.Pin.Path, lp.Pin.Commit, err)
		}
	}
	var probes []checks.RegenProbe
	for _, lp := range w.prov.Pins() {
		pin := lp.Pin
		text, err := w.do(repo, nil, checks.RegenGitShow(pin.Commit, pin.Path), true)
		if err != nil {
			return nil, fmt.Errorf("%s@%s: %v", pin.Path, pin.Commit, err)
		}
		probes = append(probes, checks.RegenProbe{
			Label: fmt.Sprintf("%s %s@%s", lp.Label, pin.Path, pin.Commit), Got: checks.SHA256Hex(text), Pinned: pin.SHA256})
	}
	return probes, nil
}

// core unpacks stellar-core-rust at its recorded commit and answers where.
func (w *world) core() (string, error) {
	return regenkit.UnpackCore(func(dir string, env, argv []string) (string, error) { return w.do(dir, env, argv, false) }, w.dir, w.prov.Core.Commit)
}

// built is what the star's build script left: the module and component hashed
// where they lie (a binary is measured by its bytes), and the two version files.
type built struct {
	out, guest, component, wasmTools, rustc string
}

// target is this world's cargo target directory. The pod gives the run one
// (CARGO_TARGET_DIR); two worlds build different features into the same
// artifact name, so each keeps a directory of its own beneath it.
func (w *world) target() string {
	base := os.Getenv("CARGO_TARGET_DIR")
	if base == "" {
		base = filepath.Join(w.p.work, "target")
	}
	return filepath.Join(base, "ts-regen-"+w.name)
}

// script lays a star's inner script on this world's directories and writes it
// where it can be exec'd.
func (w *world) script(name, out, in string, dropInstall bool) (string, error) {
	body, err := os.ReadFile(filepath.Join(w.in.Root, "scripts", name))
	if err != nil {
		return "", fmt.Errorf("scripts/%s: %v", name, err)
	}
	path := filepath.Join(w.dir, name)
	return path, os.WriteFile(path, []byte(checks.RegenScript(string(body), out, in, dropInstall)), 0o755)
}

// build runs the star's build script over the core, under the pinned wasm-tools
// and the toolchain's rustup, and measures what it leaves.
func (w *world) build(core string) (built, error) {
	rustup, err := w.p.rustupHome(w.ctx, w.exec)
	if err != nil {
		return built{}, err
	}
	wasmDir := w.p.wasmTools(w.ctx, w.exec, w.prov.Tools.WasmTools)
	b := built{out: filepath.Join(w.dir, "build")}
	script, err := w.script(w.name+"-core-inner.sh", b.out, filepath.Join(w.dir, "in"), wasmDir != "")
	if err != nil {
		return b, err
	}
	env := []string{
		"WASM_TOOLS_VERSION=" + w.prov.Tools.WasmTools,
		"CARGO_TARGET_DIR=" + w.target(),
		"RUSTUP_HOME=" + rustup,
		"CARGO_BUILD_RUSTFLAGS=" + checks.RegenRemapFlags(core, w.target(), os.Getenv("CARGO_HOME"), rustup),
		"PATH=" + pathWith(wasmDir, cargoBin()),
	}
	if _, err := w.do(core, env, []string{script}, false); err != nil {
		return b, fmt.Errorf("the build of %s: %v", w.name, err)
	}
	var errs error
	read := func(name string) string {
		s, err := os.ReadFile(filepath.Join(b.out, name))
		errs = errors.Join(errs, wrap("the build's "+name, err))
		return string(s)
	}
	b.wasmTools, b.rustc = read("wasm-tools.version"), read("rustc.version")
	for dst, name := range map[*string]string{&b.guest: "guest.wasm", &b.component: w.name + ".component.wasm"} {
		sum, err := checks.SHA256File(filepath.Join(b.out, name))
		errs = errors.Join(errs, wrap("the build's "+name, err))
		*dst = sum
	}
	return b, errs
}

// transpile runs the star's transpile script over the built component, under
// the pinned node and the pinned jco, and answers the jco version it recorded.
func (w *world) transpile(b built) (string, error) {
	node, err := w.p.nodeDir(w.ctx, w.exec)
	if err != nil {
		return "", err
	}
	gen := filepath.Join(w.dir, "gen")
	script, err := w.script("transpile-"+w.name+"-inner.sh", gen, b.out, false)
	if err != nil {
		return "", err
	}
	prefix := filepath.Join(w.dir, "npm")
	env := append(trustEnv(),
		"JCO_VERSION="+w.prov.Tools.JCO,
		"npm_config_prefix="+prefix,
		"npm_config_cache="+filepath.Join(w.p.work, "npm-cache"),
		"PATH="+pathWith(filepath.Join(prefix, "bin"), filepath.Join(node, "bin")))
	if _, err := w.do(w.dir, env, []string{script}, false); err != nil {
		return "", fmt.Errorf("the transpile of %s: %v", w.name, err)
	}
	jco, err := os.ReadFile(filepath.Join(gen, ".jco-version"))
	return string(jco), wrap("the transpile's .jco-version", err)
}

// trustVars name the CA bundle each client reads. The pod's environment sets
// them to the CA that signs the fleet cache's certificate (the registry is only
// reachable through it); the Dagger container sets the system bundle.
var trustVars = []string{
	"NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "PIP_CERT",
	"UV_NATIVE_TLS", "CARGO_HTTP_CAINFO", "GIT_SSL_CAINFO",
}

// systemCABundle is what node trusts when the environment names none.
const systemCABundle = "/etc/ssl/certs/ca-certificates.crt"

// trustEnv hands a program the trust the container was given, as given, rather
// than a path of this code's choosing. node alone falls back to the system
// bundle, since the transpile is the one step that installs from the registry.
func trustEnv() []string {
	var env []string
	for _, k := range trustVars {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		} else if k == "NODE_EXTRA_CA_CERTS" {
			env = append(env, k+"="+systemCABundle)
		}
	}
	return env
}

// wrap names what a read was for.
func wrap(what string, err error) error { return regenkit.Wrap(what, err) }

// cargoBin is where `cargo install` leaves a program, when CARGO_HOME is named.
func cargoBin() string { return regenkit.CargoBin() }

// pathWith is PATH with these directories in front (the empty ones skipped).
func pathWith(dirs ...string) string { return regenkit.PathWith(dirs...) }
