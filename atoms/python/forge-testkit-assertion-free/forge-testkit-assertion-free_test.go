package pythonforgetestkitassertionfree

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

type script struct{ calls []atoms.Cmd }

func (s *script) exec(_ context.Context, c atoms.Cmd) (string, int) {
	s.calls = append(s.calls, c)
	return "", 0
}

func TestTheDeclarationValidatesAndKeepsTheRowItReplaces(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LanePython || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "python" || Def.NotOnForks != checks.ForkFleetStyle || Def.NeedsManifest || !Def.WritesTree ||
		Def.Desc != "No assertion-free test bodies (forge-testkit)." || !slices.Equal(Def.Needs, []string{"tool:uv"}) {
		t.Errorf("%+v", Def)
	}
}

// The mode names the linter's subcommand and the pattern the files it reads.
func TestItLintsItsOwnPopulationInItsOwnMode(t *testing.T) {
	dir := t.TempDir()
	project := "[project]\nname = \"x\"\ndependencies = [\n    \"forge-testkit>=1\",\n]\n"
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &script{}
	in := atomkit.In{Root: dir, Files: []string{"tests/test_x.py", "src/x.py"}, Exec: s.exec}
	if v := check(context.Background(), in); v.State != 0 || v.Reason != id+": PASS" {
		t.Fatalf("%+v", v)
	}
	if len(s.calls) != 2 {
		t.Fatalf("%v", s.calls)
	}
	want := "run --extra dev forge-testkit-lint assertion-free tests/test_x.py"
	if got := strings.Join(s.calls[1].Args, " "); s.calls[1].Name != "uv" || got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
