package atomlib

import (
	"context"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
)

// These swap the package's sleep and jitter, so none of them runs in parallel.

func seamNet(t *testing.T, rnd float64) *[]time.Duration {
	t.Helper()
	var slept []time.Duration
	oldSleep, oldRand := netSleep, netRand
	netSleep, netRand = func(d time.Duration) { slept = append(slept, d) }, func() float64 { return rnd }
	t.Cleanup(func() { netSleep, netRand = oldSleep, oldRand })
	return &slept
}

func TestNetRetry_ADoorThatComesBackIsAPass(t *testing.T) {
	slept := seamNet(t, 0.5)
	calls := 0
	in, _ := atomtest.CheckerTree(t, "chk", 0o755, func(atoms.Cmd) (string, int) {
		calls++
		if calls <= 2 {
			return "fatal: unable to access 'http://ourea:8215/nas01-stacks.git/': Could not resolve host: ourea", 2
		}
		return "ok", 0
	})
	v := RunChecker(context.Background(), checkerRow, in, Checker{Phase: "x", Tool: "chk", Python: "python3", Two: "\nx: did not look"})
	atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass, "ok")
	if calls != 3 || len(*slept) != 2 || (*slept)[0] != time.Second || (*slept)[1] != 2*time.Second {
		t.Errorf("calls %d, waits %v, want 3 and 1 s, 2 s", calls, *slept)
	}
}

func TestNetRetry_AnUnreachableDoorIsCannotRunAfterEveryWait(t *testing.T) {
	slept := seamNet(t, 0.5)
	calls := 0
	in, _ := atomtest.CheckerTree(t, "chk", 0o755, func(atoms.Cmd) (string, int) {
		calls++
		return "fatal: Failed to connect to ourea port 8215: Connection refused", 2
	})
	v := RunChecker(context.Background(), checkerRow, in, Checker{Phase: "x", Tool: "chk", Python: "python3", Two: "\nx: did not look"})
	atomtest.Expect(t, v, atomtest.StateOf(2), atomtest.Cannot, "Connection refused", "x: did not look")
	if calls != len(netWaits)+1 || len(*slept) != len(netWaits) {
		t.Errorf("calls %d, waits %v", calls, *slept)
	}
}

func TestNetRetry_ASourceThatIsWrongIsNotRetried(t *testing.T) {
	slept := seamNet(t, 0.5)
	calls := 0
	in, _ := atomtest.CheckerTree(t, "chk", 0o755, func(atoms.Cmd) (string, int) {
		calls++
		return "fatal: unable to access: The requested URL returned error: 404", 2
	})
	RunChecker(context.Background(), checkerRow, in, Checker{Phase: "x", Tool: "chk", Python: "python3", Two: "\nx: did not look"})
	if calls != 1 || len(*slept) != 0 {
		t.Errorf("a 404 is final: calls %d, waits %v", calls, *slept)
	}
}

func TestNetRetry_ACheckerThatReadsNoNetworkIsRunOnce(t *testing.T) {
	slept := seamNet(t, 0.5)
	calls := 0
	in, _ := atomtest.CheckerTree(t, "chk", 0o755, func(atoms.Cmd) (string, int) {
		calls++
		return "Could not resolve host", 2
	})
	RunChecker(context.Background(), checkerRow, in, Checker{Phase: "x", Tool: "chk", Python: "python3"})
	if calls != 1 || len(*slept) != 0 {
		t.Errorf("no Two, no retry: calls %d, waits %v", calls, *slept)
	}
}

func TestNetRetry_WaitsAreOneTwoFourEightWithinThirtyPercent(t *testing.T) {
	var base time.Duration
	for i, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second} {
		if netWaits[i] != want {
			t.Errorf("wait %d = %v, want %v", i, netWaits[i], want)
		}
		base += netWaits[i]
	}
	for _, c := range []struct{ r, frac float64 }{{0, 0.7}, {0.5, 1}, {0.999999, 1.3}} {
		seamNet(t, c.r)
		got, want := netWait(time.Second), time.Duration(c.frac*float64(time.Second))
		if d := got - want; d < -time.Millisecond || d > time.Millisecond {
			t.Errorf("rand %v: %v, want %v", c.r, got, want)
		}
	}
	if worst := time.Duration(1.3 * float64(base)); len(netWaits) != 4 || worst > 30*time.Second {
		t.Errorf("worst total %v over 30 s", worst)
	}
}

func TestNetRetry_Transient(t *testing.T) {
	for msg, want := range map[string]bool{
		"Could not resolve host: ourea":                    true,
		"Connection refused":                               true,
		"Connection reset by peer":                         true,
		"Operation timed out":                              true,
		"The requested URL returned error: 502":            true,
		"the remote end hung up unexpectedly":              true,
		"The requested URL returned error: 404":            false,
		"fatal: repository 'http://ourea/x.git' not found": false,
		"specs: drifted":                                   false,
	} {
		if got := transientNet(strings.ToLower(msg)); got != want {
			t.Errorf("transientNet(%q) = %v, want %v", msg, got, want)
		}
	}
}
