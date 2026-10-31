package castrelease

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
)

var (
	rustRow = checks.AtomDef{ID: "rust:cast-release", Stage: checks.StageCast, Lane: checks.LaneRust}
	goRow   = checks.AtomDef{ID: "go:cast-release", Stage: checks.StageCast, Lane: checks.LaneGo}
)

const (
	cargoToml = "[workspace]\nmembers = [\"a\"]\n"
	goMod     = "module example.com/x\n\ngo 1.26\n"
)

// meta is `cargo metadata` for a workspace whose default members have these bins.
func meta(bins ...string) string {
	var ts []string
	for _, b := range bins {
		ts = append(ts, `{"name":"`+b+`","kind":["bin"]}`)
	}
	return `{"packages":[{"id":"p","targets":[` + strings.Join(ts, ",") + `]}],"workspace_default_members":["p"]}`
}

// toolchain is a fake cargo and go: it answers metadata and list, and leaves
// each built binary where the real tool is told to put it.
type toolchain struct {
	t        *testing.T
	metadata string
	list     string
	exit     map[string]int // by "metadata", "list", "build", or "build <pkg>"
	out      map[string]string
	skip     map[string]bool // binaries the build leaves out
	calls    []atoms.Cmd
}

func (c *toolchain) answer(cmd atoms.Cmd) (string, int) {
	c.calls = append(c.calls, cmd)
	key := ""
	switch {
	case cmd.Name == "git":
		return "1792086104\n", 0
	case cmd.Name == "cargo" && cmd.Args[0] == "metadata":
		key = "metadata"
	case cmd.Name == "cargo" && cmd.Args[0] == "build":
		key = "build"
	case cmd.Name == "go" && cmd.Args[0] == "list":
		key = "list"
	case cmd.Name == "go" && cmd.Args[0] == "build":
		key = "build " + cmd.Args[len(cmd.Args)-1]
	}
	if code := c.exit[key]; code != 0 {
		return c.out[key], code
	}
	switch key {
	case "metadata":
		return c.metadata, 0
	case "list":
		return c.list, 0
	case "build":
		for _, b := range binariesOf(c.metadata) {
			if c.skip[b] {
				continue
			}
			p := filepath.Join(env(cmd, "CARGO_TARGET_DIR"), "release", b)
			c.write(p, "ELF "+b)
		}
	}
	if strings.HasPrefix(key, "build ") {
		o := cmd.Args[slices.Index(cmd.Args, "-o")+1]
		if !c.skip[filepath.Base(o)] {
			c.write(o, "ELF "+filepath.Base(o))
		}
	}
	return "", 0
}

func (c *toolchain) write(p, body string) {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		c.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		c.t.Fatal(err)
	}
}

func binariesOf(metadata string) []string {
	var out []string
	for _, part := range strings.Split(metadata, `{"name":"`)[1:] {
		out = append(out, part[:strings.Index(part, `"`)])
	}
	return out
}

func env(c atoms.Cmd, name string) string {
	for _, e := range c.Env {
		if v, ok := strings.CutPrefix(e, name+"="); ok {
			return v
		}
	}
	return ""
}

func (c *toolchain) named(program string, first string) []atoms.Cmd {
	var out []atoms.Cmd
	for _, k := range c.calls {
		if k.Name == program && k.Args[0] == first {
			out = append(out, k)
		}
	}
	return out
}

func start(t *testing.T, files map[string]string, c *toolchain) (atomkit.In, *atomtest.Fake, string) {
	t.Helper()
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	t.Setenv("ATOM_RELEASE_OUT", "")
	t.Setenv("CARGO_TARGET_DIR", filepath.Join(work, "target"))
	t.Setenv("GOFLAGS", "")
	if c == nil {
		c = &toolchain{}
	}
	c.t = t
	in, f := atomtest.ToolTree(t, files, c.answer)
	f.Free = []string{"cargo", "find", "go"}
	return in, f, work
}

func rustTree(extra map[string]string) map[string]string {
	files := map[string]string{"Cargo.toml": cargoToml, "a/src/main.rs": "fn main(){}"}
	for k, v := range extra {
		files[k] = v
	}
	return files
}

func goTree(extra map[string]string) map[string]string {
	files := map[string]string{"go.mod": goMod, "cmd/x/main.go": "package main"}
	for k, v := range extra {
		files[k] = v
	}
	return files
}

func TestDEF_B6_cast_RustBuildsEveryWorkspaceBinaryWithOneBuild(t *testing.T) {
	c := &toolchain{metadata: meta("tongs", "tongs-admin", "x")}
	in, f, work := start(t, rustTree(nil), c)
	v := Rust(context.Background(), in, rustRow)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Pass, "tongs, tongs-admin, x")
	if m := c.named("cargo", "metadata"); len(m) != 1 || strings.Join(m[0].Args, " ") != "metadata --no-deps --format-version 1 --locked" {
		t.Errorf("metadata: %v", f.Calls)
	}
	b := c.named("cargo", "build")
	if len(b) != 1 || strings.Join(b[0].Args, " ") != "build --release --locked" {
		t.Fatalf("one cargo build over the workspace, got %v", f.Calls)
	}
	if stamp := slices.IndexFunc(f.Calls, func(k atoms.Cmd) bool { return k.Name == "find" }); stamp != 0 {
		t.Errorf("the tree is dated before anything is read: %v", f.Ran())
	}
	for _, n := range []string{"tongs", "tongs-admin", "x"} {
		got, err := os.ReadFile(filepath.Join(work, "release", n))
		if err != nil || string(got) != "ELF "+n {
			t.Errorf("%s was not carried into the release directory: %q %v", n, got, err)
		}
	}
}

func TestDEF_B6_cast_RustHonoursTheReleaseOutAndTheTarget(t *testing.T) {
	c := &toolchain{metadata: meta("tongs")}
	in, _, _ := start(t, rustTree(nil), c)
	out := t.TempDir()
	t.Setenv("ATOM_RELEASE_OUT", out)
	atomtest.Expect(t, Rust(context.Background(), in, rustRow), checks.StatePass, atomtest.Pass)
	if _, err := os.Stat(filepath.Join(out, "tongs")); err != nil {
		t.Error(err)
	}
	if got := env(c.named("cargo", "build")[0], "CARGO_TARGET_DIR"); !strings.HasSuffix(got, "/target") {
		t.Errorf("CARGO_TARGET_DIR is the caller's: %q", got)
	}
}

func TestDEF_B6_cast_RustWithNoTargetDirKeepsOneInTheWorkDirectory(t *testing.T) {
	c := &toolchain{metadata: meta("tongs")}
	in, _, work := start(t, rustTree(nil), c)
	t.Setenv("CARGO_TARGET_DIR", "")
	atomtest.Expect(t, Rust(context.Background(), in, rustRow), checks.StatePass, atomtest.Pass)
	if got := env(c.named("cargo", "build")[0], "CARGO_TARGET_DIR"); got != filepath.Join(work, "cargo-target") {
		t.Errorf("target %q", got)
	}
}

func TestDEF_B6_cast_RustMetadataFailureIsClassifiedLikeDagger(t *testing.T) {
	// a tool failure is a finding, with the tool's own words
	c := &toolchain{exit: map[string]int{"metadata": 101}, out: map[string]string{"metadata": "error: the lock file needs to be updated"}}
	in, _, _ := start(t, rustTree(nil), c)
	v := Rust(context.Background(), in, rustRow)
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "findings in cargo metadata", "the lock file needs to be updated")

	// a network fault is could-not-run
	c = &toolchain{exit: map[string]int{"metadata": 101}, out: map[string]string{"metadata": "error: failed to get `x`: Connection reset by peer"}}
	in, _, _ = start(t, rustTree(nil), c)
	atomtest.Expect(t, Rust(context.Background(), in, rustRow), checks.StateCannotRun, atomtest.Cannot, "could not run: cargo metadata failed on a network fault")

	// a cargo that would not start never ran
	c = &toolchain{exit: map[string]int{"metadata": -1}, out: map[string]string{"metadata": "executable file not found"}}
	in, _, _ = start(t, rustTree(nil), c)
	atomtest.Expect(t, Rust(context.Background(), in, rustRow), checks.StateCannotRun, atomtest.Cannot, "cargo metadata did not run")
}

func TestDEF_B6_cast_RustWorkspaceWithNoBinIsAFinding(t *testing.T) {
	c := &toolchain{metadata: `{"packages":[],"workspace_default_members":[]}`}
	in, f, _ := start(t, rustTree(nil), c)
	v := Rust(context.Background(), in, rustRow)
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "the workspace's default members have no bin target, so a binary repo has nothing to cast")
	if len(c.named("cargo", "build")) != 0 {
		t.Errorf("nothing is built: %v", f.Ran())
	}
}

func TestDEF_B6_cast_RustBuildFailureIsClassifiedLikeDagger(t *testing.T) {
	c := &toolchain{metadata: meta("tongs"), exit: map[string]int{"build": 101}, out: map[string]string{"build": "error[E0425]: cannot find value `x`"}}
	in, _, work := start(t, rustTree(nil), c)
	v := Rust(context.Background(), in, rustRow)
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "findings in cargo build", "E0425")
	if _, err := os.Stat(filepath.Join(work, "release", "tongs")); err == nil {
		t.Error("a failed build carried a binary")
	}

	c = &toolchain{metadata: meta("tongs"), exit: map[string]int{"build": 101}, out: map[string]string{"build": "Error: unknown flag: --locked"}}
	in, _, _ = start(t, rustTree(nil), c)
	atomtest.Expect(t, Rust(context.Background(), in, rustRow), checks.StateCannotRun, atomtest.Cannot, "refused the lane's own arguments")

	c = &toolchain{metadata: meta("tongs"), exit: map[string]int{"build": -1}, out: map[string]string{"build": "no cargo"}}
	in, _, _ = start(t, rustTree(nil), c)
	atomtest.Expect(t, Rust(context.Background(), in, rustRow), checks.StateCannotRun, atomtest.Cannot, "the release build did not run")
}

func TestDEF_B6_cast_RustBinaryTheBuildLeftOutIsAFinding(t *testing.T) {
	c := &toolchain{metadata: meta("a", "b"), skip: map[string]bool{"b": true}}
	in, _, work := start(t, rustTree(nil), c)
	v := Rust(context.Background(), in, rustRow)
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, `the release build left no "b" in `+filepath.Join(work, "release"))
}

func TestDEF_B6_cast_AStaleBinaryInTheTargetIsNeverCarried(t *testing.T) {
	c := &toolchain{metadata: meta("a"), skip: map[string]bool{"a": true}}
	in, _, work := start(t, rustTree(nil), c)
	stale := filepath.Join(os.Getenv("CARGO_TARGET_DIR"), "release", "a")
	c.write(stale, "OLD")
	v := Rust(context.Background(), in, rustRow)
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, `left no "a"`)
	if _, err := os.Stat(filepath.Join(work, "release", "a")); err == nil {
		t.Error("a previous tree's binary was carried")
	}
}

func TestDEF_B6_cast_RustSetupFailuresAreCouldNotRun(t *testing.T) {
	// the release directory cannot be made
	c := &toolchain{metadata: meta("a")}
	in, _, work := start(t, rustTree(nil), c)
	blocker := filepath.Join(work, "blocker")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATOM_RELEASE_OUT", filepath.Join(blocker, "out"))
	atomtest.Expect(t, Rust(context.Background(), in, rustRow), checks.StateCannotRun, atomtest.Cannot, "the release directory could not be made")

	// the tree cannot be dated
	c = &toolchain{metadata: meta("a")}
	in, _, _ = start(t, rustTree(nil), c)
	in.Exec = func(_ context.Context, k atoms.Cmd) (string, int) {
		if k.Name == "find" {
			return "denied", 1
		}
		return c.answer(k)
	}
	atomtest.Expect(t, Rust(context.Background(), in, rustRow), checks.StateCannotRun, atomtest.Cannot, "CANNOT RUN")

	// a target that cannot be cleared
	c = &toolchain{metadata: meta("a")}
	in, _, _ = start(t, rustTree(nil), c)
	dir := filepath.Join(os.Getenv("CARGO_TARGET_DIR"), "release", "a")
	if err := os.MkdirAll(filepath.Join(dir, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	v := Rust(context.Background(), in, rustRow)
	atomtest.Expect(t, v, checks.StateCannotRun, atomtest.Cannot, "could not clear")
}

func TestDEF_B6_cast_TheToolchainsStandDownForTheOtherLanesTrees(t *testing.T) {
	// a go tree is the go atom's: the rust atom is absent
	in, f, _ := start(t, goTree(nil), &toolchain{})
	v := Rust(context.Background(), in, rustRow)
	atomtest.Expect(t, v, checks.StatePass, "absent", "rust:cast-release: ABSENT")
	if len(f.Calls) != 0 {
		t.Errorf("an absent atom runs nothing: %v", f.Ran())
	}
	// and a cargo tree is the rust atom's
	in, _, _ = start(t, rustTree(nil), &toolchain{})
	atomtest.Expect(t, Go(context.Background(), in, goRow), checks.StatePass, "absent", "go:cast-release: ABSENT")
}

func TestDEF_B6_cast_ATreeDeclaringBothToolchainsIsRefusedByName(t *testing.T) {
	files := rustTree(goTree(nil))
	in, f, _ := start(t, files, &toolchain{})
	v := Rust(context.Background(), in, rustRow)
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "the tree declares both a Cargo.toml and a root go.mod, so the cast cannot tell which toolchain builds its binaries")
	if len(f.Calls) != 0 {
		t.Errorf("nothing runs: %v", f.Ran())
	}
	// said once: the go atom defers to the rust atom
	in, _, _ = start(t, files, &toolchain{})
	atomtest.Expect(t, Go(context.Background(), in, goRow), checks.StatePass, "absent", "go:cast-release: ABSENT", "rust:cast-release")
}

func TestDEF_B6_cast_ATreeDeclaringNeitherIsRefusedByName(t *testing.T) {
	in, _, _ := start(t, map[string]string{"README.md": "x"}, &toolchain{})
	v := Rust(context.Background(), in, rustRow)
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "the tree is cast as a binary repo and declares no lane that builds one — no Cargo.toml and no go.mod at the root — so nothing can compile its binaries")
}

func TestDEF_B6_cast_AModuleBelowTheRootIsNotTheRootsGo(t *testing.T) {
	// only a ROOT go.mod declares the go lane (cast.go: slices.Contains(mods, "."))
	files := rustTree(map[string]string{"tools/go.mod": goMod})
	in, _, _ := start(t, files, &toolchain{metadata: meta("a")})
	atomtest.Expect(t, Rust(context.Background(), in, rustRow), checks.StatePass, atomtest.Pass)
}

func TestDEF_B6_cast_TheTreeThatCannotBeReadIsCouldNotRun(t *testing.T) {
	in := atomtest.TreeIn(t, rustTree(nil))
	in.Files = nil
	in.FilesErr = os.ErrPermission
	atomtest.Expect(t, Rust(context.Background(), in, rustRow), checks.StateCannotRun, atomtest.Cannot, "the tree's Go modules could not be enumerated")

	in = atomtest.MissingRoot(t)
	atomtest.Expect(t, Go(context.Background(), in, goRow), checks.StateCannotRun, atomtest.Cannot, "the repository root could not be read")
}

func TestDEF_B6_cast_GoBuildsEveryMainUnderCmdOneEachWithTheFleetsFlags(t *testing.T) {
	c := &toolchain{list: "main example.com/x/cmd/argus\nmain example.com/x/cmd/argusctl\nfoo example.com/x/cmd/lib\n"}
	files := goTree(map[string]string{"payload/argus.toml": "x"})
	in, f, work := start(t, files, c)
	v := Go(context.Background(), in, goRow)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Pass, "argus, argusctl")
	dl := c.named("go", "mod")
	if len(dl) != 1 || strings.Join(dl[0].Args, " ") != "mod download" {
		t.Errorf("an unvendored module is downloaded once: %v", f.Calls)
	}
	if l := c.named("go", "list"); len(l) != 1 || strings.Join(l[0].Args, " ") != "list -find -f {{.Name}} {{.ImportPath}} ./cmd/..." || !slices.Contains(l[0].Env, "CGO_ENABLED=0") {
		t.Errorf("list: %v", l)
	}
	b := c.named("go", "build")
	if len(b) != 2 {
		t.Fatalf("one build per binary: %v", f.Calls)
	}
	out := filepath.Join(work, "release")
	for i, n := range []string{"argus", "argusctl"} {
		want := strings.Join(checks.GoReleaseArgsTo(out, checks.ReleaseBinary{Name: n, Package: "./cmd/" + n}, false, "", "")[1:], " ")
		if got := strings.Join(b[i].Args, " "); got != want {
			t.Errorf("build %d: got %q, want %q", i, got, want)
		}
		if !slices.Contains(b[i].Env, "CGO_ENABLED=0") || !slices.Contains(b[i].Env, "GOFLAGS=-buildvcs=false") {
			t.Errorf("env %v", b[i].Env)
		}
		if body, err := os.ReadFile(filepath.Join(out, n)); err != nil || string(body) != "ELF "+n {
			t.Errorf("%s: %q %v", n, body, err)
		}
	}
}

func TestDEF_B6_cast_GoVendoredModuleBuildsFromVendorAndDownloadsNothing(t *testing.T) {
	c := &toolchain{list: "main example.com/x/cmd/x\n"}
	in, f, _ := start(t, goTree(map[string]string{"vendor/modules.txt": "# x"}), c)
	atomtest.Expect(t, Go(context.Background(), in, goRow), checks.StatePass, atomtest.Pass)
	if len(c.named("go", "mod")) != 0 {
		t.Errorf("a vendored module downloads nothing: %v", f.Ran())
	}
	if b := c.named("go", "build"); len(b) != 1 || b[0].Args[1] != "-mod=vendor" {
		t.Errorf("build %v", b)
	}
}

func TestDEF_B6_cast_GoAppendsToTheCallersGOFLAGS(t *testing.T) {
	c := &toolchain{list: "main example.com/x/cmd/x\n"}
	in, _, _ := start(t, goTree(nil), c)
	t.Setenv("GOFLAGS", "-mod=mod")
	atomtest.Expect(t, Go(context.Background(), in, goRow), checks.StatePass, atomtest.Pass)
	if b := c.named("go", "build"); !slices.Contains(b[0].Env, "GOFLAGS=-mod=mod -buildvcs=false") {
		t.Errorf("env %v", b[0].Env)
	}
}

func TestDEF_B6_cast_GoNoMainUnderCmdIsAFinding(t *testing.T) {
	c := &toolchain{list: "foo example.com/x/cmd/lib\n"}
	in, _, _ := start(t, goTree(nil), c)
	atomtest.Expect(t, Go(context.Background(), in, goRow), checks.StateFindings, atomtest.Findings, "the tree has no main package under cmd/, so a binary repo has nothing to cast")
}

func TestDEF_B6_cast_GoListAndBuildFailuresAreClassifiedLikeDagger(t *testing.T) {
	c := &toolchain{exit: map[string]int{"list": 1}, out: map[string]string{"list": "pattern ./cmd/...: directory prefix cmd does not contain main module"}}
	in, _, _ := start(t, goTree(nil), c)
	atomtest.Expect(t, Go(context.Background(), in, goRow), checks.StateFindings, atomtest.Findings, "findings in go list ./cmd/...")

	c = &toolchain{exit: map[string]int{"list": -1}, out: map[string]string{"list": "no go"}}
	in, _, _ = start(t, goTree(nil), c)
	atomtest.Expect(t, Go(context.Background(), in, goRow), checks.StateCannotRun, atomtest.Cannot, "go list did not run")

	c = &toolchain{list: "main example.com/x/cmd/a\nmain example.com/x/cmd/b\n", exit: map[string]int{"build ./cmd/a": 1}, out: map[string]string{"build ./cmd/a": "./cmd/a/main.go:3: undefined: x"}}
	in, _, _ = start(t, goTree(nil), c)
	v := Go(context.Background(), in, goRow)
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "findings in go build ./cmd/a", "undefined: x")
	if len(c.named("go", "build")) != 1 {
		t.Error("the chain stops at the first binary that did not compile")
	}

	c = &toolchain{list: "main example.com/x/cmd/a\n", exit: map[string]int{"build ./cmd/a": -1}, out: map[string]string{"build ./cmd/a": "no go"}}
	in, _, _ = start(t, goTree(nil), c)
	atomtest.Expect(t, Go(context.Background(), in, goRow), checks.StateCannotRun, atomtest.Cannot, "the release build of ./cmd/a did not run")
}

func TestDEF_B6_cast_GoSetupFailuresAreCouldNotRun(t *testing.T) {
	c := &toolchain{list: "main example.com/x/cmd/a\n"}
	in, _, _ := start(t, goTree(nil), c)
	in.Exec = func(_ context.Context, k atoms.Cmd) (string, int) {
		if k.Name == "go" && k.Args[0] == "mod" {
			return "proxy 502", 1
		}
		return c.answer(k)
	}
	atomtest.Expect(t, Go(context.Background(), in, goRow), checks.StateCannotRun, atomtest.Cannot, "preflight")

	c = &toolchain{list: "main example.com/x/cmd/a\n"}
	in, _, work := start(t, goTree(nil), c)
	blocker := filepath.Join(work, "blocker")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATOM_RELEASE_OUT", filepath.Join(blocker, "out"))
	atomtest.Expect(t, Go(context.Background(), in, goRow), checks.StateCannotRun, atomtest.Cannot, "the release directory could not be made")
}

func TestDEF_B6_cast_GoBinaryTheBuildLeftOutIsAFinding(t *testing.T) {
	c := &toolchain{list: "main example.com/x/cmd/a\n", skip: map[string]bool{"a": true}}
	in, _, work := start(t, goTree(nil), c)
	atomtest.Expect(t, Go(context.Background(), in, goRow), checks.StateFindings, atomtest.Findings, `the release build left no "a" in `+filepath.Join(work, "release"))
}

func TestDEF_B6_cast_ABinaryThatCannotBeCarriedIsCouldNotRun(t *testing.T) {
	c := &toolchain{metadata: meta("a")}
	in, _, work := start(t, rustTree(nil), c)
	// a directory where the binary must land: the build succeeded, the copy cannot
	if err := os.MkdirAll(filepath.Join(work, "release", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	atomtest.Expect(t, Rust(context.Background(), in, rustRow), checks.StateCannotRun, atomtest.Cannot, "could not copy")
}

// DEF-B6-029 (forks/gravity 08a969e): rustup could not write its own home on
// the pod's read-only root. A tool that cannot write the lane's filesystem says
// nothing about the tree, so it is could-not-run, never findings.
func TestF13_AToolThatCannotWriteTheLanesFilesystemIsCouldNotRun(t *testing.T) {
	erofs := "info: syncing channel updates for 1.90.0-x86_64-unknown-linux-gnu\nerror: could not create temp file /usr/local/rustup/tmp/x: Read-only file system (os error 30)"
	c := &toolchain{exit: map[string]int{"metadata": 1}, out: map[string]string{"metadata": erofs}}
	in, _, _ := start(t, rustTree(nil), c)
	v := Rust(context.Background(), in, rustRow)
	atomtest.Expect(t, v, checks.StateCannotRun, atomtest.Cannot, "could not run: cargo metadata could not write the lane's filesystem", "Read-only file system")

	c = &toolchain{metadata: meta("tongs"), exit: map[string]int{"build": 1}, out: map[string]string{"build": erofs}}
	in, _, _ = start(t, rustTree(nil), c)
	atomtest.Expect(t, Rust(context.Background(), in, rustRow), checks.StateCannotRun, atomtest.Cannot, "the release build could not write the lane's filesystem")

	// a tree's own words about a file mode are its business
	c = &toolchain{metadata: meta("tongs"), exit: map[string]int{"build": 101}, out: map[string]string{"build": "error: build.rs asserts the file is not read-only"}}
	in, _, _ = start(t, rustTree(nil), c)
	atomtest.Expect(t, Rust(context.Background(), in, rustRow), checks.StateFindings, atomtest.Findings, "findings in cargo build")
}

// A DELIVERY LANE'S GO BINARIES CARRY THE STAMP (foundry-tools#18676): the
// executor names a release directory only in the lanes that ship them.
func TestDEF_B6_cast_GoBinariesAreStampedInADeliveryLane(t *testing.T) {
	c := &toolchain{list: "main example.com/x/cmd/x\n"}
	in, _, work := start(t, goTree(nil), c)
	t.Setenv("ATOM_RELEASE_OUT", filepath.Join(work, "assembled"))
	t.Setenv("ATOM_SHA", "b3db6f1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	atomtest.Expect(t, Go(context.Background(), in, goRow), checks.StatePass, atomtest.Pass)
	b := c.named("go", "build")
	if len(b) != 1 {
		t.Fatalf("builds: %v", b)
	}
	if got := strings.Join(b[0].Args, " "); !strings.Contains(got, "-X main.Version=1792086104-b3db6f1 ") {
		t.Errorf("the compile is not stamped: %q", got)
	}
}
