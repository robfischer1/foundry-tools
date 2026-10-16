package atoms

import (
	"context"
	"fmt"

	"dagger/foundry-tools/internal/checks"
)

// opsRecordLanes: flux's two record-lane lists are one set and hold every lane
// the executor runs (checks.RecordLanes is the judgement; this reads the tree).
// A tree with neither file is not the flux tree (ABSENT); a tree with one of
// them is NOT absence - the other moved, and "agrees" would be unlooked-at.
func opsRecordLanes(_ context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if in.FilesErr != nil {
		return cannotEnumerate(a, in.FilesErr)
	}
	t := in.tree()
	files := map[string]string{}
	for _, p := range in.Files {
		if !checks.IsRecordLanesFile(p) {
			continue
		}
		body, err := t.read(p)
		if err != nil {
			return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf(
				"%s: CANNOT RUN - %s would not read: %v", a.ID, p, err))
		}
		files[p] = body
	}
	if len(files) == 0 {
		return opsPhaseAbsent(a, "this tree carries neither "+checks.RecordLanesJobsFile+" nor "+checks.RecordLanesOureaFile+", so it is not the fleet's flux tree")
	}
	state, report := checks.RecordLanes(files)
	return checks.VerdictOf(a, state, report)
}
