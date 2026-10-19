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
		Def.Toolchain != "go" || Def.Surface != "root:go.mod" || Def.WritesTree || Def.NotOnForks != "" || !slices.Equal(Def.Needs, []string{"dies"}) {
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

// TestDEF_B6_012_NeedsDies: the pod lays foundry-dies only for a plan whose
// atoms need it, and the cast's layer-cast reads the star's catalog entry
// there; without it the gavel parity pod settled "no record for gavel could be
// read at foundry-dies fleet/repos/gavel.json".
func TestDEF_B6_012_NeedsDies(t *testing.T) {
	if !slices.Contains(Def.Needs, "dies") {
		t.Errorf("DEF-B6-012: %s needs %v, want dies among them", Def.ID, Def.Needs)
	}
}
