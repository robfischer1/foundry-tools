package atoms

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// allocEnv makes the test binary, re-executed, a child that holds allocMiB of
// touched memory and exits: a program whose peak RSS is known to be at least
// that.
const (
	allocEnv = "ATOMS_MEM_TEST_ALLOC"
	allocMiB = 64
)

func TestMain(m *testing.M) {
	if os.Getenv(allocEnv) == "1" {
		b := make([]byte, allocMiB<<20)
		for i := range b {
			b[i] = 1
		}
		if b[len(b)-1] != 1 {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// allocChild runs the allocating child and answers its reaped state.
func allocChild(t *testing.T) *os.ProcessState {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), allocEnv+"=1")
	if err := cmd.Run(); err != nil {
		t.Fatalf("the allocating child: %v", err)
	}
	return cmd.ProcessState
}

// A PROGRAM'S PEAK IS ITS OWN wait4 ru_maxrss, in bytes: the allocating child
// peaks above what it held, a shell run after it does not lower the atom's
// peak, and the peak is the child's, not the process-wide high-water mark.
func TestAnAtomsPeakIsTheLargestOfItsPrograms(t *testing.T) {
	m := NewMeter()
	ctx := withMeter(context.Background(), m, "a")
	big := allocChild(t)
	chargeChild(ctx, big)
	want, ok := peakRSS(big)
	if !ok || want < allocMiB<<20 {
		t.Fatalf("the child's peak %d (%v), want at least %d", want, ok, allocMiB<<20)
	}
	if got, ok := m.peakOf("a"); !ok || got != want {
		t.Fatalf("a's peak %d (%v), want the child's %d", got, ok, want)
	}
	// A smaller program after it leaves the peak where it was, and is
	// charged on its own atom as its own (smaller) peak.
	if _, code := RunProgram(ctx, Cmd{Name: "true"}); code != 0 {
		t.Fatalf("true exited %d", code)
	}
	if got, _ := m.peakOf("a"); got != want {
		t.Errorf("a smaller program moved a's peak to %d, want %d", got, want)
	}
	RunProgram(withMeter(context.Background(), m, "small"), Cmd{Name: "true"})
	small, ok := m.peakOf("small")
	if !ok || small <= 0 || small >= want {
		t.Errorf("small's peak %d (%v), want its own, above 0 and below %d", small, ok, want)
	}
	// No state, no rusage, no atom: nothing is charged.
	chargeChild(ctx, nil)
	if _, ok := peakRSS(&os.ProcessState{}); ok {
		t.Error("a state with no rusage said a peak")
	}
	RunProgram(withMeter(context.Background(), nil, "b"), Cmd{Name: "true"})
	if _, ok := m.peakOf("b"); ok {
		t.Error("b was charged on a meter it was not given")
	}
	var none *Meter
	none.chargePeak("x", 1)
}

// chargePeak keeps the larger, never the sum or the last.
func TestChargePeakKeepsTheLarger(t *testing.T) {
	m := NewMeter()
	for _, b := range []int64{5, 9, 7} {
		m.chargePeak("a", b)
	}
	if got, _ := m.peakOf("a"); got != 9 {
		t.Errorf("peak %d, want 9", got)
	}
	m.chargePeak("z", 0)
	if got, ok := m.peakOf("z"); ok {
		t.Errorf("a zero peak was kept as %d", got)
	}
}

// Stamp writes the peak an atom's programs said, and nothing for an atom
// that ran none or never started — with or without a profile.
func TestStampWritesThePeakThatIsKnown(t *testing.T) {
	m := NewMeter()
	m.chargePeak("ran-exec", 4096)
	m.chargePeak("absent-charged", 1<<20)
	for _, profiled := range []bool{true, false} {
		v := []checks.Verdict{{Atom: "ran-go", StartedAt: "t"}, {Atom: "ran-exec", StartedAt: "t"}, {Atom: "absent-charged"}}
		m.Stamp(v, nil, profiled)
		if v[0].MemPeakBytes != nil || v[2].MemPeakBytes != nil {
			t.Errorf("profiled %v: a peak not known was stamped: %v %v", profiled, deref(v[0].MemPeakBytes), deref(v[2].MemPeakBytes))
		}
		if got := v[1].MemPeakBytes; got == nil || *got != 4096 {
			t.Errorf("profiled %v: ran-exec %v, want 4096", profiled, deref(got))
		}
	}
}
