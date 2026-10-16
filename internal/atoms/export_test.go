package atoms

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// A tree with no rules/sast is absent unless a fleet template stamped it.
func TestTheTreeSaysWhatAnAbsentRulesetMeans(t *testing.T) {
	a := checks.AtomDef{ID: "fleet:opengrep-sast", Stage: checks.StagePrecommit, Lane: checks.LaneAny}
	unstamped := treeIn(t, map[string]string{"main.go": "x"}).Tree().AbsentRuleset(a)
	if unstamped.State != int(checks.StatePass) || unstamped.Result != "absent" {
		t.Errorf("%+v", unstamped)
	}
	stamped := treeIn(t, map[string]string{".copier-answers.yml": "_src_path: https://git.notusmi.com/rob/go-repo-template.git\n"}).Tree().AbsentRuleset(a)
	if stamped.State != int(checks.StateCannotRun) || !strings.Contains(stamped.Reason, "go-repo-template stamped it") {
		t.Errorf("%+v", stamped)
	}
}

// Scratch paths live under the atom's work directory: ATOM_WORK when the executor
// names one, the system's temporary directory otherwise, and never twice the same.
func TestScratchPathsLiveUnderTheAtomsWorkDirectory(t *testing.T) {
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	a, b := TempPath("hadolint", ".yaml"), TempPath("hadolint", ".yaml")
	if a == b || filepath.Dir(a) != work || !strings.HasSuffix(a, ".yaml") || !strings.Contains(a, "hadolint") {
		t.Errorf("%s %s", a, b)
	}
	if WorkDir() != work {
		t.Errorf("work dir %s", WorkDir())
	}
	t.Setenv("ATOM_WORK", "")
	if got := WorkDir(); got == "" || got == work {
		t.Errorf("without ATOM_WORK the work dir is the system's: %q", got)
	}
}

// withOneAtom joins one atom of the pull path to the built-ins for the test: every
// atom lives in its own file now (linked in by internal/catalogue), so a test of
// the runner, the CLI or a stage asks for the one it needs. It passes.
func withOneAtom(t *testing.T) {
	t.Helper()
	old := ported
	t.Cleanup(func() { ported = old })
	UseExtra(func() []Atom {
		return []Atom{{ID: "ts:regen", Scope: ScopeTree, Run: func(_ context.Context, a checks.AtomDef, _ Input) checks.Verdict {
			return checks.VerdictOf(a, int(checks.StatePass), "ts:regen: passes")
		}}}
	})
}
