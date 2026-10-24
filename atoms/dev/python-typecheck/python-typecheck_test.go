package devpythontypecheck

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

func TestTheDeclarationKeepsTheManifestsRow(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != "dev:python-typecheck" || Def.Stage != "dev" || Def.Lane != checks.LanePython || Def.Group != checks.GroupLanguage ||
		Def.Toolchain != "python" || Def.Surface != "root:pyproject.toml" || !slices.Equal(Def.Needs, []string{"tool:uv"}) ||
		Def.WritesTree || Def.Timeout != 30*time.Minute || !Def.NeedsManifest {
		t.Errorf("%+v", Def)
	}
}

// The verdict is this atom's, and its caches are its own.
func TestTheCheckIsTypecheckUnderThisAtomsRow(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[project]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var env []string
	exec := func(_ context.Context, c atoms.Cmd) (string, int) {
		env = c.Env
		return "", 0
	}
	v := check(context.Background(), atomkit.In{Root: root, Exec: exec})
	if v.Atom != id || v.State != int(checks.StatePass) || !slices.Contains(env, "MYPY_CACHE_DIR=/tmp/dev-python-typecheck/mypy-cache") {
		t.Errorf("%+v env %v", v, env)
	}
}
