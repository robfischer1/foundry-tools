package goregen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/regenkit"
)

const id = "dev:go-regen"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: "dev", Lane: checks.LaneGo}

// Seams a test replaces: where a program is looked for, and the checksummed
// download.
var (
	lookPath                       = exec.LookPath
	fetchPinned regenkit.FetchFunc = checks.FetchPinned
)

// usage is the argument syntax, in every refusal that is about arguments.
const usage = "usage: <core>... | --new <core> --rev <tag-or-commit> --world <aiws:pkg/world@ver> [...]"

// defaultCargoHome is where a new core's guest is built as far as its bytes are
// concerned: the rust image's cargo home, the one classescore's record carries.
const defaultCargoHome = "/usr/local/cargo"

// workstationCargoHome is the other cargo home the recorded guests were built
// under: the workstation's (rob02), where build-guest.sh ran on a person's
// machine. A guest records its cargo home in its bytes only when a dependency's
// panic location survives into them; one that does not still depends on it
// (MEASURED: alarm's recorded guest names no registry path, and differs from a
// build under /usr/local/cargo in nothing but the ThinLTO module hashes that
// rustc appends to local symbols, `.llvm.<hash>`; remapped to this home it is
// byte-identical to the record).
const workstationCargoHome = "/home/rob/.cargo"

// Check regenerates every core named and writes the result into the tree.
func Check(ctx context.Context, in atomkit.In) checks.Verdict {
	req, err := ParseArgs(in.Args)
	if err != nil {
		return checks.VerdictOf(row, 2, fmt.Sprintf("%s: CANNOT RUN - the arguments do not read: %v (%s)", id, err, usage))
	}
	if len(req.Existing)+len(req.New) == 0 {
		return checks.VerdictOf(row, 2, fmt.Sprintf("%s: CANNOT RUN - no core named (%s); the cores that can be regenerated here: %s",
			id, usage, strings.Join(regenerable(in.Root), ", ")))
	}
	r := &runner{ctx: ctx, in: in, work: filepath.Join(atoms.WorkDir(), "go-regen"), sources: map[string]string{}, chains: map[string]toolchain{}, laid: map[string]toolchain{}, wasm: map[string]string{}}
	defer os.RemoveAll(r.work)
	var results []outcome
	for _, core := range req.Existing {
		results = append(results, r.existing(core))
	}
	for _, nc := range req.New {
		results = append(results, r.bootstrap(nc))
	}
	v := fold(results)
	// the lines are the run's report: the engine and the pod keep an atom's stderr
	fmt.Fprintln(os.Stderr, v.Reason)
	return v
}

// outcome is one core's run: the line it answers with, or the step it failed.
type outcome struct {
	core string
	line string
	err  *failure
}

// failure is a core that did not regenerate: the step, why, and whether it is a
// finding about the tree (1) or the atom could not look (2).
type failure struct {
	state int
	step  string
	msg   string
}

func finding(step, format string, a ...any) *failure {
	return &failure{1, step, fmt.Sprintf(format, a...)}
}

func cannot(step, format string, a ...any) *failure {
	return &failure{2, step, fmt.Sprintf(format, a...)}
}

// fold settles the run on its worst core: could-not-run beats a finding beats a
// pass, and a pass carries one line per core.
func fold(results []outcome) checks.Verdict {
	state := 0
	lines := make([]string, 0, len(results))
	for _, o := range results {
		if o.err == nil {
			lines = append(lines, o.line)
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: FAILED at %s - %s", o.core, o.err.step, o.err.msg))
		state = max(state, o.err.state)
	}
	v := checks.VerdictOf(row, state, strings.Join(lines, "\n"))
	if state == 0 {
		v.Reason = fmt.Sprintf("%s: PASS - %d core(s) regenerated\n%s", id, len(results), strings.Join(lines, "\n"))
	}
	return v
}

// regenerable lists the cores a tree can regenerate: a directory with a
// provenance.json that names gravity as its generator and a build-guest.sh.
func regenerable(root string) []string {
	var cores []string
	ents, _ := os.ReadDir(root) // an unreadable root lists none
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		text, err := os.ReadFile(filepath.Join(root, e.Name(), "provenance.json"))
		if err != nil {
			continue
		}
		p, err := ParseProv(string(text))
		if _, serr := os.Stat(filepath.Join(root, e.Name(), "build-guest.sh")); err != nil || serr != nil || !strings.HasPrefix(p.GenCommand, "gravity ") {
			continue
		}
		cores = append(cores, e.Name())
	}
	sort.Strings(cores)
	return cores
}

// runner is the state of one run: the work directory, the sources fetched and
// the compilers laid, each once.
type runner struct {
	ctx  context.Context
	in   atomkit.In
	work string
	// sources are the stellar-core-rust trees unpacked, by commit.
	sources map[string]string
	// chains are the compilers selected, by the rustc line they print; laid are
	// the ones installed, by version.
	chains map[string]toolchain
	laid   map[string]toolchain
	// wasm are the wasm-tools releases laid, by version.
	wasm map[string]string
	// native is the pod's rustc line, read once; wasmTarget says its wasm32
	// standard library has been checked for.
	native     string
	nativeErr  *failure
	wasmTarget bool
}

// run runs a program: its output, or an error naming it, its exit and what it said.
func (r *runner) run(dir string, env []string, argv ...string) (string, error) {
	out, code := r.in.Run(r.ctx, atoms.Cmd{Dir: dir, Name: argv[0], Args: argv[1:], Env: env})
	if code == 0 {
		return out, nil
	}
	return out, fmt.Errorf("%s exited %d: %s", strings.Join(argv, " "), code, regenkit.Tail(out))
}

// exec is the run's program runner as regenkit's ExecFunc.
func (r *runner) exec(c atoms.Cmd) (string, int) { return r.in.Run(r.ctx, c) }

// runner adapts run to regenkit's Runner.
func (r *runner) runner() regenkit.Runner {
	return func(dir string, env, argv []string) (string, error) { return r.run(dir, env, argv...) }
}

// toolchain is the compiler a core is built with: the directory of its binaries
// (put first on PATH; "" is the pod's own) and the line `rustc --version` prints.
type toolchain struct {
	dir, line, how string
}

// bin is a program of the toolchain: by path when laid, by name when the pod's.
func (t toolchain) bin(name string) string {
	if t.dir == "" {
		return name
	}
	return filepath.Join(t.dir, name)
}

// existing regenerates one core that has a provenance.json and a build-guest.sh.
func (r *runner) existing(core string) outcome {
	o := outcome{core: core}
	line, err := r.regenerate(core)
	o.line = line
	if err != nil {
		o.err = err
	}
	return o
}

func (r *runner) regenerate(core string) (string, *failure) {
	dir := filepath.Join(r.in.Root, core)
	provText, err := os.ReadFile(filepath.Join(dir, "provenance.json"))
	if err != nil {
		return "", cannot("read", "%s/provenance.json would not read (%v); the cores that can be regenerated here: %s", core, err, strings.Join(regenerable(r.in.Root), ", "))
	}
	guestText, err := os.ReadFile(filepath.Join(dir, "build-guest.sh"))
	if err != nil {
		return "", cannot("read", "%s/build-guest.sh would not read (%v); the cores that can be regenerated here: %s", core, err, strings.Join(regenerable(r.in.Root), ", "))
	}
	prov, err := ParseProv(string(provText))
	if err != nil {
		return "", finding("read", "%s/%v", core, err)
	}
	bg, err := ParseBuildGuest(string(guestText))
	if err != nil {
		return "", finding("read", "%s/%v", core, err)
	}
	if bg.Rev != prov.CoreCommit {
		return "", finding("read", "provenance.json core.commit is %s but build-guest.sh builds REV=%s", prov.CoreCommit, bg.Rev)
	}
	if f := agree(prov, bg); f != nil {
		return "", f
	}
	chain, f := r.toolchain(prov.CoreToolchain)
	if f != nil {
		return "", f
	}
	src, f := r.source(prov.CoreCommit)
	if f != nil {
		return "", f
	}
	var wasmDir string
	if bg.Compose != nil {
		if wasmDir, f = r.wasmTools(recorded(prov.CoreToolchain, "wasm-tools")); f != nil {
			return "", f
		}
	}
	oldGuest, _ := os.ReadFile(filepath.Join(dir, prov.Guest.File)) // a core with no guest yet has no recorded home
	visible, err := RecordedCargoHome(oldGuest)
	if err != nil {
		return "", finding("build", "%v", err)
	}
	guest, home, f := r.guest(core, bg, src, chain, visible, wasmDir, prov.Guest.SHA256)
	if f != nil {
		return "", f
	}
	gen, f := r.generate(core, dir, prov.GenCommit, prov.GenCommand, guest)
	if f != nil {
		return "", f
	}
	return r.settle(core, dir, string(provText), prov, guest, gen, chain, wasmDir, home)
}

// agree holds provenance.json's prose to build-guest.sh's commands: the script
// is what runs, and a record that says otherwise is a record of another build.
func agree(prov Prov, bg BuildGuest) *failure {
	if bg.Compose != nil {
		want := "tools/compose/compose.sh OUT.wasm " + strings.Join(bg.Compose, " ") + " ("
		if !strings.HasPrefix(prov.CoreBuild, want) {
			return finding("read", "core.build is %q but build-guest.sh runs tools/compose/compose.sh over %s", prov.CoreBuild, strings.Join(bg.Compose, " "))
		}
		return nil
	}
	recorded, _, _ := strings.Cut(prov.CoreBuild, " (")
	if !slices.Equal(strings.Fields(recorded), bg.Cargo) {
		return finding("read", "core.build is %q but build-guest.sh runs `%s`", prov.CoreBuild, strings.Join(bg.Cargo, " "))
	}
	return nil
}

// nativeRustc is the pod's compiler line, read once.
func (r *runner) nativeRustc() (string, *failure) {
	if r.native == "" && r.nativeErr == nil {
		out, err := r.run(r.in.Root, nil, "rustc", "--version")
		if err != nil {
			r.nativeErr = cannot("toolchain", "the container's rustc would not answer: %v", err)
		}
		r.native = strings.TrimSpace(out)
	}
	return r.native, r.nativeErr
}

// toolchain selects the compiler a record pins: the container's own when its
// `rustc --version` is the pinned line, else the one laid from the dist
// tarballs pinned for that version. A version with no pin is CANNOT RUN naming
// both; there is no nearest compiler, and the line printed after selection is
// held to the record's.
func (r *runner) toolchain(record string) (toolchain, *failure) {
	line, version, ok := checks.RegenRustLine(record)
	if !ok {
		return toolchain{}, finding("toolchain", "core.toolchain %q records no `rustc X.Y.Z (hash date)`", record)
	}
	if t, ok := r.chains[line]; ok {
		return t, nil
	}
	native, f := r.nativeRustc()
	if f != nil {
		return toolchain{}, f
	}
	if native == line {
		if f := r.wasmStd(); f != nil {
			return toolchain{}, f
		}
		t := toolchain{line: line, how: "container's"}
		r.chains[line] = t
		return t, nil
	}
	_, nativeVersion, _ := checks.RegenRustLine(native)
	t, f := r.lay(version, line, nativeVersion)
	if f != nil {
		return toolchain{}, f
	}
	r.chains[line] = t
	return t, nil
}

// wasmStd checks the container's compiler has the wasm32 standard library, once:
// the executor lays it into the rustup home, and anywhere else rustup adds it.
func (r *runner) wasmStd() *failure {
	if r.wasmTarget {
		return nil
	}
	if v := atoms.WitProvision(r.ctx, r.in, row, checks.WitGuestTarget); v != nil {
		return cannot("toolchain", "%s", v.Reason)
	}
	r.wasmTarget = true
	return nil
}

// lay installs a rust toolchain from its pinned dist tarballs under the work
// directory: each is fetched mirror-first and held to its sum, unpacked, and
// installed by its own install.sh into one prefix. It is laid once per version.
// want is the line it must print ("" for a toolchain chosen by version alone).
func (r *runner) lay(version, want, nativeVersion string) (toolchain, *failure) {
	if t, ok := r.laid[version]; ok {
		return t, held(t, want)
	}
	dist, err := checks.RegenRustDist(version, nativeVersion)
	if err != nil {
		return toolchain{}, cannot("toolchain", "%v", err)
	}
	prefix, unpack := filepath.Join(r.work, "rust-"+version), filepath.Join(r.work, "dist-"+version)
	for _, u := range dist.URLs {
		name := strings.TrimSuffix(filepath.Base(u), ".tar.gz")
		tgz := filepath.Join(unpack, name+".tar.gz")
		if err := regenkit.Fetch(r.ctx, fetchPinned, u, tgz); err != nil {
			return toolchain{}, cannot("toolchain", "rust %s: %v", version, err)
		}
		for _, argv := range [][]string{
			checks.RegenTar(tgz, unpack, true, 0, ""),
			{"sh", filepath.Join(unpack, name, "install.sh"), "--prefix=" + prefix, "--disable-ldconfig"},
		} {
			if err := regenkit.Step(r.exec, nil, argv...); err != nil {
				return toolchain{}, cannot("toolchain", "rust %s: %s: %v", version, name, err)
			}
		}
		_ = os.RemoveAll(filepath.Join(unpack, name))
		_ = os.Remove(tgz)
	}
	t := toolchain{dir: filepath.Join(prefix, "bin"), how: "laid"}
	out, err := r.run(r.in.Root, nil, t.bin("rustc"), "--version")
	got := strings.TrimSpace(out)
	if err != nil {
		return toolchain{}, cannot("toolchain", "the laid rustc %s would not answer: %v", version, err)
	}
	t.line = got
	if f := held(t, want); f != nil {
		return toolchain{}, f
	}
	r.laid[version] = t
	return t, nil
}

// held checks a laid toolchain prints the line a record pins ("" accepts any
// line of the version it was laid for).
func held(t toolchain, want string) *failure {
	if want != "" && t.line != want {
		return cannot("toolchain", "provenance.json pins %q but the rustc laid for it prints %q", want, t.line)
	}
	return nil
}

// source unpacks stellar-core-rust at a commit, once.
func (r *runner) source(commit string) (string, *failure) {
	if dir, ok := r.sources[commit]; ok {
		return dir, nil
	}
	dir, err := regenkit.UnpackCore(r.runner(), filepath.Join(r.work, "core-"+commit[:min(12, len(commit))]), commit)
	if err != nil {
		return "", cannot("fetch", "stellar-core-rust: %v", err)
	}
	r.sources[commit] = dir
	return dir, nil
}

// guest builds the core's guest under the cargo home its record implies, and
// answers where it lies and the home it was built under ("" is the container's
// own). A home the recorded guest shows in its bytes is the only candidate. Else
// the guest is built in the container's own home and, when that is not the
// recorded guest, again under the workstation's, and the build that is the
// record is kept; when neither is, the first is kept and the line says it
// differs.
func (r *runner) guest(core string, bg BuildGuest, src string, chain toolchain, visible, wasmDir, want string) ([]byte, string, *failure) {
	homes := []string{visible}
	if visible == "" {
		homes = []string{"", workstationCargoHome}
	}
	var first []byte
	for i, home := range homes {
		body, f := r.build(core, bg, src, chain, home, wasmDir)
		if f != nil {
			return nil, "", f
		}
		if i == 0 {
			first = body
		}
		if sum(body) == want || len(homes) == 1 {
			return body, home, nil
		}
	}
	return first, "", nil
}

// homeNote words a cargo home that is not the container's own.
func homeNote(home string) string {
	if home == "" || home == cargoHome() {
		return ""
	}
	return "; built under cargo home " + home
}

// buildEnv is the environment of the guest's build: bounded jobs, a target
// directory of the core's own, the compiler first on PATH, and, when the
// recorded guest was built under another cargo home, the remap that puts the
// dependencies' source paths (which enter the wasm in panic locations) where the
// record has them.
func (r *runner) buildEnv(chain toolchain, target, recordedHome, wasmDir string) []string {
	env := append(checks.CargoBoundedEnv(), "CARGO_TARGET_DIR="+target)
	if chain.dir != "" || wasmDir != "" {
		env = append(env, "PATH="+regenkit.PathWith(wasmDir, chain.dir))
	}
	if home := cargoHome(); recordedHome != "" && recordedHome != home {
		env = append(env, "CARGO_ENCODED_RUSTFLAGS=--remap-path-prefix="+home+"="+recordedHome)
	}
	return env
}

// cargoHome is this run's cargo home.
func cargoHome() string {
	if h := os.Getenv("CARGO_HOME"); h != "" {
		return h
	}
	return filepath.Join(os.Getenv("HOME"), ".cargo")
}

// build produces the core's guest the way its build-guest.sh does, and answers
// where it lies: tools/compose/compose.sh OUT <worlds> from the revision's own
// tree for a composed core, the recorded cargo argv (no shell) and the artifact
// carried out of the target for a plain one.
func (r *runner) build(core string, bg BuildGuest, src string, chain toolchain, recordedHome, wasmDir string) ([]byte, *failure) {
	dir := filepath.Join(r.work, core)
	target := filepath.Join(dir, "target")
	defer os.RemoveAll(target)
	guest := filepath.Join(dir, "guest.wasm")
	if err := errors.Join(os.MkdirAll(dir, 0o755), atoms.ClearOutput(guest)); err != nil {
		return nil, cannot("build", "%v", err)
	}
	env := r.buildEnv(chain, target, recordedHome, wasmDir)
	if bg.Compose != nil {
		argv := append([]string{filepath.Join(src, "tools", "compose", "compose.sh"), guest}, bg.Compose...)
		if _, err := r.run(src, env, argv...); err != nil {
			return nil, cannot("build", "tools/compose/compose.sh %s under %s: %v", strings.Join(bg.Compose, " "), chain.line, err)
		}
		return read(guest, "tools/compose/compose.sh output")
	}
	manifest, err := os.ReadFile(filepath.Join(src, "Cargo.toml"))
	if err != nil {
		return nil, cannot("build", "the core's Cargo.toml would not read: %v", err)
	}
	artifact := checks.WitGuestArtifact(string(manifest))
	if artifact == "" || filepath.Base(bg.Artifact) != artifact {
		return nil, finding("build", "build-guest.sh copies %s but Cargo.toml's crate builds %q", filepath.Base(bg.Artifact), artifact)
	}
	cargo := slices.Clone(bg.Cargo)
	cargo[0] = chain.bin(cargo[0])
	if _, err := r.run(src, env, cargo...); err != nil {
		return nil, cannot("build", "%s under %s: %v", strings.Join(bg.Cargo, " "), chain.line, err)
	}
	if err := atoms.CarryOut(filepath.Join(target, bg.Artifact), guest); err != nil {
		return nil, cannot("build", "the guest would not copy out of the target: %v", err)
	}
	return read(guest, bg.Artifact)
}

// read is the guest a build left, or CANNOT RUN naming what it should have left.
func read(guest, what string) ([]byte, *failure) {
	body, err := os.ReadFile(guest)
	if err != nil {
		return nil, cannot("build", "the build left no %s", what)
	}
	return body, nil
}

// generated is what the generator wrote: the directory and the files in it.
type generated struct {
	dir   string
	files map[string][]byte
}

// generate runs the recorded generator command over the guest, as the regen test
// does: the guest and the bindings are mapped into a directory of their own, and
// the module files gravity writes beside the bindings land there too. The
// generator is the prebuilt for the recorded commit, held to that commit by its
// --version; gofmt is the staged Go's, which gravity reads through $GOFMT.
func (r *runner) generate(core, coreDir, commit, command string, guest []byte) (generated, *failure) {
	bin, _, err := atoms.StagedGravity(commit)
	if err != nil || bin == "" {
		return generated{}, cannot("generate", "no gravity staged for %s (tool:%s is not on PATH, and GRAVITY names none): %v", commit, checks.GravityTool(commit), err)
	}
	if out, err := r.run(coreDir, nil, bin, "--version"); err != nil || !strings.Contains(out, commit) {
		return generated{}, cannot("generate", "%s is %q, provenance.json pins the generator at %s", bin, out, commit)
	}
	gofmt, err := lookPath("gofmt")
	if err != nil {
		return generated{}, cannot("generate", "tool:gofmt is not on PATH, and gravity --gofmt needs it: %v", err)
	}
	dir := filepath.Join(r.work, core, "gen")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return generated{}, cannot("generate", "%v", err)
	}
	args := strings.Fields(command)
	if len(args) == 0 || args[0] != "gravity" {
		return generated{}, finding("generate", "generator.command %q does not start with gravity", command)
	}
	var maps int
	for i, a := range args {
		switch a {
		case "./zz_generated.go", "./guest.wasm":
			args[i] = filepath.Join(dir, strings.TrimPrefix(a, "./"))
			maps++
		}
	}
	if maps != 2 {
		return generated{}, finding("generate", "generator.command %q does not name ./guest.wasm and ./zz_generated.go", command)
	}
	if err := os.WriteFile(filepath.Join(dir, "guest.wasm"), guest, 0o644); err != nil {
		return generated{}, cannot("generate", "%v", err)
	}
	if _, err := r.run(coreDir, []string{"GOFMT=" + gofmt}, append([]string{bin}, args[1:]...)...); err != nil {
		return generated{}, cannot("generate", "%v", err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return generated{}, cannot("generate", "%v", err)
	}
	g := generated{dir: dir, files: map[string][]byte{}}
	for _, e := range ents {
		if e.Name() == "guest.wasm" {
			continue
		}
		if g.files[e.Name()], err = os.ReadFile(filepath.Join(dir, e.Name())); err != nil {
			return generated{}, cannot("generate", "%v", err)
		}
	}
	return g, nil
}

// sum is a file's sha256, hex.
func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// short is the head of a sha256, enough to tell two apart on a line.
func short(s string) string { return s[:min(10, len(s))] }

// settle puts the regenerated files in the tree, rewrites provenance.json's
// hashes, and words what moved.
func (r *runner) settle(core, dir, provText string, prov Prov, guestBytes []byte, gen generated, chain toolchain, wasmDir, home string) (string, *failure) {
	bindingsFile := "zz_generated.go"
	if prov.Bindings != nil {
		bindingsFile = prov.Bindings.File
	}
	want := []string{bindingsFile}
	for _, e := range prov.Embedded {
		want = append(want, e.File)
	}
	for _, f := range want {
		if _, ok := gen.files[f]; !ok {
			return "", finding("generate", "the generator did not write %s (it wrote %s)", f, strings.Join(slices.Sorted(maps.Keys(gen.files)), ", "))
		}
	}
	for f := range gen.files {
		if !slices.Contains(want, f) {
			return "", finding("generate", "the generator wrote %s, which provenance.json does not record", f)
		}
	}
	files := map[string][]byte{prov.Guest.File: guestBytes}
	for f, b := range gen.files {
		files[f] = b
	}
	moved := map[string][2]string{} // file -> old, new
	var writes []write
	for _, f := range slices.Sorted(maps.Keys(files)) {
		old, _ := os.ReadFile(filepath.Join(dir, f))
		moved[f] = [2]string{sum(old), sum(files[f])}
		if !bytes.Equal(old, files[f]) {
			writes = append(writes, write{f, files[f]})
		}
	}
	recs := prov.Records()
	for i := range recs {
		recs[i].SHA256, recs[i].Bytes = sum(files[recs[i].File]), len(files[recs[i].File])
	}
	rewritten, err := Rewrite(provText, recs)
	if err != nil {
		return "", finding("write", "%v", err)
	}
	if rewritten != provText {
		writes = append(writes, write{"provenance.json", []byte(rewritten)})
	}
	for _, w := range writes {
		if err := os.WriteFile(filepath.Join(dir, w.name), w.body, 0o644); err != nil {
			return "", cannot("write", "%v", err)
		}
	}
	embedded := make([]string, 0, len(prov.Embedded))
	for _, e := range prov.Embedded {
		embedded = append(embedded, e.File+" "+delta(moved[e.File]))
	}
	return fmt.Sprintf("%s: guest %s; bindings %s; embedded %s; %s (%s)%s", core, delta(moved[prov.Guest.File]), delta(moved[bindingsFile]),
		strings.Join(embedded, ", "), chain.line, chain.how, homeNote(home)+r.toolNote(prov, wasmDir)), nil
}

// write is a file to put in a core's directory.
type write struct {
	name string
	body []byte
}

// delta words a file's hash: unchanged, or old->new.
func delta(m [2]string) string {
	if m[0] == m[1] {
		return "unchanged " + short(m[1])
	}
	return short(m[0]) + "->" + short(m[1])
}

// toolNote names the composing tools a composed core ran under beside the ones
// its record names, so a byte difference that follows from them can be read off
// the line. A plain core composes nothing and says nothing.
func (r *runner) toolNote(prov Prov, wasmDir string) string {
	if !strings.HasPrefix(prov.CoreBuild, "tools/compose/compose.sh") {
		return ""
	}
	return fmt.Sprintf("; wasm-tools %s (recorded %s), wac %s (recorded %s)",
		r.toolVersion(wasmDir, "wasm-tools"), recorded(prov.CoreToolchain, "wasm-tools"), r.toolVersion("", "wac"), recorded(prov.CoreToolchain, "wac"))
}

// wasmTools selects the wasm-tools a composed core is built with. The same
// compiler on the same source gives the same core modules, but a different
// wasm-tools wraps them in a different component (MEASURED: classescore under
// 1.262.0 kept every embedded module and its bindings and changed guest.wasm),
// so the version the record names is the one used: the container's when it is
// that, else the pinned release of it laid under the work directory. A recorded
// version with no release pinned is CANNOT RUN naming both; a record that names
// none uses the container's. It answers the directory to put on PATH ("" for
// the container's own).
func (r *runner) wasmTools(want string) (string, *failure) {
	have := r.toolVersion("", "wasm-tools")
	if want == "unrecorded" || want == have {
		return "", nil
	}
	if dir, ok := r.wasm[want]; ok {
		return dir, nil
	}
	url, member, ok := checks.WasmToolsRelease(want)
	if !ok {
		return "", cannot("toolchain", "provenance.json records wasm-tools %s but the container has %s and no release of %s is pinned", want, have, want)
	}
	dir, tgz := filepath.Join(r.work, "wasm-tools-"+want), filepath.Join(r.work, "wasm-tools-"+want+".tar.gz")
	if err := regenkit.Fetch(r.ctx, fetchPinned, url, tgz); err != nil {
		return "", cannot("toolchain", "wasm-tools %s: %v", want, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", cannot("toolchain", "wasm-tools %s: %v", want, err)
	}
	if err := regenkit.Step(r.exec, nil, checks.RegenTar(tgz, dir, true, strings.Count(member, "/"), member)...); err != nil {
		return "", cannot("toolchain", "wasm-tools %s: %v", want, err)
	}
	if got := r.toolVersion(dir, "wasm-tools"); got != want {
		return "", cannot("toolchain", "the wasm-tools laid for %s prints %s", want, got)
	}
	r.wasm[want] = dir
	return dir, nil
}

// toolVersion is the semver a tool prints for --version, or "?": the one in dir,
// or on PATH when dir is "".
func (r *runner) toolVersion(dir, name string) string {
	if dir != "" {
		name = filepath.Join(dir, name)
	}
	out, err := r.run(r.in.Root, nil, name, "--version")
	if err != nil {
		return "?"
	}
	return semver(out)
}

// recorded is the version a provenance toolchain string names for a tool.
func recorded(toolchain, tool string) string {
	for _, part := range strings.Split(toolchain, ",") {
		if f := strings.Fields(part); len(f) == 2 && f[0] == tool {
			return f[1]
		}
	}
	return "unrecorded"
}

// semver is the first x.y.z in text, or "?".
func semver(text string) string {
	for _, w := range strings.Fields(text) {
		if parts := strings.Split(w, "."); len(parts) == 3 && allDigits(parts) {
			return w
		}
	}
	return "?"
}

func allDigits(parts []string) bool {
	for _, p := range parts {
		if p == "" || strings.Trim(p, "0123456789") != "" {
			return false
		}
	}
	return true
}
