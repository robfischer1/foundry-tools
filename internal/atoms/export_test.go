package atoms

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// The tree the atoms in their own packages are handed answers the three
// questions the chains asked of their Directory, and the whole walk.
func TestTheExportedTreeAnswersEntriesReadsGlobsAndTheWalk(t *testing.T) {
	in := treeIn(t, map[string]string{"a.txt": "A", "d/b.yml": "B", "d/c.yml": "C"})
	tr := in.Tree()
	if got, err := tr.Entries("."); err != nil || !slices.Equal(got, []string{"a.txt", "d/"}) {
		t.Errorf("entries %v, %v", got, err)
	}
	if got, err := tr.Read("d/b.yml"); err != nil || got != "B" {
		t.Errorf("read %q, %v", got, err)
	}
	if got := tr.Glob("d/*.yml"); !slices.Equal(got, []string{"d/b.yml", "d/c.yml"}) {
		t.Errorf("glob %v", got)
	}
	if got, err := tr.Files(); err != nil || !slices.Equal(got, []string{"a.txt", "d/b.yml", "d/c.yml"}) {
		t.Errorf("files %v, %v", got, err)
	}
}

// A tree with no rules/sast is absent unless a fleet template stamped it.
func TestTheExportedTreeSaysWhatAnAbsentRulesetMeans(t *testing.T) {
	a := checks.AtomDef{ID: "fleet:opengrep-sast", Stage: checks.StagePrecommit, Lane: checks.LaneAny}
	expect(t, treeIn(t, map[string]string{"main.go": "x"}).Tree().AbsentRuleset(a), checks.StatePass, absent, "no SAST-shipping fleet template stamped it")
	stamped := treeIn(t, map[string]string{".copier-answers.yml": "_src_path: https://git.notusmi.com/rob/go-repo-template.git\n"})
	expect(t, stamped.Tree().AbsentRuleset(a), checks.StateCannotRun, cannot, "go-repo-template stamped it")
}

// The program seam is the input's Exec, or the real thing.
func TestTheExportedRunIsTheInputsExecOrTheRealProgram(t *testing.T) {
	var ran []string
	in := Input{Exec: func(_ context.Context, c Cmd) (string, int) {
		ran = append(ran, c.Name)
		return "answered", 3
	}}
	if out, code := in.Run(t.Context(), Cmd{Name: "tool"}); out != "answered" || code != 3 || !slices.Equal(ran, []string{"tool"}) {
		t.Errorf("%q %d %v", out, code, ran)
	}
	if out, code := (Input{}).Run(t.Context(), Cmd{Name: "/no/such/program"}); code != -1 || !strings.Contains(out, "/no/such/program") {
		t.Errorf("a program that would not start: %q %d", out, code)
	}
}

func TestTheExportedDoorIsTheInputsOrTheProductions(t *testing.T) {
	d := checks.Door{Base: "http://door.invalid"}
	if got := (Input{Door: d}).GitDoor(); got.Base != d.Base {
		t.Errorf("%+v", got)
	}
	if got := (Input{}).GitDoor(); got.Base != checks.NewDoor().Base {
		t.Errorf("%+v", got)
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

func TestTheExportedNeverRanIsTheCouldNotRunOfAProgramThatWouldNotStart(t *testing.T) {
	v := NeverRan(checks.AtomDef{ID: "fleet:x", Stage: checks.StagePrecommit, Lane: checks.LaneAny}, "opengrep: gone")
	if v.State != int(checks.StateCannotRun) || !strings.Contains(v.Reason, "the atom never ran: opengrep: gone") {
		t.Errorf("%+v", v)
	}
}
