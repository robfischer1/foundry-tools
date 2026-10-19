package atomkit

import (
	"context"
	"os"
	"time"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// probeBudget bounds the probe: an atom that does not stand down over an empty
// tree is not asked further.
var probeBudget = 10 * time.Second

// absentSentence is the sentence the index carries for an atom the planner
// stands down on its surface (lld.md FN-082).
//
// ONE SOURCE. An any-lane atom says why it stands down in its own Check (the
// Dagger gate prints that sentence), so the index asks the Check, over a tree
// holding nothing, rather than carrying a second copy of the words that drifts
// (DEF-B3-079: fleet:manifest-one-command carried the empty-lane text). An atom
// that does not stand down over nothing, a lane atom (the lane stands it down:
// AbsentVerdict) or a Check that panics or hangs gets the lane's sentence.
func absentSentence(d Def) string {
	generic := checks.AbsentReasonFor(d.ID, d.Lane, d.NeedsManifest)
	if d.Lane != checks.LaneAny || d.Check == nil {
		return generic
	}
	root, _ := os.MkdirTemp("", "describe-") // a root that could not be made is an empty one: the atoms read nothing there either
	defer func() { _ = os.RemoveAll(root) }()
	ctx, cancel := context.WithTimeout(context.Background(), probeBudget)
	defer cancel()
	in := In{
		Root: root, Files: []string{}, Committable: []string{}, Tracked: []string{}, Changed: []string{},
		Now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Exec: func(context.Context, atoms.Cmd) (string, int) {
			return "describe runs no program", 127
		},
	}
	answer := make(chan checks.Verdict, 1)
	go func() {
		defer func() {
			if recover() != nil { // a Check that panics over nothing has no sentence to give
				answer <- checks.Verdict{}
			}
		}()
		answer <- d.Check(ctx, in)
	}()
	select {
	case v := <-answer:
		if v.Result == "absent" && v.State == int(checks.StatePass) && v.Reason != "" {
			return v.Reason
		}
	case <-ctx.Done():
	}
	return generic
}
