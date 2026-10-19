package tspack

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

func TestDEF_B6_publish_TSPackDeclarationRunsOnlyInThePublishStage(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != "ts:pack" || Def.Stage != checks.StagePublish || checks.IsPullPath(Def.Stage) || Def.Lane != checks.LaneTS ||
		Def.Toolchain != "bun" || Def.Surface != "root:package.json" || !Def.WritesTree || Def.NotOnForks != "" {
		t.Errorf("%+v", Def)
	}
}

func TestDEF_B6_publish_TSPackCheckPacks(t *testing.T) {
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	t.Setenv("ATOM_RELEASE_OUT", "")
	in, f := atomtest.ToolTree(t, map[string]string{"package.json": `{"name":"@f/x","version":"1"}`}, func(c atoms.Cmd) (string, int) {
		d := c.Args[len(c.Args)-1]
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "f-x-1.tgz"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		return "", 0
	})
	f.Free = []string{"bun"}
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass, "@f/x@1")
}
