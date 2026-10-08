package atoms

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// ids are real catalogue rows: a registration names one, so the fakes borrow
// the names of the first rows and bring their own functions.
func ids(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = checks.Atoms[i].ID
	}
	return out
}

// passes is a RunFunc that answers a pass naming its atom.
func passes(_ context.Context, a checks.AtomDef, _ Input) checks.Verdict {
	return checks.VerdictOf(a, 0, "ran "+a.ID)
}

func registry(t *testing.T, atoms ...Atom) *Registry {
	t.Helper()
	reg, err := NewRegistry(atoms...)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestRegistryRefusesWhatItCannotRun(t *testing.T) {
	known := ids(1)[0]
	for _, tc := range []struct {
		name    string
		atoms   []Atom
		wantErr string
	}{
		{"an id outside the catalogue", []Atom{{ID: "nope:nothing", Run: passes}}, "not in the catalogue"},
		{"the same id twice", []Atom{{ID: known, Run: passes}, {ID: known, Run: passes}}, "registered twice"},
		{"no function", []Atom{{ID: known}}, "no run function"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg, err := NewRegistry(tc.atoms...)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %v, want one containing %q", err, tc.wantErr)
			}
			if reg != nil {
				t.Errorf("a refused registry must not be usable")
			}
		})
	}
}

func TestBuiltinIsTheFourPortedAtomsInOrder(t *testing.T) {
	reg, err := NewRegistry(Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"fleet:check-yaml", "fleet:check-added-large-files", "fleet:check-merge-conflict", "fleet:stop-justifications"}
	if got := reg.IDs(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("ids %q, want %q", got, want)
	}
	for _, a := range Builtin() {
		if a.Scope != ScopeTree {
			t.Errorf("%s is whole-tree scope", a.ID)
		}
	}
}

func TestExecuteReturnsRegistryOrderWhateverOrderTheyFinish(t *testing.T) {
	names := ids(4)
	// Atom 0 cannot finish until atom 3 has: finish order is the reverse of
	// registry order, and the pool is wide enough that both are in flight.
	last := make(chan struct{})
	var atoms []Atom
	for i, id := range names {
		atoms = append(atoms, Atom{ID: id, Run: func(_ context.Context, a checks.AtomDef, _ Input) checks.Verdict {
			switch i {
			case 0:
				<-last
			case 3:
				close(last)
			}
			return checks.VerdictOf(a, 0, fmt.Sprintf("atom %d", i))
		}})
	}
	got := Execute(context.Background(), registry(t, atoms...), Input{}, Options{Workers: 4, Timeout: 2 * time.Second})
	if len(got) != 4 {
		t.Fatalf("%d verdicts, want 4", len(got))
	}
	for i, v := range got {
		if v.Atom != names[i] || v.State != 0 || !strings.Contains(strings.Join(v.Logs, "\n"), fmt.Sprintf("atom %d", i)) {
			t.Errorf("slot %d holds %s state %d logs %q", i, v.Atom, v.State, v.Logs)
		}
	}
}

func TestExecuteRunsAtomsConcurrently(t *testing.T) {
	for _, tc := range []struct {
		name    string
		workers int
		n       int
	}{
		{"an explicit pool", 4, 4},
		{"the default pool is GOMAXPROCS", 0, min(runtime.GOMAXPROCS(0), len(checks.Atoms))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Every atom waits for every other to be running. A serial runner
			// never gets past the first, and its atoms time out as 2.
			var arrived sync.WaitGroup
			arrived.Add(tc.n)
			var atoms []Atom
			for _, id := range ids(tc.n) {
				atoms = append(atoms, Atom{ID: id, Run: func(_ context.Context, a checks.AtomDef, _ Input) checks.Verdict {
					arrived.Done()
					arrived.Wait()
					return checks.VerdictOf(a, 0, "together")
				}})
			}
			for _, v := range Execute(context.Background(), registry(t, atoms...), Input{}, Options{Workers: tc.workers, Timeout: 2 * time.Second}) {
				if v.State != 0 {
					t.Errorf("%s did not run alongside the rest: state %d: %s", v.Atom, v.State, v.Reason)
				}
			}
		})
	}
}

func TestExecuteBoundsThePool(t *testing.T) {
	// The first two atoms wait for each other, so two ARE in flight at once
	// (the floor is certain); the other four and the 20ms hold give a pool
	// that is too wide every chance to show a third.
	var inFlight, peak atomic.Int32
	var pair sync.WaitGroup
	pair.Add(2)
	var atoms []Atom
	for i, id := range ids(6) {
		atoms = append(atoms, Atom{ID: id, Run: func(_ context.Context, a checks.AtomDef, _ Input) checks.Verdict {
			n := inFlight.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			if i < 2 {
				pair.Done()
				pair.Wait()
			}
			time.Sleep(20 * time.Millisecond)
			inFlight.Add(-1)
			return checks.VerdictOf(a, 0, "x")
		}})
	}
	for _, v := range Execute(context.Background(), registry(t, atoms...), Input{}, Options{Workers: 2, Timeout: 2 * time.Second}) {
		if v.State != 0 {
			t.Fatalf("%s settled %d: %s", v.Atom, v.State, v.Reason)
		}
	}
	if p := peak.Load(); p != 2 {
		t.Errorf("peak concurrency %d, want exactly the pool of 2", p)
	}
}

func TestExecuteSettlesAPanicAsThatAtomsCannotRun(t *testing.T) {
	names := ids(3)
	reg := registry(t,
		Atom{ID: names[0], Run: passes},
		Atom{ID: names[1], Run: func(context.Context, checks.AtomDef, Input) checks.Verdict { panic("boom: the index was out of range") }},
		Atom{ID: names[2], Run: passes},
	)
	got := Execute(context.Background(), reg, Input{}, Options{Timeout: 2 * time.Second})
	if got[0].State != 0 || got[2].State != 0 {
		t.Errorf("a panic took its neighbours down: %d, %d", got[0].State, got[2].State)
	}
	v := got[1]
	if v.State != 2 || v.Result != "cannot-run" || v.Atom != names[1] {
		t.Errorf("the panicking atom settled %+v", v)
	}
	if !strings.Contains(v.Reason, "boom: the index was out of range") || !strings.Contains(v.Reason, "panicked") {
		t.Errorf("the panic text must ride in the reason: %q", v.Reason)
	}
	if v.StartedAt == "" || v.FinishedAt == "" {
		t.Errorf("an atom that started and panicked has a time: %q %q", v.StartedAt, v.FinishedAt)
	}
}

func TestExecuteSettlesAPanicWithAnErrorValue(t *testing.T) {
	reg := registry(t, Atom{ID: ids(1)[0], Run: func(context.Context, checks.AtomDef, Input) checks.Verdict {
		panic(errors.New("an error value"))
	}})
	if v := Execute(context.Background(), reg, Input{}, Options{})[0]; v.State != 2 || !strings.Contains(v.Reason, "an error value") {
		t.Errorf("settled %+v", v)
	}
}

func TestExecuteSettlesAnAtomThatMissesItsDeadlineAsCannotRun(t *testing.T) {
	names := ids(2)
	// The first atom ignores its context entirely: only the runner's own
	// select can settle it.
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	reg := registry(t,
		Atom{ID: names[0], Run: func(context.Context, checks.AtomDef, Input) checks.Verdict { <-stuck; return checks.Verdict{} }},
		Atom{ID: names[1], Run: passes},
	)
	start := time.Now()
	got := Execute(context.Background(), reg, Input{}, Options{Workers: 2, Timeout: 30 * time.Millisecond})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the vector waited %v for an atom with a 30ms deadline", elapsed)
	}
	if v := got[0]; v.State != 2 || !strings.Contains(v.Reason, "no answer") || !strings.Contains(v.Reason, "30ms") {
		t.Errorf("the stuck atom settled %+v", v)
	}
	if got[0].StartedAt == "" || got[0].FinishedAt == "" {
		t.Errorf("an atom that started and timed out has a time: %q %q", got[0].StartedAt, got[0].FinishedAt)
	}
	if got[1].State != 0 {
		t.Errorf("the deadline of one atom is not the other's: state %d", got[1].State)
	}
}

func TestExecuteHandsTheAtomItsDeadline(t *testing.T) {
	var deadline time.Time
	var ok bool
	reg := registry(t, Atom{ID: ids(1)[0], Run: func(ctx context.Context, a checks.AtomDef, _ Input) checks.Verdict {
		deadline, ok = ctx.Deadline()
		return checks.VerdictOf(a, 0, "x")
	}})
	Execute(context.Background(), reg, Input{}, Options{Timeout: time.Hour})
	if !ok || time.Until(deadline) < 59*time.Minute || time.Until(deadline) > time.Hour {
		t.Errorf("deadline %v (set %v), want about an hour from now", deadline, ok)
	}
	ok = false
	Execute(context.Background(), reg, Input{}, Options{})
	if !ok || time.Until(deadline) > DefaultTimeout || time.Until(deadline) < DefaultTimeout-10*time.Second {
		t.Errorf("the zero Options deadline is DefaultTimeout (%v), got %v away", DefaultTimeout, time.Until(deadline))
	}
}

func TestExecuteSettlesACancelledCallerAsCannotRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	reg := registry(t, Atom{ID: ids(1)[0], Run: func(context.Context, checks.AtomDef, Input) checks.Verdict {
		<-stuck
		return checks.Verdict{}
	}})
	if v := Execute(ctx, reg, Input{}, Options{})[0]; v.State != 2 || !strings.Contains(v.Reason, "canceled") {
		t.Errorf("an atom run for a caller that has gone settled %+v", v)
	}
}

func TestExecuteScopeChanged(t *testing.T) {
	names := ids(1)
	for _, tc := range []struct {
		name       string
		in         Input
		wantCalled bool
		wantState  int
		wantResult string
		wantReason string
		wantTimed  bool
	}{
		{"a change set to read runs the atom", Input{Changed: []string{"a.go"}}, true, 0, "pass", "", true},
		{"an empty change set is absent and the atom is not called", Input{Changed: []string{}}, false, 0, "absent", "ABSENT - no file was added", false},
		{"no change set at all is the same", Input{}, false, 0, "absent", "ABSENT", false},
		{"a change set that would not compute is a 2, never a pass", Input{ChangedErr: errors.New("git said no"), Changed: []string{"a.go"}}, false, 2, "cannot-run", "git said no", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			reg := registry(t, Atom{ID: names[0], Scope: ScopeChanged, Run: func(_ context.Context, a checks.AtomDef, _ Input) checks.Verdict {
				called = true
				return checks.VerdictOf(a, 0, "x")
			}})
			v := Execute(context.Background(), reg, tc.in, Options{})[0]
			if called != tc.wantCalled || v.State != tc.wantState || v.Result != tc.wantResult {
				t.Errorf("called=%v state=%d result=%q, want %v/%d/%q\n%s", called, v.State, v.Result, tc.wantCalled, tc.wantState, tc.wantResult, v.Reason)
			}
			if !strings.Contains(v.Reason, tc.wantReason) {
				t.Errorf("reason %q lacks %q", v.Reason, tc.wantReason)
			}
			if timed := v.StartedAt != "" && v.FinishedAt != ""; timed != tc.wantTimed {
				t.Errorf("timed=%v, want %v (%q..%q)", timed, tc.wantTimed, v.StartedAt, v.FinishedAt)
			}
		})
	}
}

func TestExecuteScopeTreeDoesNotNeedTheChangeSet(t *testing.T) {
	reg := registry(t, Atom{ID: ids(1)[0], Scope: ScopeTree, Run: passes})
	if v := Execute(context.Background(), reg, Input{ChangedErr: errors.New("no git")}, Options{})[0]; v.State != 0 {
		t.Errorf("a whole-tree atom ran into the change set's failure: %+v", v)
	}
}

func TestExecuteStampsStartAndFinishFromTheClock(t *testing.T) {
	base := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	var ticks atomic.Int64
	clock := func() time.Time { return base.Add(time.Duration(ticks.Add(1)) * time.Second) }
	reg := registry(t, Atom{ID: ids(1)[0], Run: passes})
	v := Execute(context.Background(), reg, Input{}, Options{Workers: 1, Clock: clock})[0]
	if v.StartedAt != "2026-10-08T12:00:01.000Z" || v.FinishedAt != "2026-10-08T12:00:02.000Z" {
		t.Errorf("stamped %q..%q", v.StartedAt, v.FinishedAt)
	}
}

func TestExecuteOfNothingIsNothing(t *testing.T) {
	if got := Execute(context.Background(), registry(t), Input{}, Options{}); len(got) != 0 {
		t.Errorf("an empty registry answered %d verdicts", len(got))
	}
}
