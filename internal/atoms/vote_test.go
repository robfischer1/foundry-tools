package atoms

import (
	"context"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// The runner gives an atom the longer of its own deadline and the run's, so a
// slow witness over a large change is not cut off at the per-atom minute, and an
// atom without one is cut off exactly as before.
func TestExecuteGivesAnAtomItsOwnLongerDeadline(t *testing.T) {
	slow := func(_ context.Context, a checks.AtomDef, _ Input) checks.Verdict {
		time.Sleep(150 * time.Millisecond)
		return checks.VerdictOf(a, 0, "answered late")
	}
	names := ids(3)
	reg := registry(t,
		Atom{ID: names[0], Run: slow},
		Atom{ID: names[1], Run: slow, Deadline: func(Input) time.Duration { return 5 * time.Second }},
		Atom{ID: names[2], Run: slow, Deadline: func(Input) time.Duration { return time.Millisecond }},
	)
	got := Execute(context.Background(), reg, Input{}, Options{Timeout: 50 * time.Millisecond})
	if got[0].State != 2 || !strings.Contains(got[0].Reason, "no answer") {
		t.Errorf("an atom with no deadline of its own outlived the run's: %+v", got[0])
	}
	if got[1].State != 0 {
		t.Errorf("the atom with the longer deadline was cut off: %+v", got[1])
	}
	if got[2].State != 2 {
		t.Errorf("a deadline SHORTER than the run's shortened the atom's: %+v", got[2])
	}
}
