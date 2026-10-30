package devpython

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
	"dagger/foundry-tools/internal/checks/rulesets"
)

type ruffRun struct {
	calls  []atoms.Cmd
	config string // the file --config named, read when ruff was run
	exit   int
	probe  int
}

func (r *ruffRun) exec(_ context.Context, c atoms.Cmd) (string, int) {
	r.calls = append(r.calls, c)
	if slices.Contains(c.Args, "--version") {
		return "ruff 0.0", r.probe
	}
	if i := slices.Index(c.Args, "--config"); i >= 0 {
		b, _ := os.ReadFile(c.Args[i+1])
		r.config = string(b)
	}
	return "x.py:1:1: E1 a finding", r.exit
}

var ruffRow = checks.AtomDef{ID: "dev:python-lint", Stage: "dev", Lane: checks.LanePython}

// The dev verb lays the fleet's ruleset like python:ruff-check does, so it is
// not the tree's own pyproject.toml that decides: the file ruff is handed is the
// embedded one, and a pyproject.toml in the tree is never named.
func TestRuffRunsUnderTheFleetsRulesetNotTheTrees(t *testing.T) {
	t.Setenv("ATOM_WORK", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[tool.ruff]\nline-length = 60\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &ruffRun{exit: 1}
	v := Ruff(t.Context(), atomkit.In{Root: root, Args: []string{"src/x.py"}, Exec: r.exec}, ruffRow, "check", "/tmp/c")
	if v.State != int(checks.StateFindings) {
		t.Errorf("an exit of 1 is the finding: %+v", v)
	}
	if r.config != rulesets.Ruff {
		t.Errorf("ruff was handed %q, not the fleet's ruleset", r.config)
	}
	run := r.calls[len(r.calls)-1]
	want := []string{"check", "--config", filepath.Join(os.Getenv("ATOM_WORK"), "ruff.toml"), "--line-length", checks.RuffLineLength, "src/x.py"}
	if run.Name != "ruff" || !slices.Equal(run.Args, want) || run.Dir != root || !slices.Equal(run.Env, []string{"RUFF_CACHE_DIR=/tmp/c"}) {
		t.Errorf("ran %+v, want ruff %v", run, want)
	}
}

// The neighbouring cases: a clean run passes, format spells its own verb and no
// cache, and a ruff that is not there is could-not-run, never a pass.
func TestRuffSettlesCleanFormatAndMissingTool(t *testing.T) {
	t.Setenv("ATOM_WORK", t.TempDir())
	r := &ruffRun{}
	if v := Ruff(t.Context(), atomkit.In{Root: t.TempDir(), Exec: r.exec}, ruffRow, "format", ""); v.State != int(checks.StatePass) {
		t.Errorf("an exit of 0 passes: %+v", v)
	}
	run := r.calls[len(r.calls)-1]
	if run.Args[0] != "format" || run.Env != nil || strings.Contains(strings.Join(run.Args, " "), "--check") {
		t.Errorf("format ran %+v", run)
	}
	r = &ruffRun{probe: 127}
	if v := Ruff(t.Context(), atomkit.In{Root: t.TempDir(), Exec: r.exec}, ruffRow, "check", ""); v.State != int(checks.StateCannotRun) || len(r.calls) != 1 {
		t.Errorf("no ruff on PATH is could-not-run and runs nothing else: %+v %d", v, len(r.calls))
	}
}

// A ruleset that cannot be written is could-not-run and ruff never starts.
func TestRuffCannotRunWhenTheRulesetCannotBeWritten(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATOM_WORK", filepath.Join(blocker, "under"))
	r := &ruffRun{}
	if v := Ruff(t.Context(), atomkit.In{Root: t.TempDir(), Exec: r.exec}, ruffRow, "check", ""); v.State != int(checks.StateCannotRun) || len(r.calls) != 0 {
		t.Errorf("%+v %d", v, len(r.calls))
	}
}
