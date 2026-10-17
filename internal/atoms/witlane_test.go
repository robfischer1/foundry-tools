package atoms

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

func TestCargoTargetDirHonoursTheEnvironment(t *testing.T) {
	t.Setenv("ATOM_WORK", "/work/atom")
	t.Setenv("CARGO_TARGET_DIR", "/work/pod/target")
	if got := CargoTargetDir(); got != "/work/pod/target" {
		t.Errorf("%s", got)
	}
	t.Setenv("CARGO_TARGET_DIR", "")
	if got := CargoTargetDir(); got != "/work/atom/cargo-target" {
		t.Errorf("%s", got)
	}
}

func TestWitProvisionAddsTheTargetThenProbesEachTool(t *testing.T) {
	var ran []string
	in := Input{Root: "/src", Exec: func(_ context.Context, c Cmd) (string, int) {
		ran = append(ran, strings.Join(append([]string{c.Name}, c.Args...), " "))
		return "", 0
	}}
	if v := WitProvision(t.Context(), in, checks.AtomDef{ID: "x:y"}, "wasm32-unknown-unknown", "wasm-tools", "wac"); v != nil {
		t.Fatalf("%+v", v)
	}
	want := "rustup target add wasm32-unknown-unknown|wasm-tools --version|wac --version"
	if strings.Join(ran, "|") != want {
		t.Errorf("%v", ran)
	}

	// a tool that will not answer its version is the atom never running too
	in.Exec = func(_ context.Context, c Cmd) (string, int) {
		if c.Name == "wac" {
			return "no such file", 127
		}
		return "", 0
	}
	if v := WitProvision(t.Context(), in, checks.AtomDef{ID: "x:y"}, "wasm32-unknown-unknown", "wasm-tools", "wac"); v == nil || v.State != 2 || !strings.Contains(v.Reason, "wac --version") {
		t.Errorf("%+v", v)
	}

	// a target that will not add is the atom never running; no tool is probed after it
	ran = nil
	in.Exec = func(_ context.Context, c Cmd) (string, int) {
		ran = append(ran, c.Name)
		return "offline", 1
	}
	v := WitProvision(t.Context(), in, checks.AtomDef{ID: "x:y"}, "wasm32-unknown-unknown", "wasm-tools")
	if v == nil || v.State != 2 || !strings.Contains(v.Reason, "the atom never ran") || !strings.Contains(v.Reason, "offline") || len(ran) != 1 {
		t.Errorf("%+v ran %v", v, ran)
	}
}

func TestClearOutputAndCarryOut(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "a", "m.wasm"), filepath.Join(dir, "out", "deep", "m.wasm")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ClearOutput(src); err != nil {
		t.Fatal(err)
	}
	if err := ClearOutput(src); err != nil {
		t.Errorf("an output that is not there is already clear: %v", err)
	}
	// an absent source is skipped
	if err := CarryOut(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("nothing was built and something was carried")
	}
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CarryOut(src, dst); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dst)
	fi, _ := os.Stat(dst)
	if err != nil || string(b) != "new" || fi.Mode().Perm() != 0o755 {
		t.Errorf("%q %v %v", b, fi, err)
	}

	// failures: a directory is not clearable as a file's path; a destination under a file
	if err := os.MkdirAll(filepath.Join(dir, "d", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ClearOutput(filepath.Join(dir, "d")); err == nil {
		t.Error("a non-empty directory cannot be cleared")
	}
	if err := CarryOut(src, filepath.Join(dst, "under-a-file")); err == nil {
		t.Error("a destination under a file cannot be made")
	}
	if err := CarryOut(src, filepath.Join(dir, "d")); err == nil {
		t.Error("a destination that is a directory cannot be written")
	}
	if err := CarryOut(filepath.Join(src, "under-a-file"), dst); err == nil {
		t.Error("a source under a file cannot be opened")
	}
	if err := CarryOut(filepath.Join(dir, "d"), dst); err == nil {
		t.Error("a source that is a directory cannot be copied")
	}
}

func TestCargoRunReadsCargosExitAndANeverRan(t *testing.T) {
	a := checks.AtomDef{ID: "x:y"}
	for _, tc := range []struct {
		code, state int
		word        string
	}{{0, 0, ""}, {101, 1, ""}, {1, 1, ""}, {137, 2, "the atom never ran"}, {-1, 2, "the atom never ran"}} {
		in := Input{Root: "/src", Exec: func(_ context.Context, c Cmd) (string, int) { return "out", tc.code }}
		v := CargoRun(t.Context(), in, a, []string{"A=1"}, "cargo", "build")
		if v.State != tc.state || !strings.Contains(v.Reason, tc.word) {
			t.Errorf("exit %d: %+v", tc.code, v)
		}
	}
}
