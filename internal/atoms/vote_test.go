package atoms

import (
	"context"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// THE VOTER'S DEADLINE. A voting fleet:witness asks for real, so its honest worst
// case grows with the change set while every other atom's does not.
func TestWitnessDeadlineIsTheWorstCaseOfTheSourcesItWouldAsk(t *testing.T) {
	const one = 3 * (120*time.Second + 2*time.Second)
	for _, tc := range []struct {
		name string
		in   Input
		want time.Duration
	}{
		{"no change set", Input{}, 0},
		{"only files nobody asks about", Input{Changed: []string{"README.md", "a_test.go"}}, 0},
		{"one source", Input{Changed: []string{"a.go"}}, one},
		{"four sources are one round", Input{Changed: []string{"a.go", "b.go", "c.py", "d.py"}}, one},
		{"five sources are two rounds", Input{Changed: []string{"a.go", "b.go", "c.py", "d.py", "e.go"}}, 2 * one},
		{"a test file is not a source", Input{Changed: []string{"a.go", "a_test.go"}}, one},
		{"a dry run asks nothing", Input{WitnessDry: true, Changed: []string{"a.go"}}, 0},
		{"a change set that would not compute", Input{ChangedErr: context.Canceled, Changed: []string{"a.go"}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := witnessDeadline(tc.in); got != tc.want {
				t.Errorf("deadline %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOnlyFleetWitnessCarriesADeadline(t *testing.T) {
	for _, a := range Builtin() {
		if has := a.Deadline != nil; has != (a.ID == "fleet:witness") {
			t.Errorf("%s carries a deadline: %v", a.ID, has)
		}
	}
}

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

func TestRenderSaysWhichSideVoted(t *testing.T) {
	for _, tc := range []struct {
		voter, want string
	}{
		{"binary", "shadow atoms (binary voted): 0 compared"},
		{"chains", "shadow atoms (chains voted): 0 compared"},
		{"", "shadow atoms: 0 compared"},
	} {
		if got := (Report{Voter: tc.voter}).Render(); !strings.HasPrefix(got, tc.want) {
			t.Errorf("voter %q: report %q lacks %q", tc.voter, got, tc.want)
		}
	}
}
