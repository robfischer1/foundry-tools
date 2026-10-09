package atoms

import (
	"bytes"
	"context"
	"os"
	"runtime/pprof"
	"sync"
	"syscall"
	"time"

	"github.com/google/pprof/profile"

	"dagger/foundry-tools/internal/checks"
)

// WHAT EACH ATOM COST IN CPU. The atoms run concurrently in one process, so the
// process's own rusage says what the run cost and nothing about who spent it.
// Two meters split it:
//
//   - IN PROCESS, a CPU profile for the length of the run, every atom's goroutine
//     labelled `atom=<id>` (pprof.Do in settle). A goroutine the atom starts
//     inherits the label, so a pure-Go atom's helpers are its own. The samples
//     are summed per label at the end.
//   - CHILDREN, the rusage of every program an atom execs (RunProgram, git),
//     charged to the atom whose context started it.
//
// IT IS EVIDENCE, NEVER A VERDICT. A profile that would not start (one is
// already running: a test under -cpuprofile) or would not parse leaves the
// in-process half unknown, and nothing an atom answers depends on any of it.

// cpuLabel is the profile label the atoms' goroutines carry.
const cpuLabel = "atom"

// Meter tallies the CPU of the programs each atom execs. The zero value is not
// usable; NewMeter makes one. A nil *Meter charges nothing.
type Meter struct {
	mu    sync.Mutex
	child map[string]time.Duration
}

// NewMeter answers an empty meter.
func NewMeter() *Meter { return &Meter{child: map[string]time.Duration{}} }

// charge adds d to the atom's children.
func (m *Meter) charge(id string, d time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.child[id] += d
}

// children answers what the atom's programs cost, and whether it ran any.
func (m *Meter) children(id string) (time.Duration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.child[id]
	return d, ok
}

// meterKey carries the atom's meter and id on its context.
type meterKey struct{}

type metered struct {
	m  *Meter
	id string
}

// withMeter is the atom's context, its execs charged to id on m.
func withMeter(ctx context.Context, m *Meter, id string) context.Context {
	return context.WithValue(ctx, meterKey{}, metered{m, id})
}

// chargeChild charges a finished program's user and system time to the atom
// whose context ran it. A program that never started has no state; a context
// that names no atom (Collect's git, before any atom) charges nothing, and that
// time is the run's unattributed remainder.
func chargeChild(ctx context.Context, ps *os.ProcessState) {
	at, ok := ctx.Value(meterKey{}).(metered)
	if !ok || ps == nil {
		return
	}
	at.m.charge(at.id, ps.UserTime()+ps.SystemTime())
}

// Profile is a started CPU profile. A nil *Profile is one that would not start,
// and answers nothing.
type Profile struct {
	buf bytes.Buffer
}

// StartProfile starts the process's CPU profile, or answers nil when it will
// not start: profiling is process-global, and one already running is somebody
// else's to stop.
func StartProfile() *Profile {
	p := &Profile{}
	if pprof.StartCPUProfile(&p.buf) != nil {
		return nil
	}
	return p
}

// Stop stops the profile and answers the CPU each atom label spent in process,
// and whether that is known. A profile that would not start or would not parse
// is not known.
func (p *Profile) Stop() (map[string]time.Duration, bool) {
	if p == nil {
		return nil, false
	}
	pprof.StopCPUProfile()
	return cpuByLabel(p.buf.Bytes())
}

// cpuByLabel sums a CPU profile's cpu samples by their atom label. Samples with
// no atom label (the runner, the GC, Collect) are nobody's here.
func cpuByLabel(raw []byte) (map[string]time.Duration, bool) {
	prof, err := profile.ParseData(raw)
	if err != nil {
		return nil, false
	}
	col := -1
	for i, st := range prof.SampleType {
		if st.Type == "cpu" && st.Unit == "nanoseconds" {
			col = i
		}
	}
	if col < 0 {
		return nil, false
	}
	out := map[string]time.Duration{}
	for _, s := range prof.Sample {
		if ids := s.Label[cpuLabel]; len(ids) == 1 {
			out[ids[0]] += time.Duration(s.Value[col])
		}
	}
	return out, true
}

// Stamp writes each started atom's CPU onto its verdict: in process plus its
// children. An atom that never started has none. One that started is known when
// the profile is, or when it ran a program; otherwise its CPU is not known and
// carries nothing, which is not the same as zero.
func (m *Meter) Stamp(vector []checks.Verdict, inproc map[string]time.Duration, profiled bool) {
	for i, v := range vector {
		if v.StartedAt == "" {
			continue
		}
		child, ran := m.children(v.Atom)
		if !profiled && !ran {
			continue
		}
		ms := millis(inproc[v.Atom] + child)
		vector[i].CPUMs = &ms
	}
}

// millis is d in whole milliseconds, rounded.
func millis(d time.Duration) int64 { return d.Round(time.Millisecond).Milliseconds() }

// ProcessCPU is the process's own CPU and its reaped children's, the whole run's
// cost as the kernel counts it — what the atoms' figures are held against.
func ProcessCPU() time.Duration {
	var total time.Duration
	for _, who := range []int{syscall.RUSAGE_SELF, syscall.RUSAGE_CHILDREN} {
		var ru syscall.Rusage
		// THE ERROR IS DROPPED: getrusage fails only for a `who` it does not
		// know or a bad pointer (EINVAL, EFAULT), and these are neither, so a
		// branch for it is one no test can take.
		_ = syscall.Getrusage(who, &ru)
		total += time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
	}
	return total
}
