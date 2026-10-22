package devgosast

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

func TestTheDeclarationIsTheDevVerbOfTheSastScan(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != "dev:go-sast" || Def.Stage != "dev" || Def.Lane != checks.LaneGo || Def.Group != checks.GroupLanguage ||
		Def.Toolchain != "go" || Def.Surface != "root:go.mod" || strings.Join(Def.Needs, ",") != "tool:opengrep" || Def.WritesTree {
		t.Errorf("%+v", Def)
	}
}

// ruled is a tree with a ruleset: the scan runs.
func ruled(t *testing.T, answer string, code int) (atomkit.In, *[]string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "rules", "sast"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rules", "sast", "a.yml"), []byte("rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var runs []string
	exec := func(_ context.Context, c atoms.Cmd) (string, int) {
		runs = append(runs, c.Name+" "+strings.Join(c.Args, " "))
		return answer, code
	}
	return atomkit.In{Root: dir, Files: []string{"go.mod", "rules/sast/a.yml"}, Exec: exec}, &runs
}

// It runs the gate's scan: a finding fails (opengrep's exit 1 under --error), a
// clean scan passes, a zero-file scan is refused, and each says its own id.
func TestAFindingFailsACleanScanPassesAZeroFileScanIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name, out string
		code      int
		state     checks.State
		needle    string
	}{
		{"clean", "Ran 4 rules on 12 files: 0 findings.", 0, checks.StatePass, ""},
		{"finding", "main.go: injection", 1, checks.StateFindings, "main.go: injection"},
		{"zero files", "Ran 4 rules on 0 files: 0 findings.", 0, checks.StateCannotRun, "REFUSING a zero-file scan"},
	} {
		in, runs := ruled(t, tc.out, tc.code)
		v := check(context.Background(), in)
		said := v.Reason + strings.Join(v.Logs, "\n")
		if v.Atom != "dev:go-sast" || v.Stage != "dev" || v.State != int(tc.state) || !strings.Contains(said, tc.needle) {
			t.Errorf("%s: %+v", tc.name, v)
		}
		if len(*runs) != 1 || (*runs)[0] != "opengrep scan --config rules/sast --error ." {
			t.Errorf("%s: runs %v", tc.name, *runs)
		}
	}
}
