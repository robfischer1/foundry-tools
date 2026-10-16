package atomlib

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

func TestOpsSurfaceStandsDownOffTheOpsShape(t *testing.T) {
	a := checks.AtomDef{ID: "ops:x", Stage: checks.StagePrecommit, Lane: checks.LaneAny}
	if v := OpsSurface(a, atoms.Input{Files: []string{"flux/x.yaml"}}); v != nil {
		t.Errorf("an ops tree was stood down: %+v", v)
	}
	v := OpsSurface(a, atoms.Input{Files: []string{"main.go"}})
	if v == nil {
		t.Fatal("a star is not an ops tree")
	}
	atomtest.Expect(t, *v, atomtest.StateOf(0), atomtest.Absent, "has no ops shape")
	v = OpsSurface(a, atoms.Input{FilesErr: atomtest.ErrBoom})
	if v == nil {
		t.Fatal("a scan that failed is not an ops tree")
	}
	atomtest.Expect(t, *v, atomtest.StateOf(2), atomtest.Cannot, "the tree would not enumerate")
}

func TestProbeToolAnswersAVerdictOnlyWhenTheProbeFails(t *testing.T) {
	a := checks.AtomDef{ID: "ops:x", Stage: checks.StagePrecommit, Lane: checks.LaneAny}
	ok := atoms.Input{Exec: func(context.Context, atoms.Cmd) (string, int) { return "v1", 0 }}
	if v := ProbeTool(context.Background(), a, ok, "tool", "--version"); v != nil {
		t.Errorf("a probe that passed: %+v", v)
	}
	bad := atoms.Input{Exec: func(context.Context, atoms.Cmd) (string, int) { return " gone ", 127 }}
	v := ProbeTool(context.Background(), a, bad, "tool", "--version")
	if v == nil {
		t.Fatal("a probe that failed answered nothing")
	}
	atomtest.Expect(t, *v, atomtest.StateOf(2), atomtest.Cannot, "ops:x: CANNOT RUN - the phase's tool could not be provisioned: tool --version exited 127: gone")
}

func TestPhaseAbsentAndNeverRanWordThemselvesAsTheChainsDid(t *testing.T) {
	a := checks.AtomDef{ID: "ops:x", Stage: checks.StagePrecommit, Lane: checks.LaneAny}
	atomtest.Expect(t, PhaseAbsent(a, "no tools/x in this tree"), atomtest.StateOf(0), atomtest.Absent, "ops:x: ABSENT - no tools/x in this tree")
	atomtest.Expect(t, NeverRan(a, "boom"), atomtest.StateOf(2), atomtest.Cannot, "the atom never ran: boom")
}

func TestOpsSettledAppendsTheLineTheRulesAdd(t *testing.T) {
	state, out := OpsSettled("yaml", 1, "x")
	if state != 1 || !strings.Contains(out, "yaml failed (rc=1) — findings") {
		t.Errorf("%d %q", state, out)
	}
	state, out = OpsSettled("yaml", 0, "x")
	if state != 0 || out != "x" {
		t.Errorf("%d %q", state, out)
	}
	state, _ = OpsSettledPython("yaml", 1, "Traceback (most recent call last):\nModuleNotFoundError: x\n")
	if state != 2 {
		t.Errorf("a crashed python is a 2, got %d", state)
	}
}

func TestComposeSurfaceStandsDownWithoutASpecAndRefusesABrokenScan(t *testing.T) {
	a := checks.AtomDef{ID: "compose:x", Stage: checks.StagePrecommit, Lane: checks.LaneAny}
	_, stop := ComposeSurface(a, atoms.Input{Committable: []string{"a.yml"}})
	if stop == nil {
		t.Fatal("no spec, no surface")
	}
	atomtest.Expect(t, *stop, atomtest.StateOf(0), atomtest.Absent, "tracks no compose.yaml/compose.yml")
	_, stop = ComposeSurface(a, atoms.Input{FilesErr: atomtest.ErrBoom})
	if stop == nil {
		t.Fatal("a broken scan is not an absence")
	}
	atomtest.Expect(t, *stop, atomtest.StateOf(2), atomtest.Cannot, "the compose-surface scan itself failed")
	files, stop := ComposeSurface(a, atoms.Input{Committable: []string{"compose.yaml", "x"}})
	if stop != nil || len(files) != 2 {
		t.Errorf("%v %v", files, stop)
	}
}
