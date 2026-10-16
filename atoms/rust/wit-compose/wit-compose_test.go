package rustwitcompose

import (
	"context"
	"errors"
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
// from its rules, first match wins; anything else exits 0. The replay host's
// build leaves the host where cargo would.
type fake struct {
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
	if i := slices.Index(c.Args, "--target-dir"); c.Name == "cargo" && i >= 0 {
		host := filepath.Join(c.Args[i+1], "release", "replay")
		_ = os.MkdirAll(filepath.Dir(host), 0o755)
		_ = os.WriteFile(host, []byte("host"), 0o644)
	}
	return "", 0
}

func (f *fake) ran(needle string) bool { return f.index(needle) >= 0 }

func (f *fake) index(needle string) int {
	return slices.IndexFunc(f.runs, func(r string) bool { return strings.Contains(r, needle) })
}

const two = "[package]\nname = \"stellar-core\"\n\n[features]\ndefault = []\nwit-guest = [\"wit-guest-identity\"]\nwit-guest-identity = [\"dep:wit-bindgen\"]\nwit-guest-reader = [\"dep:wit-bindgen\"]\n"

const (
	hostBuild = "cargo build --locked --release --manifest-path tools/replay/Cargo.toml"
	compose   = "tools/compose/compose.sh"
	replay    = "replay-host/replay"
)

// tree is the files a composing crate carries, and extra on top.
func tree(extra map[string]string) map[string]string {
	t := map[string]string{
		"Cargo.toml":                two,
		"tools/compose/compose.sh":  "#!/usr/bin/env bash\n",
		"tools/replay/Cargo.toml":   "[package]\nname = \"replay\"\n",
		"tests/tapes/identity.json": "{}",
		"tests/tapes/reader.json":   "{}",
		"tests/tapes/promote.json":  "{}",
	}
	for k, v := range extra {
		t[k] = v
	}
	return t
}

// input lays files on disk and answers the population they make, over a target
// dir and a work area of the test's own.
func input(t *testing.T, f *fake, files map[string]string) atomkit.In {
	t.Helper()
	t.Setenv("CARGO_TARGET_DIR", filepath.Join(t.TempDir(), "target"))
	t.Setenv("ATOM_WORK", t.TempDir())
	root := t.TempDir()
	var names []string
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return atomkit.In{Root: root, Files: names, Exec: f.exec}
}

func TestTheDeclarationValidatesAndNamesItsRow(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrepush || Def.Lane != checks.LaneRust || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "rust" || Def.Surface != "root:Cargo.toml" || Def.NotOnForks != "" || Def.SubsumedBy != "" ||
		!Def.WritesTree || !Def.NeedsManifest || !slices.Equal(Def.Needs, []string{"tool:wasm-tools", "tool:wac"}) {
		t.Errorf("%+v", Def)
	}
}

func TestAbsentWithoutTheComposerOrAWorld(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"no world":    tree(map[string]string{"Cargo.toml": "[package]\nname = \"x\"\n\n[features]\ndefault = []\n"}),
		"no composer": {"Cargo.toml": two, "tools/replay/Cargo.toml": ""},
		"no replay":   {"Cargo.toml": two, "tools/compose/compose.sh": ""},
	} {
		f := &fake{}
		v := check(t.Context(), input(t, f, files))
		if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "rust:wit-compose: ABSENT") || len(f.runs) != 0 {
			t.Errorf("%s: %+v ran %v", name, v, f.runs)
		}
	}
}

func TestBuildsComposesAndReplaysTheWorldsTapes(t *testing.T) {
	f := &fake{rules: []rule{{replay, "TOTAL 1371 pass, 0 fail", 0}}}
	in := input(t, f, tree(nil))
	v := check(t.Context(), in)
	if v.State != 0 || !strings.Contains(strings.Join(v.Logs, "\n"), "TOTAL 1371 pass, 0 fail") {
		t.Fatalf("%+v", v)
	}
	target, work := os.Getenv("CARGO_TARGET_DIR"), os.Getenv("ATOM_WORK")
	host, composed := filepath.Join(work, "replay-host", "replay"), filepath.Join(work, checks.WitComposedName)
	var got []string
	for _, r := range f.runs {
		if !strings.HasPrefix(r, "find") {
			got = append(got, r)
		}
	}
	want := []string{
		"rustup target add " + checks.WitGuestTarget,
		"wasm-tools --version",
		"wac --version",
		hostBuild + " --target-dir " + filepath.Join(target, "replay-host"),
		compose + " " + composed,
		// The tapes are the discovered worlds' own: identity and reader, not promote.
		host + " " + composed + " tests/tapes/identity.json tests/tapes/reader.json",
	}
	if !slices.Equal(got, want) {
		t.Errorf("ran\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if _, err := os.Stat(host); err != nil {
		t.Errorf("the replay host was not carried out of the target: %v", err)
	}
	// compose.sh builds the guests into the target it is given
	for _, line := range []string{hostBuild + " --target-dir " + filepath.Join(target, "replay-host"), compose + " " + composed} {
		if !slices.Contains(f.envs[line], "CARGO_TARGET_DIR="+target) {
			t.Errorf("%q ran without CARGO_TARGET_DIR=%s: %v", line, target, f.envs[line])
		}
	}
	for _, e := range checks.CargoBoundedEnv() {
		if !slices.Contains(f.envs[compose+" "+composed], e) {
			t.Errorf("compose ran without %s", e)
		}
	}
	if f.index("find") > f.index("rustup") || f.index("find") < 0 {
		t.Errorf("the stamp comes before everything: %v", f.runs)
	}
}

// DEF-B4-wit-1: nothing this atom reads was written by another atom, and it names
// no path outside the target dir and work area it was given.
func TestItReadsNothingAnotherAtomWrote(t *testing.T) {
	f := &fake{}
	if v := check(t.Context(), input(t, f, tree(nil))); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	for _, r := range f.runs {
		if strings.Contains(r, checks.ReleaseCachePath) || strings.Contains(r, "wit-guest") {
			t.Errorf("%q names the Dagger release volume or the guest atom's output", r)
		}
	}
	for _, n := range Def.Needs {
		if !strings.HasPrefix(n, "tool:") {
			t.Errorf("need %q is not a tool", n)
		}
	}
}

func TestWithoutATargetDirTheAtomBuildsInItsWorkArea(t *testing.T) {
	f := &fake{}
	in := input(t, f, tree(nil))
	t.Setenv("CARGO_TARGET_DIR", "")
	if v := check(t.Context(), in); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	if !f.ran(filepath.Join(os.Getenv("ATOM_WORK"), "cargo-target")) {
		t.Errorf("%v", f.runs)
	}
}

func TestEachStepsExitIsMappedAndStops(t *testing.T) {
	for _, s := range []struct{ name, needle, next string }{
		{"replay host", hostBuild, compose},
		{"compose", compose, replay},
		{"replay", replay, ""},
	} {
		t.Run(s.name, func(t *testing.T) {
			f := &fake{rules: []rule{{s.needle, "boom", 1}}}
			v := check(t.Context(), input(t, f, tree(nil)))
			if v.State != 1 || !strings.Contains(v.Reason, "["+s.name+"]") || len(v.Logs) == 0 || !strings.Contains(v.Logs[0], s.name) {
				t.Errorf("%+v", v)
			}
			if s.next != "" && f.ran(s.next) {
				t.Errorf("%s failed and %s ran anyway: %v", s.name, s.next, f.runs)
			}
			f = &fake{rules: []rule{{s.needle, "", 137}}}
			if v = check(t.Context(), input(t, f, tree(nil))); v.State != 2 {
				t.Errorf("%+v", v)
			}
		})
	}
}

func TestRefusalsAreCouldNotRuns(t *testing.T) {
	// a world with no tape answers nothing
	v := check(t.Context(), input(t, &fake{}, map[string]string{"Cargo.toml": two, "tools/compose/compose.sh": "", "tools/replay/Cargo.toml": "", "tests/tapes/promote.json": ""}))
	if v.State != 2 || !strings.Contains(v.Reason, "CANNOT RUN") || !strings.Contains(v.Reason, "answer nothing") {
		t.Errorf("%+v", v)
	}

	// red tapes alone do not stand in for a world's tape
	v = check(t.Context(), input(t, &fake{}, map[string]string{"Cargo.toml": two, "tools/compose/compose.sh": "", "tools/replay/Cargo.toml": "", "tests/red/tail.json": "{}"}))
	if v.State != 2 || !strings.Contains(v.Reason, "answer nothing") {
		t.Errorf("%+v", v)
	}

	// a tree that will not enumerate
	in := input(t, &fake{}, tree(nil))
	in.FilesErr = errors.New("the tree went away")
	if v = check(t.Context(), in); v.State != 2 || !strings.Contains(v.Reason, "the tree would not enumerate") {
		t.Errorf("%+v", v)
	}

	// a manifest that will not read
	in = input(t, &fake{}, tree(nil))
	if err := os.Remove(filepath.Join(in.Root, "Cargo.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(in.Root, "Cargo.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if v = check(t.Context(), in); v.State != 2 || !strings.Contains(v.Reason, "Cargo.toml would not read") {
		t.Errorf("%+v", v)
	}

	// provisioning that fails is the atom never running
	for _, needle := range []string{"rustup target add", "wasm-tools --version", "wac --version", "find"} {
		f := &fake{rules: []rule{{needle, "unreachable", 1}}}
		v = check(t.Context(), input(t, f, tree(nil)))
		if v.State != 2 || f.ran(hostBuild) {
			t.Errorf("%s: %+v ran %v", needle, v, f.runs)
		}
	}
}

// THE RED TAPES RIDE AFTER THE WORLDS' (F18): sorted, named by package, and not
// filtered by the manifest, so the replay host judges each as red.
func TestTheRedTapesRideAfterTheWorldTapes(t *testing.T) {
	f := &fake{}
	in := input(t, f, tree(map[string]string{"tests/red/tail.json": "{}", "tests/red/alarm.json": "{}", "tests/red/PROVENANCE": "x"}))
	if v := check(t.Context(), in); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	i := f.index(replay)
	if i < 0 || !strings.HasSuffix(f.runs[i], " tests/tapes/identity.json tests/tapes/reader.json tests/red/alarm.json tests/red/tail.json") {
		t.Errorf("%v", f.runs)
	}
	if strings.Contains(f.runs[i], "PROVENANCE") || strings.Contains(f.runs[i], "promote.json") {
		t.Errorf("a non-tape is not replayed: %s", f.runs[i])
	}

	// A failing red replay is the replay step's finding.
	f = &fake{rules: []rule{{replay, "red", 1}}}
	in = input(t, f, tree(map[string]string{"tests/red/tail.json": "{}"}))
	if v := check(t.Context(), in); v.State != 1 || !strings.Contains(v.Reason, "[replay]") {
		t.Errorf("%+v", v)
	}
}
