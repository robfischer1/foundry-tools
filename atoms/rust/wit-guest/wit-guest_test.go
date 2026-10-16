package rustwitguest

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// rule answers an exec whose argv line contains the needle.
type rule struct {
	needle string
	out    string
	code   int
}

// fake is an Exec that records every run (argv line, environment) and answers
// from its rules, first match wins; anything else exits 0. A cargo build leaves
// the module where cargo would, so the atom has one to carry out.
type fake struct {
	// after runs once a cargo build has left its module, to sabotage what follows
	after func()
	rules []rule
	runs  []string
	envs  map[string][]string
}

func (f *fake) exec(_ context.Context, c atoms.Cmd) (string, int) {
	line := strings.Join(append([]string{c.Name}, c.Args...), " ")
	f.runs = append(f.runs, line)
	if f.envs == nil {
		f.envs = map[string][]string{}
	}
	f.envs[line] = c.Env
	for _, r := range f.rules {
		if strings.Contains(line, r.needle) {
			return r.out, r.code
		}
	}
	if i := slices.Index(c.Args, "--target-dir"); c.Name == "cargo" && c.Args[0] == "build" && i >= 0 {
		module := filepath.Join(c.Args[i+1], checks.WitGuestTarget, "release", "stellar_core.wasm")
		_ = os.MkdirAll(filepath.Dir(module), 0o755)
		_ = os.WriteFile(module, []byte("\x00asm"), 0o644)
		if f.after != nil {
			f.after()
		}
	}
	return "", 0
}

func (f *fake) ran(needle string) bool {
	return slices.ContainsFunc(f.runs, func(r string) bool { return strings.Contains(r, needle) })
}

func (f *fake) index(needle string) int {
	return slices.IndexFunc(f.runs, func(r string) bool { return strings.Contains(r, needle) })
}

const (
	one = "[package]\nname = \"stellar-core\"\n\n[features]\ndefault = []\nwit-guest = [\"dep:wit-bindgen\"]\n"
	two = "[package]\nname = \"stellar-core\"\n\n[features]\ndefault = []\nwit-guest = [\"wit-guest-identity\"]\nwit-guest-identity = [\"dep:wit-bindgen\"]\nwit-guest-reader = [\"dep:wit-bindgen\"]\n"

	build = "cargo build --locked --release"
	lift  = "wasm-tools component new"
)

// input is a tree whose Cargo.toml is the manifest, over a target dir and a work
// area of the test's own.
func input(t *testing.T, f *fake, manifest string) atomkit.In {
	t.Helper()
	t.Setenv("CARGO_TARGET_DIR", filepath.Join(t.TempDir(), "target"))
	t.Setenv("ATOM_WORK", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return atomkit.In{Root: root, Files: []string{"Cargo.toml"}, Exec: f.exec}
}

func TestTheDeclarationValidatesAndNamesItsRow(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrepush || Def.Lane != checks.LaneRust || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "rust" || Def.Surface != "root:Cargo.toml" || Def.NotOnForks != "" || Def.SubsumedBy != "" ||
		!Def.WritesTree || !Def.NeedsManifest || !slices.Equal(Def.Needs, []string{"tool:wasm-tools"}) {
		t.Errorf("%+v", Def)
	}
}

// DEF-B4-wit-1: the atom needs no other atom's output and no volume: its Needs
// are a tool, not a handoff.
func TestTheAtomNeedsNoOtherAtomsOutput(t *testing.T) {
	for _, n := range Def.Needs {
		if !strings.HasPrefix(n, "tool:") {
			t.Errorf("need %q is not a tool", n)
		}
	}
}

func TestAbsentWithoutAWorld(t *testing.T) {
	f := &fake{}
	v := check(t.Context(), input(t, f, "[package]\nname = \"x\"\n\n[features]\ndefault = []\nwit = []\n"))
	if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "rust:wit-guest: ABSENT - Cargo.toml declares no wit-guest-<world> feature") || len(f.runs) != 0 {
		t.Errorf("%+v ran %v", v, f.runs)
	}
}

func TestBuildsLiftsValidatesAndReadsBackTheWorld(t *testing.T) {
	f := &fake{rules: []rule{{"component wit", "world identity-core", 0}}}
	in := input(t, f, one)
	v := check(t.Context(), in)
	if v.State != 0 || !strings.Contains(strings.Join(v.Logs, "\n"), "world identity-core") {
		t.Fatalf("%+v", v)
	}
	target := os.Getenv("CARGO_TARGET_DIR")
	work := os.Getenv("ATOM_WORK")
	module, component := filepath.Join(work, "wit-guest.wasm"), filepath.Join(work, "wit-guest.component.wasm")
	want := []string{
		"rustup target add " + checks.WitGuestTarget,
		"wasm-tools --version",
		build + " --no-default-features --features wit-guest --target " + checks.WitGuestTarget + " --target-dir " + target,
		lift + " " + module + " -o " + component,
		"wasm-tools validate " + component,
		"wasm-tools component wit " + component,
		"cargo test --locked --workspace --lib --no-default-features --features wit-guest --target-dir " + filepath.Join(target, checks.WitGuestTestDir),
	}
	var got []string
	for _, r := range f.runs {
		if !strings.HasPrefix(r, "touch") && !strings.HasPrefix(r, "find") {
			got = append(got, r)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("ran\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// the module is carried out of the target, so the next world's build cannot replace it
	if _, err := os.Stat(module); err != nil {
		t.Errorf("the module was not carried out of the target: %v", err)
	}
	// the stamp comes first, on the tree cargo will read
	if f.index("touch") < 0 || f.index("touch") > f.index("rustup") {
		t.Errorf("the stamp must come before everything: %v", f.runs)
	}
}

// DEF-B4-wit-1: the atom builds where CARGO_TARGET_DIR says (the pod's own), the
// native test in a directory of its own under it, and never in a path it knows.
func TestBuildsIntoTheTargetDirTheEnvironmentNames(t *testing.T) {
	f := &fake{}
	in := input(t, f, one)
	if v := check(t.Context(), in); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	target := os.Getenv("CARGO_TARGET_DIR")
	for _, r := range f.runs {
		if strings.HasPrefix(r, "cargo") && !strings.Contains(r, target) {
			t.Errorf("%q builds outside %s", r, target)
		}
		if strings.Contains(r, checks.ReleaseCachePath) {
			t.Errorf("%q names the Dagger release volume", r)
		}
	}
}

func TestWithoutATargetDirTheAtomBuildsInItsWorkArea(t *testing.T) {
	f := &fake{}
	in := input(t, f, one)
	t.Setenv("CARGO_TARGET_DIR", "")
	if v := check(t.Context(), in); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	want := filepath.Join(os.Getenv("ATOM_WORK"), "cargo-target")
	if !f.ran("--target-dir " + want) {
		t.Errorf("not built in %s: %v", want, f.runs)
	}
}

// The compile is bounded, as the chain's container was.
func TestEveryCargoRunIsBounded(t *testing.T) {
	f := &fake{}
	in := input(t, f, one)
	if v := check(t.Context(), in); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	for line, env := range f.envs {
		if !strings.HasPrefix(line, "cargo") {
			continue
		}
		for _, e := range checks.CargoBoundedEnv() {
			if !slices.Contains(env, e) {
				t.Errorf("%q ran without %s: %v", line, e, env)
			}
		}
	}
}

func TestTheArtifactNameComesOffTheManifest(t *testing.T) {
	f := &fake{}
	in := input(t, f, strings.Replace(one, "[features]", "[lib]\nname = \"core-guest\"\n\n[features]", 1))
	// the fake writes stellar_core.wasm; the atom clears and reads core_guest.wasm
	target := os.Getenv("CARGO_TARGET_DIR")
	stale := filepath.Join(target, checks.WitGuestTarget, "release", "core_guest.wasm")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old tree"), 0o644); err != nil {
		t.Fatal(err)
	}
	if v := check(t.Context(), in); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	// a module the build did not produce this time is absent, never the old tree's
	if _, err := os.Stat(filepath.Join(os.Getenv("ATOM_WORK"), "wit-guest.wasm")); err == nil {
		t.Errorf("a stale module was carried out")
	}
}

func TestEachStepsExitIsMapped(t *testing.T) {
	for _, tc := range []struct {
		name, needle string
		code, state  int
	}{
		{"the build fails", build, 1, 1},
		{"a compile error, cargo's 101", build, 101, 1},
		{"cargo cannot run", build, 127, 2},
		{"the module will not lift", lift, 1, 1},
		{"the component does not validate", "wasm-tools validate", 1, 1},
		{"the world cannot be read back", "component wit", 1, 1},
		{"a step killed by a signal is not a finding", "wasm-tools validate", 137, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{rules: []rule{{tc.needle, "boom", tc.code}}}
			if v := check(t.Context(), input(t, f, one)); v.State != tc.state {
				t.Errorf("%+v", v)
			}
		})
	}

	// A step that failed goes no further.
	for _, c := range [][2]string{{build, lift}, {lift, "wasm-tools validate"}, {"wasm-tools validate", "component wit"}, {"component wit", "cargo test"}} {
		f := &fake{rules: []rule{{c[0], "", 1}}}
		check(t.Context(), input(t, f, one))
		if f.ran(c[1]) {
			t.Errorf("%s failed and %s ran anyway: %v", c[0], c[1], f.runs)
		}
	}
}

func TestRefusalsAreCouldNotRuns(t *testing.T) {
	v := check(t.Context(), input(t, &fake{}, "[features]\nwit-guest = []\n"))
	if v.State != 2 || !strings.Contains(v.Reason, "CANNOT RUN") || !strings.Contains(v.Reason, "names no [package] or [lib] name") {
		t.Errorf("%+v", v)
	}

	in := input(t, &fake{}, one)
	if err := os.Remove(filepath.Join(in.Root, "Cargo.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(in.Root, "Cargo.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	v = check(t.Context(), in)
	if v.State != 2 || !strings.Contains(v.Reason, "Cargo.toml would not read") {
		t.Errorf("%+v", v)
	}

	// provisioning that fails is the atom never running, not a finding about the tree
	for _, tc := range []struct{ needle, word string }{
		{"rustup target add", "unreachable"},
		{"wasm-tools --version", "wasm-tools"},
		{"touch", "preflight"},
	} {
		f := &fake{rules: []rule{{tc.needle, "static.rust-lang.org unreachable", 1}}}
		v = check(t.Context(), input(t, f, one))
		if v.State != 2 || !strings.Contains(v.Reason+strings.Join(v.Logs, "\n"), tc.word) || f.ran(build) {
			t.Errorf("%s: %+v ran %v", tc.needle, v, f.runs)
		}
	}
	f := &fake{rules: []rule{{"rustup", "exec: rustup: not found", -1}}}
	if v = check(t.Context(), input(t, f, one)); v.State != 2 || !strings.Contains(v.Reason, "the atom never ran") {
		t.Errorf("%+v", v)
	}
}

// Two worlds and the alias: exactly the two are built, the alias never is, each
// world gets its steps, and the native test runs once with both features.
func TestEveryWorldIsGradedAndNotTheAlias(t *testing.T) {
	f := &fake{rules: []rule{{"component wit", "world w", 0}}}
	v := check(t.Context(), input(t, f, two))
	if v.State != 0 {
		t.Fatalf("%+v", v)
	}
	for _, w := range []string{"wit-guest-identity", "wit-guest-reader"} {
		if !f.ran(build + " --no-default-features --features " + w + " ") {
			t.Errorf("%s: no wasm32 build: %v", w, f.runs)
		}
		if !strings.Contains(strings.Join(v.Logs, "\n"), "["+w+"] ") {
			t.Errorf("%s: pass logs name the world, got %q", w, v.Logs)
		}
	}
	if f.ran("--features wit-guest ") {
		t.Errorf("the bare alias must not be graded when a world exists: %v", f.runs)
	}
	tests := 0
	for _, r := range f.runs {
		if strings.HasPrefix(r, "cargo test") {
			tests++
			if !strings.Contains(r, "--features wit-guest-identity,wit-guest-reader --target-dir") || !strings.Contains(r, "--workspace --lib --no-default-features") {
				t.Errorf("the one native test carries both features, --lib only: %s", r)
			}
		}
	}
	if tests != 1 {
		t.Errorf("a green combined run is one run, got %d", tests)
	}
}

// A red names the world and the step; a combination nobody reproduces names the combination.
func TestFindingsNameTheFailingWorld(t *testing.T) {
	const testRun = "cargo test --locked --workspace --lib --no-default-features --features "
	const both = "wit-guest-identity,wit-guest-reader"
	for _, c := range []struct {
		name, world, step string
		red               []string
	}{
		{"reader build", "wit-guest-reader", "build", []string{build + " --no-default-features --features wit-guest-reader "}},
		{"reader test", "wit-guest-reader", "test", []string{testRun + both, testRun + "wit-guest-reader "}},
		{"identity test", "wit-guest-identity", "test", []string{testRun + both, testRun + "wit-guest-identity "}},
		{"combination only", "all wit-guest-identity+wit-guest-reader", "test", []string{testRun + both}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fake{}
			for _, r := range c.red {
				f.rules = append(f.rules, rule{r, "FAILED", 101})
			}
			v := check(t.Context(), input(t, f, two))
			if v.State != 1 || !strings.Contains(v.Reason, "world "+c.world) || !strings.Contains(v.Reason, c.step) {
				t.Fatalf("%+v", v)
			}
			if len(v.Logs) == 0 || !strings.Contains(v.Logs[0], c.world) {
				t.Errorf("first log line names the world, got %q", v.Logs)
			}
		})
	}

	// A world that fails its build stops the atom before the native test.
	f := &fake{rules: []rule{{build + " --no-default-features --features wit-guest-identity ", "", 101}}}
	check(t.Context(), input(t, f, two))
	if f.ran("wit-guest-reader") || f.ran("cargo test") {
		t.Errorf("a failed build goes no further: %v", f.runs)
	}
}

// The alias alone, with no wit-guest-<world>, is itself the one world.
func TestTheAliasAloneIsTheOneWorld(t *testing.T) {
	f := &fake{}
	if v := check(t.Context(), input(t, f, one)); v.State != 0 || !f.ran("--features wit-guest ") || !f.ran("cargo test") {
		t.Errorf("%+v ran %v", v, f.runs)
	}
}

// Only the stamp lets the native test see a module added since the volume was
// last written: the stamp runs on the tree before any cargo run.
func TestTheStampPrecedesEveryCargoRun(t *testing.T) {
	f := &fake{}
	check(t.Context(), input(t, f, two))
	stamp := f.index("touch")
	for i, r := range f.runs {
		if strings.HasPrefix(r, "cargo") && i < stamp {
			t.Errorf("cargo ran before the stamp: %v", f.runs)
		}
	}
}

// An output that cannot be cleared before the build, or carried out after it, is
// the atom's could-not-run: it never judged the tree.
func TestAnOutputThatCannotBeClearedOrCarriedIsACouldNotRun(t *testing.T) {
	// a module path that is a non-empty directory cannot be cleared
	f := &fake{}
	in := input(t, f, one)
	blocked := filepath.Join(os.Getenv("ATOM_WORK"), "wit-guest.wasm")
	if err := os.MkdirAll(filepath.Join(blocked, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	v := check(t.Context(), in)
	if v.State != 2 || !strings.Contains(v.Reason, "could not clear") || f.ran(build) {
		t.Errorf("%+v ran %v", v, f.runs)
	}

	// a module path that becomes a non-empty directory under the build cannot be written
	f = &fake{}
	in = input(t, f, one)
	blocked = filepath.Join(os.Getenv("ATOM_WORK"), "wit-guest.wasm")
	f.after = func() { _ = os.MkdirAll(filepath.Join(blocked, "x"), 0o755) }
	v = check(t.Context(), in)
	if v.State != 2 || !strings.Contains(v.Reason, "the module would not copy out") || !strings.Contains(v.Reason, "[world wit-guest, build]") || f.ran(lift) {
		t.Errorf("%+v ran %v", v, f.runs)
	}
}
