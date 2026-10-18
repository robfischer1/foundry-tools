package witreplay

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

// The body is exercised end to end by the gate atom's own tests
// (atoms/rust/wit-compose); these hold it to being parameterised by the row, in
// its own package, where its coverage is counted.

var row = checks.AtomDef{ID: "x:replay", Stage: "dev", Lane: checks.LaneRust}

type rule struct {
	needle, out string
	code        int
}

type fake struct {
	rules []rule
	runs  []string
	after func()
}

func (f *fake) exec(_ context.Context, c atoms.Cmd) (string, int) {
	line := strings.Join(append([]string{c.Name}, c.Args...), " ")
	f.runs = append(f.runs, line)
	for _, r := range f.rules {
		if strings.Contains(line, r.needle) {
			return r.out, r.code
		}
	}
	if i := slices.Index(c.Args, "--target-dir"); c.Name == "cargo" && i >= 0 {
		host := filepath.Join(c.Args[i+1], "release", "replay")
		_ = os.MkdirAll(filepath.Dir(host), 0o755)
		_ = os.WriteFile(host, []byte("host"), 0o644)
		if f.after != nil {
			f.after()
		}
	}
	return "", 0
}

const two = "[package]\nname = \"s\"\n\n[features]\ndefault = []\nwit-guest = [\"wit-guest-identity\"]\nwit-guest-identity = [\"dep:wit-bindgen\"]\n"

func tree(extra map[string]string) map[string]string {
	t := map[string]string{
		"Cargo.toml": two, "tools/compose/compose.sh": "x", "tools/replay/Cargo.toml": "x",
		"tests/tapes/identity.json": "{}",
	}
	for k, v := range extra {
		t[k] = v
	}
	return t
}

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

func TestEveryReasonLeadsWithTheRowsId(t *testing.T) {
	ctx := t.Context()
	// absent: no world, then no composer
	if v := Run(ctx, input(t, &fake{}, map[string]string{"Cargo.toml": "[package]\nname = \"x\"\n"}), row); v.Result != "absent" || !strings.HasPrefix(v.Reason, "x:replay: ABSENT") {
		t.Errorf("%+v", v)
	}
	if v := Run(ctx, input(t, &fake{}, map[string]string{"Cargo.toml": two}), row); v.Result != "absent" || !strings.HasPrefix(v.Reason, "x:replay: ABSENT") {
		t.Errorf("%+v", v)
	}
	// no tape for any world
	noTape := tree(nil)
	delete(noTape, "tests/tapes/identity.json")
	if v := Run(ctx, input(t, &fake{}, noTape), row); v.State != 2 || !strings.HasPrefix(v.Reason, "x:replay: CANNOT RUN") {
		t.Errorf("%+v", v)
	}
	// a tree that will not enumerate, a manifest that will not read
	in := input(t, &fake{}, tree(nil))
	in.FilesErr = errors.New("gone")
	if v := Run(ctx, in, row); v.State != 2 {
		t.Errorf("%+v", v)
	}
	in = input(t, &fake{}, tree(nil))
	_ = os.Remove(filepath.Join(in.Root, "Cargo.toml"))
	if v := Run(ctx, in, row); v.State != 2 || !strings.HasPrefix(v.Reason, "x:replay: CANNOT RUN") || !strings.Contains(v.Reason, "Cargo.toml would not read") {
		t.Errorf("%+v", v)
	}
	// provisioning
	for _, needle := range []string{"rustup target add", "wasm-tools --version", "find"} {
		if v := Run(ctx, input(t, &fake{rules: []rule{{needle, "no", 1}}}, tree(nil)), row); v.State != 2 {
			t.Errorf("%s: %+v", needle, v)
		}
	}
}

func TestEachStepFailureNamesTheStepAndTheRow(t *testing.T) {
	for _, s := range []struct{ name, needle string }{
		{"replay host", "cargo build"}, {"compose", "compose.sh"}, {"replay", "replay-host/replay"},
	} {
		v := Run(t.Context(), input(t, &fake{rules: []rule{{s.needle, "boom", 1}}}, tree(map[string]string{"tests/red/a.json": "{}"})), row)
		if v.State != 1 || !strings.HasPrefix(v.Reason, "x:replay ["+s.name+"]") || v.Logs[0] != "failed at "+s.name {
			t.Errorf("%s: %+v", s.name, v)
		}
	}
}

func TestAnOutputThatCannotBeClearedOrCarriedIsACouldNotRun(t *testing.T) {
	f := &fake{}
	in := input(t, f, tree(nil))
	built := filepath.Join(os.Getenv("CARGO_TARGET_DIR"), checks.WitReplayDir, "release", "replay")
	if err := os.MkdirAll(filepath.Join(built, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if v := Run(t.Context(), in, row); v.State != 2 || !strings.Contains(v.Reason, "could not clear") {
		t.Errorf("%+v", v)
	}
	f = &fake{}
	in = input(t, f, tree(nil))
	host := filepath.Join(os.Getenv("ATOM_WORK"), checks.WitReplayDir, "replay")
	f.after = func() { _ = os.MkdirAll(filepath.Join(host, "x"), 0o755) }
	if v := Run(t.Context(), in, row); v.State != 2 || !strings.Contains(v.Reason, "x:replay [replay host]") || !strings.Contains(v.Reason, "would not copy out") {
		t.Errorf("%+v", v)
	}
}

func TestAPassRunsAllThreeStepsInOrder(t *testing.T) {
	f := &fake{}
	if v := Run(t.Context(), input(t, f, tree(nil)), row); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	var order []string
	for _, r := range f.runs {
		switch {
		case strings.HasPrefix(r, "cargo build"):
			order = append(order, "host")
		case strings.Contains(r, "compose.sh"):
			order = append(order, "compose")
		case strings.Contains(r, "replay-host/replay"):
			order = append(order, "replay")
		}
	}
	if !slices.Equal(order, []string{"host", "compose", "replay"}) {
		t.Errorf("%v", f.runs)
	}
}
