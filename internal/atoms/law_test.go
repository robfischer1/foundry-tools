package atoms

import (
	"os"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/govlaw"
)

// THE LAW LANES ARE GRADED IN depth BY internal/govlaw and each atom's own tests;
// this holds what RunLaw adds: the tree is handed over read from the disk, the
// lane's state and report are the verdict, and a tree that will not walk is
// could-not-run and asks the lane nothing.
func TestRunLawHandsTheLaneTheTreeFromTheDisk(t *testing.T) {
	in := treeIn(t, map[string]string{"a.md": "alpha", "d/b.md": "beta"})
	a := checks.AtomDef{ID: "law:x", Stage: checks.StagePrecommit, Lane: checks.LaneAny}
	var seen []string
	v := RunLaw(a, in, func(tr govlaw.Tree) govlaw.Result {
		for _, p := range tr.Paths {
			body, err := tr.Read(p)
			if err != nil {
				t.Errorf("%s: %v", p, err)
			}
			seen = append(seen, p+"="+body)
		}
		return govlaw.Result{State: govlaw.StateFindings, Report: "law:x: FINDINGS - one"}
	})
	if strings.Join(seen, ",") != "a.md=alpha,d/b.md=beta" {
		t.Errorf("the lane saw %v", seen)
	}
	expect(t, v, stateOf(1), findings, "law:x: FINDINGS - one")
}

func TestRunLawOfATreeThatWillNotWalkIsCouldNotRun(t *testing.T) {
	a := checks.AtomDef{ID: "law:x", Stage: checks.StagePrecommit, Lane: checks.LaneAny}
	lane := func(govlaw.Tree) govlaw.Result {
		t.Error("the lane judged a tree nobody could list")
		return govlaw.Result{}
	}
	expect(t, RunLaw(a, missingRoot(t), lane), stateOf(2), cannot, "the tree would not enumerate")
	in := treeIn(t, map[string]string{"governance/a.md": "x"})
	in.FS = failingFS{FS: os.DirFS(in.Root), dir: "governance"}
	expect(t, RunLaw(a, in, lane), stateOf(2), cannot, "the tree would not enumerate: boom")
}
