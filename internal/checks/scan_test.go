package checks

import (
	"errors"
	"strings"
	"testing"
)

func TestTheScanVerdictsWordTheCouldNotRuns(t *testing.T) {
	a := AtomDef{ID: "fleet:x", Stage: StagePrecommit, Lane: LaneAny}
	if v := CannotEnumerate(a, errors.New("boom")); v.State != int(StateCannotRun) || !strings.Contains(v.Reason, "fleet:x: CANNOT RUN") || !strings.Contains(v.Reason, "the tree would not enumerate: boom") {
		t.Errorf("%+v", v)
	}
	if v := Interrupted(a, errors.New("deadline")); v.State != int(StateCannotRun) || !strings.Contains(v.Reason, "stopped before the scan finished: deadline") {
		t.Errorf("%+v", v)
	}
}

func TestUnmeasuredShowsTheFirstFiveAndCountsTheRest(t *testing.T) {
	if got := Unmeasured([]string{"a", "b"}); got != "a\nb" {
		t.Errorf("%q", got)
	}
	got := Unmeasured([]string{"1", "2", "3", "4", "5", "6", "7"})
	if got != "1\n2\n3\n4\n5\n... and 2 more" {
		t.Errorf("%q", got)
	}
}

func TestLaneDeclaredByFilesIsGoAndPythonOnly(t *testing.T) {
	for l, want := range map[Lane]bool{LaneGo: true, LanePython: true, LaneRust: false, LaneTS: false, LaneAny: false} {
		if LaneDeclaredByFiles(l) != want {
			t.Errorf("%q: want %v", l, want)
		}
	}
}
