package atoms

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

var facadeRow = checks.AtomDef{ID: "x:facade", Stage: checks.StagePrepush, Lane: checks.LaneRust}

func facadeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func metaAt(root string, dirs ...string) string {
	type pkg struct {
		ID           string              `json:"id"`
		ManifestPath string              `json:"manifest_path"`
		Features     map[string][]string `json:"features"`
	}
	var pkgs []pkg
	var ids []string
	for _, d := range dirs {
		manifest := filepath.Join(root, d, "Cargo.toml")
		pkgs = append(pkgs, pkg{ID: manifest, ManifestPath: manifest, Features: map[string][]string{"wit-guest-a": {}}})
		ids = append(ids, manifest)
	}
	out, _ := json.Marshal(map[string]any{"packages": pkgs, "workspace_members": ids, "workspace_root": root})
	return string(out)
}

func TestAPackageRootIsItsOwnFacadeAndRunsNothing(t *testing.T) {
	root := facadeTree(t, map[string]string{"Cargo.toml": "[package]\nname = \"x\"\n\n[workspace]\n"})
	ran := false
	in := Input{Root: root, Exec: func(context.Context, Cmd) (string, int) { ran = true; return "", 0 }}
	f, bad := ResolveWitFacade(t.Context(), in, facadeRow)
	if bad != nil || f.Dir != "" || f.Virtual || !strings.Contains(f.Manifest, "[package]") || ran {
		t.Fatalf("%+v %v ran=%v", f, bad, ran)
	}
	if f.Abs(root) != root || f.Rel("tests/a.json") != "tests/a.json" {
		t.Errorf("%q %q", f.Abs(root), f.Rel("tests/a.json"))
	}
	if rel, ok := f.Within("any/file"); !ok || rel != "any/file" {
		t.Errorf("%q %v", rel, ok)
	}
}

func TestARootThatWillNotReadIsACouldNotRun(t *testing.T) {
	in := Input{Root: t.TempDir()}
	if _, bad := ResolveWitFacade(t.Context(), in, facadeRow); bad == nil || bad.State != 2 || !strings.Contains(bad.Reason, "x:facade: CANNOT RUN - Cargo.toml would not read") {
		t.Errorf("%+v", bad)
	}
}

func TestAVirtualRootAsksCargoMetadataAndReadsTheFacade(t *testing.T) {
	root := facadeTree(t, map[string]string{"Cargo.toml": "[workspace]\nmembers = [\"rust\"]\n", "rust/Cargo.toml": "[package]\nname = \"facade\"\n"})
	var got Cmd
	in := Input{Root: root, Exec: func(_ context.Context, c Cmd) (string, int) { got = c; return metaAt(root, "rust"), 0 }}
	f, bad := ResolveWitFacade(t.Context(), in, facadeRow)
	if bad != nil || f.Dir != "rust" || !f.Virtual || !strings.Contains(f.Manifest, "facade") {
		t.Fatalf("%+v %v", f, bad)
	}
	if got.Name != "cargo" || strings.Join(got.Args, " ") != "metadata --no-deps --format-version 1" || got.Dir != root || !got.StdoutOnly {
		t.Errorf("%+v", got)
	}
	if f.Abs(root) != filepath.Join(root, "rust") || f.Rel("tools/x") != "rust/tools/x" {
		t.Errorf("%q %q", f.Abs(root), f.Rel("tools/x"))
	}
	if rel, ok := f.Within("rust/tests/a.json"); !ok || rel != "tests/a.json" {
		t.Errorf("%q %v", rel, ok)
	}
	if _, ok := f.Within("go/a.go"); ok {
		t.Error("a file outside the facade is not within it")
	}
}

func TestAVirtualRootWithNoFacadeAnswersEmptyAndVirtual(t *testing.T) {
	root := facadeTree(t, map[string]string{"Cargo.toml": "[workspace]\n"})
	in := Input{Root: root, Exec: func(context.Context, Cmd) (string, int) { return metaAt(root), 0 }}
	f, bad := ResolveWitFacade(t.Context(), in, facadeRow)
	if bad != nil || !f.Virtual || f.Manifest != "" || f.Dir != "" {
		t.Errorf("%+v %v", f, bad)
	}
}

func TestAVirtualRootWhoseFacadeCannotBeFoundIsACouldNotRun(t *testing.T) {
	root := facadeTree(t, map[string]string{"Cargo.toml": "[workspace]\n"})
	for name, tc := range map[string]struct {
		exec func(context.Context, Cmd) (string, int)
		want string
	}{
		"never ran":      {func(context.Context, Cmd) (string, int) { return "cargo: not found", -1 }, "the atom never ran"},
		"exits non-zero": {func(context.Context, Cmd) (string, int) { return "", 101 }, "cargo metadata exited 101"},
		"not json":       {func(context.Context, Cmd) (string, int) { return "nope", 0 }, "cargo metadata did not parse"},
		"facade missing": {func(context.Context, Cmd) (string, int) { return metaAt(root, "rust"), 0 }, "facade's Cargo.toml would not read"},
	} {
		_, bad := ResolveWitFacade(t.Context(), Input{Root: root, Exec: tc.exec}, facadeRow)
		if bad == nil || bad.State != 2 || !strings.Contains(bad.Reason, tc.want) {
			t.Errorf("%s: %+v", name, bad)
		}
	}
}
