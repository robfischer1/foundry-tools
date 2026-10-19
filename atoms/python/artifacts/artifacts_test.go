package pythonartifacts

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

func TestDEF_B6_publish_PythonBuildDeclarationRunsOnlyInThePublishStage(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != "python:artifacts" || Def.Stage != checks.StagePublish || checks.IsPullPath(Def.Stage) || Def.Lane != checks.LanePython ||
		Def.Toolchain != "python" || Def.Surface != "root:pyproject.toml" || !slices.Equal(Def.Needs, []string{"tool:uv"}) || Def.NotOnForks != "" {
		t.Errorf("%+v", Def)
	}
}

func TestDEF_B6_publish_PythonBuildCheckBuilds(t *testing.T) {
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	t.Setenv("ATOM_RELEASE_OUT", "")
	in, f := atomtest.ToolTree(t, map[string]string{"pyproject.toml": "[project]\n"}, func(c atoms.Cmd) (string, int) {
		if err := os.MkdirAll(c.Args[len(c.Args)-1], 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(c.Args[len(c.Args)-1], "a-1.tar.gz"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		return "", 0
	})
	f.Free = []string{"uv"}
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass, "a-1.tar.gz")
}
