package checks

import (
	"strings"
	"testing"
)

// The whole point of the type, asserted directly: NOTHING BUT A ZERO IS A PASS.
// A 127 is a missing binary, a 137 is an OOM kill, a 143 is a cancellation, and
// each of those has at some point been rendered as a green check somewhere in
// this fleet.
func TestOnlyZeroPasses(t *testing.T) {
	for code := -8; code <= 255; code++ {
		got := StateFor(code)
		if code == 0 && got != StatePass {
			t.Fatalf("exit 0 must be a pass, got %v", got)
		}
		if code != 0 && got == StatePass {
			t.Fatalf("exit %d read as a PASS — a non-zero exit is never clean", code)
		}
	}
}

func TestStateForNamesTheThreeStates(t *testing.T) {
	for _, tc := range []struct {
		exit int
		want State
	}{
		{0, StatePass},
		{1, StateFindings},
		{2, StateCannotRun},
		{3, StateCannotRun},
		{127, StateCannotRun},
		{137, StateCannotRun},
		{-1, StateCannotRun},
	} {
		if got := StateFor(tc.exit); got != tc.want {
			t.Errorf("StateFor(%d) = %v, want %v", tc.exit, got, tc.want)
		}
	}
}

// Anything that is not 0 or 1 is CANNOT RUN rather than findings. Reading a
// missing binary as "the tool ran and disagrees with you" is a different lie
// from the one this module deletes, but it is still a lie.
func TestUnexpectedExitIsCannotRunNotFindings(t *testing.T) {
	for _, code := range []int{2, 3, 42, 126, 127, 137, 143, 255} {
		if got := StateFor(code); got != StateCannotRun {
			t.Errorf("StateFor(%d) = %v, want StateCannotRun", code, got)
		}
	}
}

func TestStateStringsAreStable(t *testing.T) {
	want := map[State]string{
		StatePass:      "pass",
		StateFindings:  "findings",
		StateCannotRun: "cannot-run",
	}
	for s, w := range want {
		if got := s.String(); got != w {
			t.Errorf("State(%d).String() = %q, want %q", int(s), got, w)
		}
	}
	if got := State(9).String(); got != "cannot-run" {
		t.Errorf("an unknown state must degrade to cannot-run, got %q", got)
	}
}

// A verdict answers `dagger check` with nil ONLY on a pass. Everything else
// carries an error whose text names the state, so a red check says which of the
// two reds it is.
func TestAnswerIsNilOnlyForAPass(t *testing.T) {
	a := AtomByID("go:staticcheck")

	if _, err := VerdictOf(a, 0, "").Answer(); err != nil {
		t.Fatalf("exit 0 must answer nil, got %v", err)
	}

	_, err := VerdictOf(a, 1, "boom").Answer()
	if err == nil || !strings.Contains(err.Error(), "FINDINGS") {
		t.Fatalf("exit 1 must answer a FINDINGS error, got %v", err)
	}

	_, err = VerdictOf(a, 2, "not installed").Answer()
	if err == nil || !strings.Contains(err.Error(), "CANNOT RUN") {
		t.Fatalf("exit 2 must answer a CANNOT RUN error, got %v", err)
	}

	_, err = VerdictOf(a, 127, "no such file").Answer()
	if err == nil || !strings.Contains(err.Error(), "CANNOT RUN (exit 127)") {
		t.Fatalf("exit 127 must answer CANNOT RUN naming the real code, got %v", err)
	}
}

// ABSENT is a pass that SAYS SO. The ops lane's rule — "a facet with no surface
// in the tree reports absent, exit 0, and says so" — is the whole reason this
// is not just a silent skip.
func TestAbsentPassesAndSaysSo(t *testing.T) {
	v := AbsentVerdict(AtomByID("rust:cargo-test"))
	if v.State != int(StatePass) {
		t.Fatalf("absent must be state 0, got %d", v.State)
	}
	if v.Result != "absent" {
		t.Fatalf("absent must be distinguishable from a clean scan, got result %q", v.Result)
	}
	if !strings.Contains(v.Reason, "Cargo.toml") {
		t.Fatalf("absent must name the manifest it looked for, got %q", v.Reason)
	}
	msg, err := v.Answer()
	if err != nil {
		t.Fatalf("absent must answer nil, got %v", err)
	}
	if !strings.Contains(msg, "ABSENT") {
		t.Fatalf("absent must say so in its message, got %q", msg)
	}
}
