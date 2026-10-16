package atoms

import (
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/govlaw"
)

// THE LAW LANES (Nomos F5b). Each atom (atoms/law/<name>) hands the tree to
// internal/govlaw, which owns the judgement and is ABSENT on a tree that is not
// foundry-stocks. The tree is walked from the disk the way fleet:retired-verbs
// walks it, so a checkout git would not read is graded all the same.

// RunLaw is a law lane's verdict over the input's tree. An unwalkable tree is
// CANNOT RUN, never an absence: a lane that could not list the tree has not
// found it empty.
func RunLaw(a checks.AtomDef, in Input, lane func(govlaw.Tree) govlaw.Result) checks.Verdict {
	t := in.tree()
	paths, err := t.files()
	if err != nil {
		return cannotEnumerate(a, err)
	}
	r := lane(govlaw.Tree{Paths: paths, Read: t.read})
	return checks.VerdictOf(a, r.State, r.Report)
}
