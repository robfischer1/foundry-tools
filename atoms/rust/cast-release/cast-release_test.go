package rustcastrelease

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
)

func TestDEF_B6_cast_RustDeclarationValidatesAndRunsOnlyInTheCastStage(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StageCast || checks.IsPullPath(Def.Stage) || Def.Lane != checks.LaneRust ||
		Def.Toolchain != "rust" || Def.Surface != "root:Cargo.toml" || !Def.WritesTree || Def.NotOnForks != "" || len(Def.Needs) != 0 {
		t.Errorf("%+v", Def)
	}
}

func TestDEF_B6_cast_RustCheckCompilesTheWorkspace(t *testing.T) {
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	t.Setenv("ATOM_RELEASE_OUT", "")
	t.Setenv("CARGO_TARGET_DIR", filepath.Join(work, "target"))
	in, f := atomtest.ToolTree(t, map[string]string{"Cargo.toml": "[package]\nname = \"tongs\"\n"}, func(c atoms.Cmd) (string, int) {
		switch {
		case c.Name == "cargo" && c.Args[0] == "metadata":
			return `{"packages":[{"id":"p","targets":[{"name":"tongs","kind":["bin"]}]}],"workspace_default_members":["p"]}`, 0
		case c.Name == "cargo" && c.Args[0] == "build":
			p := filepath.Join(work, "target", "release", "tongs")
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("ELF"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return "", 0
	})
	f.Free = []string{"cargo", "find"}
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass, "tongs")
	if _, err := os.Stat(filepath.Join(work, "release", "tongs")); err != nil {
		t.Error(err)
	}
}
