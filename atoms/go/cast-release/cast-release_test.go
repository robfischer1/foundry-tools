package gocastrelease

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
)

func TestDEF_B6_cast_GoDeclarationValidatesAndRunsOnlyInTheCastStage(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StageCast || checks.IsPullPath(Def.Stage) || Def.Lane != checks.LaneGo ||
		Def.Toolchain != "go" || Def.Surface != "root:go.mod" || Def.WritesTree || Def.NotOnForks != "" || len(Def.Needs) != 0 {
		t.Errorf("%+v", Def)
	}
}

func TestDEF_B6_cast_GoCheckCompilesEveryCmd(t *testing.T) {
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	t.Setenv("ATOM_RELEASE_OUT", "")
	in, f := atomtest.ToolTree(t, map[string]string{"go.mod": "module example.com/x\n", "vendor/modules.txt": "#"}, func(c atoms.Cmd) (string, int) {
		switch {
		case c.Name == "go" && c.Args[0] == "list":
			return "main example.com/x/cmd/argus\n", 0
		case c.Name == "go" && c.Args[0] == "build":
			o := c.Args[slices.Index(c.Args, "-o")+1]
			if err := os.WriteFile(o, []byte("ELF"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return "", 0
	})
	f.Free = []string{"go"}
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass, "argus")
	if _, err := os.Stat(filepath.Join(work, "release", "argus")); err != nil {
		t.Error(err)
	}
}
