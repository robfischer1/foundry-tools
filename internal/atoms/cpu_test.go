package atoms

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"runtime/pprof"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/pprof/profile"

	"dagger/foundry-tools/internal/checks"
)

// THE PROFILE IS PROCESS-GLOBAL, so no test here is parallel: one that started a
// profile while another held it would measure nothing. A run under -cpuprofile
// holds it for the whole binary, and these tests fail there rather than pass
// having measured nothing.

// spin burns about d of CPU on the calling goroutine: CPU, not wall time, read
// off the process's rusage, so a loaded host that starves the test makes it
// slower and not wrong.
// It reads the kernel itself rather than ProcessCPU, so a mutant of that
// cannot turn it into a loop that never ends; a minute of wall time caps it.
func spin(d time.Duration) {
	cpu := func() time.Duration {
		var ru syscall.Rusage
		_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
		return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
	}
	from, deadline := cpu(), time.Now().Add(time.Minute)
	x := 0
	for cpu()-from < d && time.Now().Before(deadline) {
		for range 100000 {
			x++
		}
	}
	_ = x
}

// spinScript is a shell loop that burns CPU in a child process.
var spinScript = []string{"-c", "i=0; while [ $i -lt 300000 ]; do i=$((i+1)); done"}

func started(t *testing.T) *Profile {
	t.Helper()
	p := StartProfile()
	if p == nil {
		t.Fatal("a CPU profile is already running in this process, so nothing here can be measured")
	}
	return p
}

func TestALabelledGoroutinesChildrenAreItsOwn(t *testing.T) {
	p := started(t)
	var wg sync.WaitGroup
	pprof.Do(context.Background(), pprof.Labels(cpuLabel, "parent"), func(context.Context) {
		// The spin is in a goroutine the labelled one STARTS, not in the
		// labelled goroutine itself: inheritance is what this proves.
		wg.Go(func() { spin(300 * time.Millisecond) })
	})
	wg.Wait()
	spin(100 * time.Millisecond) // unlabelled: nobody's
	got, ok := p.Stop()
	if !ok {
		t.Fatal("the profile did not parse")
	}
	if got["parent"] < 150*time.Millisecond {
		t.Errorf("the child goroutine's CPU went to %v under the parent's label, want most of 300ms; all: %v", got["parent"], got)
	}
	if len(got) != 1 {
		t.Errorf("labels %v, want the parent's alone: unlabelled samples are nobody's", got)
	}
}

func TestAProfileAlreadyRunningIsNotStartedAgain(t *testing.T) {
	p := started(t)
	defer p.Stop()
	if again := StartProfile(); again != nil {
		again.Stop()
		t.Fatal("a second profile started while one was running")
	}
	var none *Profile
	if got, ok := none.Stop(); got != nil || ok {
		t.Errorf("a profile that never started answered %v, %v", got, ok)
	}
}

func profileBytes(t *testing.T, p *profile.Profile) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := p.Write(&b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestCPUByLabelSumsTheCPUColumnByAtom(t *testing.T) {
	cpuProfile := func(samples ...*profile.Sample) *profile.Profile {
		return &profile.Profile{
			SampleType: []*profile.ValueType{{Type: "samples", Unit: "count"}, {Type: "cpu", Unit: "nanoseconds"}},
			Sample:     samples,
		}
	}
	sample := func(cpu int64, labels map[string][]string) *profile.Sample {
		return &profile.Sample{Value: []int64{1, cpu}, Label: labels}
	}
	for _, tc := range []struct {
		name string
		raw  []byte
		want map[string]time.Duration
		ok   bool
	}{
		{"not a profile", []byte("garbage"), nil, false},
		{"no cpu column", profileBytes(t, &profile.Profile{
			SampleType: []*profile.ValueType{{Type: "alloc_space", Unit: "bytes"}},
			Sample:     []*profile.Sample{{Value: []int64{7}, Label: map[string][]string{cpuLabel: {"a"}}}},
		}), nil, false},
		{"another type in nanoseconds", profileBytes(t, &profile.Profile{
			SampleType: []*profile.ValueType{{Type: "wall", Unit: "nanoseconds"}},
			Sample:     []*profile.Sample{{Value: []int64{7}, Label: map[string][]string{cpuLabel: {"a"}}}},
		}), nil, false},
		{"cpu in another unit", profileBytes(t, &profile.Profile{
			SampleType: []*profile.ValueType{{Type: "cpu", Unit: "count"}},
			Sample:     []*profile.Sample{{Value: []int64{7}, Label: map[string][]string{cpuLabel: {"a"}}}},
		}), nil, false},
		{"summed by label, the cpu column only", profileBytes(t, cpuProfile(
			sample(10, map[string][]string{cpuLabel: {"a"}}),
			sample(5, map[string][]string{cpuLabel: {"a"}}),
			sample(3, map[string][]string{cpuLabel: {"b"}}),
			sample(99, nil),
			sample(98, map[string][]string{"other": {"a"}}),
			sample(97, map[string][]string{cpuLabel: {"a", "b"}}),
		)), map[string]time.Duration{"a": 15, "b": 3}, true},
		{"no samples is known and empty", profileBytes(t, cpuProfile()), map[string]time.Duration{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := cpuByLabel(tc.raw)
			if ok != tc.ok || len(got) != len(tc.want) {
				t.Fatalf("got %v, %v; want %v, %v", got, ok, tc.want, tc.ok)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("%s: %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

func TestAProgramIsChargedToTheAtomThatRanIt(t *testing.T) {
	m := NewMeter()
	ctx := withMeter(context.Background(), m, "a")
	if _, code := RunProgram(ctx, Cmd{Name: "sh", Args: spinScript}); code != 0 {
		t.Fatalf("sh exited %d", code)
	}
	if d, ran := m.children("a"); !ran || d < 10*time.Millisecond {
		t.Errorf("a's programs cost %v (ran %v), want the shell's spin", d, ran)
	}
	// git is the other choke point: the change set's reads go through it.
	if out, code := git(withMeter(context.Background(), m, "g"), t.TempDir(), "version"); code != 0 {
		t.Fatalf("git exited %d: %s", code, out)
	}
	if _, ran := m.children("g"); !ran {
		t.Error("the git an atom ran was charged to nobody")
	}
	// A program that never started has no state to charge; a context that names
	// no atom charges nobody; a nil meter takes the charge and keeps nothing.
	RunProgram(ctx, Cmd{Name: "/no/such/program"})
	RunProgram(context.Background(), Cmd{Name: "true"})
	RunProgram(withMeter(context.Background(), nil, "b"), Cmd{Name: "true"})
	if _, ran := m.children("b"); ran {
		t.Error("b was charged on a meter it was not given")
	}
	if len(m.child) != 2 {
		t.Errorf("charged %v, want a and g alone", m.child)
	}
}

func TestChargeChildAddsUserAndSystemTime(t *testing.T) {
	m := NewMeter()
	ctx := withMeter(context.Background(), m, "a")
	var want time.Duration
	for range 2 {
		cmd := exec.Command("sh", spinScript...)
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
		chargeChild(ctx, cmd.ProcessState)
		want += cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()
	}
	if got, _ := m.children("a"); got != want {
		t.Errorf("charged %v, want the sum of both runs' user and system time, %v", got, want)
	}
	chargeChild(ctx, nil)
	if got, _ := m.children("a"); got != want {
		t.Errorf("a program with no state changed the charge to %v", got)
	}
}

func TestStampWritesWhatIsKnownAndOnlyThat(t *testing.T) {
	ms := func(n int64) *int64 { return &n }
	vector := func() []checks.Verdict {
		return []checks.Verdict{
			{Atom: "ran-go", StartedAt: "t"},
			{Atom: "ran-exec", StartedAt: "t"},
			{Atom: "absent"},
			{Atom: "absent-charged"},
		}
	}
	m := NewMeter()
	m.charge("ran-exec", 2400*time.Microsecond)
	m.charge("absent-charged", time.Second)
	inproc := map[string]time.Duration{"ran-go": 1500 * time.Microsecond, "ran-exec": 100 * time.Microsecond}
	for _, tc := range []struct {
		name     string
		inproc   map[string]time.Duration
		profiled bool
		want     []*int64
	}{
		{"profiled: in process plus children, rounded", inproc, true, []*int64{ms(2), ms(3), nil, nil}},
		{"not profiled: an atom that ran a program still knows its children", nil, false, []*int64{nil, ms(2), nil, nil}},
		{"profiled with no samples is zero, not unknown", map[string]time.Duration{}, true, []*int64{ms(0), ms(2), nil, nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := vector()
			m.Stamp(v, tc.inproc, tc.profiled)
			for i, w := range tc.want {
				got := v[i].CPUMs
				if (got == nil) != (w == nil) || (got != nil && *got != *w) {
					t.Errorf("%s: %v, want %v", v[i].Atom, deref(got), deref(w))
				}
			}
		})
	}
}

func deref(p *int64) any {
	if p == nil {
		return "nil"
	}
	return *p
}

func TestMillisRoundsToTheNearest(t *testing.T) {
	for d, want := range map[time.Duration]int64{
		0: 0, 499 * time.Microsecond: 0, 500 * time.Microsecond: 1, 1499 * time.Microsecond: 1, 2 * time.Second: 2000,
	} {
		if got := millis(d); got != want {
			t.Errorf("millis(%v) = %d, want %d", d, got, want)
		}
	}
}

func TestProcessCPUCountsTheProcessAndItsChildren(t *testing.T) {
	before := ProcessCPU()
	spin(50 * time.Millisecond)
	cmd := exec.Command("sh", spinScript...)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	child := cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()
	if moved := ProcessCPU() - before; moved < 50*time.Millisecond+child/2 {
		t.Errorf("the process's CPU moved %v over a 50ms spin and a child's %v", moved, child)
	}
}

// THE ATOMS' CPU THROUGH THE RUNNER: an atom that spins in process and one that
// runs a program are each charged their own, and the CPU never moves a verdict.
func TestExecuteChargesEachAtomItsOwnCPU(t *testing.T) {
	names := ids(3)
	run := map[string]RunFunc{
		names[0]: func(_ context.Context, a checks.AtomDef, _ Input) checks.Verdict {
			var wg sync.WaitGroup
			wg.Go(func() { spin(250 * time.Millisecond) })
			wg.Wait()
			return checks.VerdictOf(a, 0, "spun")
		},
		names[1]: func(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
			_, code := in.run(ctx, Cmd{Name: "sh", Args: spinScript})
			return checks.VerdictOf(a, code, "ran sh")
		},
		names[2]: func(_ context.Context, a checks.AtomDef, _ Input) checks.Verdict {
			return checks.VerdictOf(a, 1, "found")
		},
	}
	var atoms []Atom
	for _, id := range names {
		atoms = append(atoms, Atom{ID: id, Run: run[id]})
	}
	p := started(t)
	m := NewMeter()
	vector := Execute(context.Background(), registry(t, atoms...), Input{}, Options{Workers: 3, Timeout: 10 * time.Second, CPU: m})
	inproc, ok := p.Stop()
	m.Stamp(vector, inproc, ok)
	if !ok {
		t.Fatal("the profile did not parse")
	}
	if c := vector[0].CPUMs; c == nil || *c < 150 {
		t.Errorf("the spinning atom's CPU is %v, want most of 250ms", deref(c))
	}
	if c := vector[1].CPUMs; c == nil || *c < 10 {
		t.Errorf("the exec atom's CPU is %v, want its shell's", deref(c))
	}
	if d, _ := m.children(names[0]); d != 0 {
		t.Errorf("the spinning atom ran no program, but was charged %v", d)
	}
	if vector[0].State != 0 || vector[1].State != 0 || vector[2].State != 1 {
		t.Errorf("states %d %d %d, want 0 0 1", vector[0].State, vector[1].State, vector[2].State)
	}
}

// THE BINARY'S OUTPUT: the vector, then the trailer, and the vector reads as it
// always did. A profile that will not start and a kernel that will not say leave
// the CPU unknown, and every verdict exactly as it was.
func TestRunPrintsTheStageCPUAfterTheVector(t *testing.T) {
	dir := newRepo(t)
	put(t, dir, "ok.yml", "a: 1\n")
	commitAll(t, dir, "root")
	parse := func(out string) ([]checks.Verdict, checks.RunTrailer) {
		t.Helper()
		v, tr, err := checks.ParseRun(out)
		if err != nil {
			t.Fatal(err)
		}
		return v, tr
	}

	var measured, blind bytes.Buffer
	if code := Run(context.Background(), []string{"-root", dir, "-stage", "precommit"}, &measured, &bytes.Buffer{}, fixedNow); code != 0 {
		t.Fatalf("exit %d", code)
	}
	v1, tr := parse(measured.String())
	if tr.StageCPUMs == nil {
		t.Fatal("no stage CPU after the vector")
	}
	known := 0
	for _, v := range v1 {
		if v.CPUMs != nil {
			known++
		}
	}
	if known == 0 {
		t.Error("no atom's CPU is known: the profile did not start, or nothing ran")
	}

	origStart, origCPU := startProfile, processCPU
	t.Cleanup(func() { startProfile, processCPU = origStart, origCPU })
	startProfile = func() *Profile { return nil }
	processCPU = func() time.Duration { return 1499 * time.Microsecond }
	if code := Run(context.Background(), []string{"-root", dir, "-stage", "precommit"}, &blind, &bytes.Buffer{}, fixedNow); code != 0 {
		t.Fatalf("exit %d", code)
	}
	v2, tr2 := parse(blind.String())
	if tr2.StageCPUMs == nil || *tr2.StageCPUMs != 1 {
		t.Errorf("stage CPU %v, want the kernel's 1.499ms as 1", tr2.StageCPUMs)
	}
	if !strings.HasSuffix(strings.TrimSpace(blind.String()), "\n{\"stage_cpu_ms\":1}") {
		t.Errorf("the trailer is not the last line: %q", blind.String()[max(0, blind.Len()-80):])
	}
	if len(v1) != len(v2) {
		t.Fatalf("%d verdicts measured, %d blind", len(v1), len(v2))
	}
	for i := range v1 {
		a, b := v1[i], v2[i]
		a.CPUMs, b.CPUMs = nil, nil
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		if !bytes.Equal(ja, jb) {
			t.Errorf("measuring moved %s:\n%s\n%s", a.Atom, ja, jb)
		}
	}
}
