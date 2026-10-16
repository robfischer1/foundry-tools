package rustrelease

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
)

const copiesTron = "FROM x\nCOPY release/tron /tron\n"

func star(name, dockerfile string) map[string]string {
	return atomtest.Star(name, dockerfile, map[string]string{"Cargo.toml": "[package]\nname = \"" + name + "\"\n"})
}

func line(c atoms.Cmd) string { return strings.Join(append([]string{c.Name}, c.Args...), " ") }

// cargo is a fake cargo that leaves each built binary under the target
// directory it was given, as cargo does, unless told not to.
type cargo struct {
	t       *testing.T
	exit    map[string]int // by "-p <pkg>"
	out     map[string]string
	skip    map[string]bool // packages that build no binary
	targets []string
}

func (c *cargo) answer(cmd atoms.Cmd) (string, int) {
	if cmd.Name != "cargo" || cmd.Args[0] != "build" {
		return "", 0
	}
	pkg := cmd.Args[len(cmd.Args)-1]
	for _, e := range cmd.Env {
		if v, ok := strings.CutPrefix(e, "CARGO_TARGET_DIR="); ok {
			c.targets = append(c.targets, v)
			if code := c.exit[pkg]; code != 0 {
				return c.out[pkg], code
			}
			if !c.skip[pkg] {
				p := filepath.Join(v, "release", pkg)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					c.t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("ELF "+pkg), 0o755); err != nil {
					c.t.Fatal(err)
				}
			}
			return "", 0
		}
	}
	c.t.Fatalf("cargo ran with no CARGO_TARGET_DIR: %v", cmd.Env)
	return "", 0
}

// run lays the tree and runs the atom over the fake.
func run(t *testing.T, files map[string]string, c *cargo) (checks.Verdict, *atomtest.Fake, string) {
	t.Helper()
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	t.Setenv("ATOM_RELEASE_OUT", "")
	t.Setenv("CARGO_TARGET_DIR", "")
	if c == nil {
		c = &cargo{}
	}
	c.t = t
	in, f := atomtest.ToolTree(t, files, c.answer)
	f.Free = []string{"cargo", "find"}
	return check(context.Background(), in), f, work
}

func builds(f *atomtest.Fake) []atoms.Cmd {
	var out []atoms.Cmd
	for _, c := range f.Calls {
		if c.Name == "cargo" && c.Args[0] == "build" {
			out = append(out, c)
		}
	}
	return out
}

func TestTheDeclarationValidatesAndKeepsTheRowItReplaces(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrepush || Def.Lane != checks.LaneRust || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "rust" || Def.NotOnForks != "" || Def.NeedsManifest || Def.Surface != "root:Cargo.toml" ||
		!slices.Equal(Def.Needs, []string{"dies"}) || Def.Timeout != 45*time.Minute || !Def.WritesTree ||
		!strings.HasPrefix(Def.Desc, "The release binaries the image will carry compile under cargo's release profile") {
		t.Errorf("%+v", Def)
	}
}

// THE RELEASE BUILD IS THE IMAGE'S COMPILE, derived: the star's own crate under
// the release profile, --locked, its binary carried into the release directory;
// or the binaries the Dockerfile copies, one build each.
func TestRustReleaseBuildsWhatTheImageWillCarry(t *testing.T) {
	c := &cargo{}
	v, f, work := run(t, star("tron", copiesTron), c)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Pass, "release build: tron", "the Dockerfile's COPY lines", "--locked")
	b := builds(f)
	if len(b) != 1 || line(b[0]) != "cargo build --release --locked -p tron" {
		t.Fatalf("builds %v", f.Calls)
	}
	// the tree is dated before the compile, and the compile is bounded
	if stamp := slices.IndexFunc(f.Calls, func(c atoms.Cmd) bool { return c.Name == "find" }); stamp != 0 {
		t.Errorf("the stamp must come first: %v", f.Calls)
	}
	for _, want := range checks.CargoBoundedEnv() {
		if !slices.Contains(b[0].Env, want) {
			t.Errorf("the compile is not bounded by %s: %v", want, b[0].Env)
		}
	}
	got, err := os.ReadFile(filepath.Join(work, "release", "tron"))
	if err != nil || string(got) != "ELF tron" {
		t.Errorf("the binary was not carried into the release directory: %q %v", got, err)
	}
	if fi, _ := os.Stat(filepath.Join(work, "release", "tron")); fi == nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("the binary is not executable: %v", fi)
	}

	// The Dockerfile copies more than one, and each gets its own build.
	v, f, _ = run(t, star("cerberus", "FROM x\nCOPY release/cerberus /cerberus\nCOPY release/cerberus-admin /cerberus-admin\n"), &cargo{})
	atomtest.Expect(t, v, checks.StatePass, atomtest.Pass, "cerberus, cerberus-admin")
	var pkgs []string
	for _, b := range builds(f) {
		pkgs = append(pkgs, b.Args[len(b.Args)-1])
	}
	if !slices.Equal(pkgs, []string{"cerberus", "cerberus-admin"}) {
		t.Errorf("built %v", pkgs)
	}
}

// THE TARGET DIRECTORY IS THE CALLER'S. The executor gives each pod one of its
// own (CARGO_TARGET_DIR) and the engine's lane gives its release volume; with
// neither the atom keeps one in its work directory. The atom depends on no
// volume, shared or locked, and never builds into the tree.
func TestTheTargetDirectoryIsCARGO_TARGET_DIROrTheWorkDirectorys(t *testing.T) {
	c := &cargo{}
	_, _, work := run(t, star("tron", copiesTron), c)
	if want := filepath.Join(work, "cargo-target"); !slices.Equal(c.targets, []string{want}) {
		t.Errorf("with no CARGO_TARGET_DIR the target is %v, want %s", c.targets, want)
	}

	given := filepath.Join(t.TempDir(), "pod-target")
	c = &cargo{}
	c.t = t
	t.Setenv("ATOM_WORK", t.TempDir())
	t.Setenv("ATOM_RELEASE_OUT", "")
	t.Setenv("CARGO_TARGET_DIR", given)
	in, f := atomtest.ToolTree(t, star("tron", copiesTron), c.answer)
	f.Free = []string{"cargo", "find"}
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass)
	if !slices.Equal(c.targets, []string{given}) {
		t.Errorf("CARGO_TARGET_DIR %s was not honoured: %v", given, c.targets)
	}
	if _, err := os.Stat(filepath.Join(given, "release", "tron")); err != nil {
		t.Errorf("the build did not land in the given target: %v", err)
	}
}

// A binary an earlier build left in a target that outlives the run is never
// carried: it is cleared before the build, so what is carried is this tree's.
func TestAStaleBinaryInTheTargetIsNeverCarried(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	stale := filepath.Join(target, "release", "tron")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	t.Setenv("ATOM_RELEASE_OUT", "")
	t.Setenv("CARGO_TARGET_DIR", target)
	// a build that produces no binary for the package (a record naming a crate
	// with no [[bin]]) leaves nothing to carry
	c := &cargo{skip: map[string]bool{"tron": true}}
	c.t = t
	in, f := atomtest.ToolTree(t, star("tron", copiesTron), c.answer)
	f.Free = []string{"cargo", "find"}
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass)
	if _, err := os.Stat(filepath.Join(work, "release", "tron")); err == nil {
		t.Error("a previous tree's binary was carried")
	}
}

// A CRATE THAT DOES NOT COMPILE IS A FINDING IN CARGO'S VOCABULARY (101, not 1),
// reported with the compiler's own words, and the chain stops at it: a second
// binary is not built on top of a first that failed.
func TestRustReleaseReportsTheBinaryThatDidNotBuild(t *testing.T) {
	c := &cargo{exit: map[string]int{"a": 101}, out: map[string]string{"a": "error[E0425]: cannot find value `x` in this scope"}}
	v, f, _ := run(t, star("a", "FROM x\nCOPY release/a /a\nCOPY release/b /b\n"), c)
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "release build: a, b", "error[E0425]")
	if len(builds(f)) != 1 {
		t.Errorf("b was built on top of a's failed compile: %v", f.Calls)
	}

	// A signal is could-not-run, as it is for every cargo atom.
	v, _, _ = run(t, star("tron", copiesTron), &cargo{exit: map[string]int{"tron": 137}})
	atomtest.Expect(t, v, checks.StateCannotRun, atomtest.Cannot)

	// A cargo that would not start never ran.
	v, _, _ = run(t, star("tron", copiesTron), &cargo{exit: map[string]int{"tron": -1}, out: map[string]string{"tron": "cargo: not found"}})
	atomtest.Expect(t, v, checks.StateCannotRun, atomtest.Cannot, "the atom never ran", "cargo: not found")
}

// A tree that cannot be dated is the atom's could-not-run, never a build on
// artifacts that look fresh.
func TestAStampThatFailsIsCouldNotRun(t *testing.T) {
	t.Setenv("ATOM_WORK", t.TempDir())
	t.Setenv("CARGO_TARGET_DIR", "")
	in, f := atomtest.ToolTree(t, star("tron", copiesTron), func(c atoms.Cmd) (string, int) {
		if c.Name == "find" {
			return "find: permission denied", 1
		}
		return "", 0
	})
	f.Free = []string{"cargo", "find"}
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "rust:release: CANNOT RUN - preflight", "permission denied")
	if len(builds(f)) != 0 {
		t.Errorf("cargo ran on a tree that could not be dated: %v", f.Calls)
	}
}

// A binary that built and would not copy is the run's, not the tree's: state 2
// with copyout's words, as the engine's copyout exec gave it.
func TestAReleaseDirectoryThatCannotBeMadeIsCouldNotRun(t *testing.T) {
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	t.Setenv("CARGO_TARGET_DIR", "")
	// the release directory's name is taken by a file
	if err := os.WriteFile(filepath.Join(work, "release"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c := &cargo{}
	c.t = t
	in, f := atomtest.ToolTree(t, star("tron", copiesTron), c.answer)
	f.Free = []string{"cargo", "find"}
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the release directory could not be made")
	if len(builds(f)) != 0 {
		t.Errorf("cargo ran with nowhere to put its binary: %v", f.Calls)
	}
}

// THE THREE ABSENCES ARE go:release's, IN ITS ORDER: no image, no star, a
// Dockerfile that compiles itself — each a 0 that says why, none a compile.
func TestRustReleaseIsAbsentWhereGoReleaseIs(t *testing.T) {
	self := "FROM docker.notusmi.com/library/rust:1.98 AS build\nRUN cargo build --release -p tron\nFROM x\nCOPY --from=build /src/target/release/tron /tron\n"
	cases := []struct {
		name  string
		files map[string]string
		why   []string
	}{
		{"no image", map[string]string{"Cargo.toml": "[package]\nname = \"x\"\n", "src/main.rs": ""}, []string{"tracks no Dockerfile"}},
		{"no star", map[string]string{"Cargo.toml": "", "Dockerfile": "FROM x\n"}, []string{"names no star", "not a star image"}},
		{"an answers file naming nothing", map[string]string{"Cargo.toml": "", "Dockerfile": "FROM x\n", ".copier-answers.yml": "project: x\n"}, []string{"no service_name"}},
		{"a Dockerfile that compiles itself", star("tron", self), []string{"compiles itself", "release/"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, f, _ := run(t, c.files, nil)
			atomtest.Expect(t, v, checks.StatePass, atomtest.Absent, c.why...)
			if strings.Contains(v.Reason, "compiles itself") != (c.name == "a Dockerfile that compiles itself") {
				t.Errorf("the star is read before the Dockerfile's ask: %s", v.Reason)
			}
			if len(f.Calls) != 0 {
				t.Errorf("nothing compiles for an absence: %v", f.Calls)
			}
		})
	}
}

// EVERY WAY THE RELEASE BUILD CANNOT ANSWER IS A COULD-NOT-RUN THAT SAYS WHY.
func TestRustReleaseSaysWhyItCouldNotRun(t *testing.T) {
	v, f, _ := run(t, atomtest.Star("tron", "FROM x\nCOPY release/-flag /tron\n", map[string]string{"Cargo.toml": ""}), nil)
	atomtest.Expect(t, v, checks.StateCannotRun, atomtest.Cannot, "rust:release: CANNOT RUN", "is not a binary name")
	if len(f.Calls) != 0 {
		t.Errorf("a build that could not be planned ran: %v", f.Calls)
	}

	files := star("tron", copiesTron)
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	in, _ := atomtest.ToolTree(t, files, nil)
	if err := os.Remove(filepath.Join(in.Root, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "Dockerfile could not be read")
}

// A binary that built and would not copy is the run's, not the tree's: state 2
// with copyout's words, as the engine's copyout exec gave it.
func TestABinaryThatWillNotCopyIsCouldNotRun(t *testing.T) {
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	t.Setenv("ATOM_RELEASE_OUT", "")
	t.Setenv("CARGO_TARGET_DIR", "")
	// the binary's name is taken by a directory
	if err := os.MkdirAll(filepath.Join(work, "release", "tron"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &cargo{}
	c.t = t
	in, f := atomtest.ToolTree(t, star("tron", copiesTron), c.answer)
	f.Free = []string{"cargo", "find"}
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "could not copy")
	if len(builds(f)) != 1 {
		t.Errorf("builds %v", f.Calls)
	}
}
