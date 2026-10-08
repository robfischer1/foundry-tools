package atoms

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// DefaultTimeout is each atom's own deadline when Options names none. The four
// atoms ported first read a tree from local disk and answer in milliseconds; a
// minute is slack for a cold disk, and short enough that one wedged atom is
// found by the vector and not by the Job's twenty-minute silence watcher.
const DefaultTimeout = time.Minute

// Options tune a run. The zero value is the production configuration.
type Options struct {
	// Workers is the pool size; 0 is runtime.GOMAXPROCS(0).
	Workers int
	// Timeout is each atom's deadline; 0 is DefaultTimeout.
	Timeout time.Duration
	// Clock stamps StartedAt and FinishedAt; nil is time.Now.
	Clock func() time.Time
}

// Execute answers the registry's vector: one verdict per atom, IN REGISTRY ORDER
// whatever order they finished in.
//
// EVERY ATOM SETTLES. A worker pool runs them concurrently; each atom sits
// behind recover (a panic is THAT atom's 2, carrying the panic text, and does
// not take the process or its neighbours down) and behind its own deadline (an
// atom that does not answer is a 2, not a hung vector). An atom that never
// started — absent for want of a change, or settled 2 because the change set
// would not compute — carries no timestamps, as in the module's vector: a time
// it did not run at is a fact nobody measured.
func Execute(ctx context.Context, reg *Registry, in Input, opt Options) []checks.Verdict {
	workers := opt.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	clock := opt.Clock
	if clock == nil {
		clock = time.Now
	}

	out := make([]checks.Verdict, len(reg.entries))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(workers, len(reg.entries)) {
		wg.Go(func() {
			for i := range jobs {
				started := clock()
				v, ran := settle(ctx, reg.entries[i], in, timeout)
				if ran {
					v = checks.Timed(v, started, clock())
				}
				out[i] = v
			}
		})
	}
	for i := range reg.entries {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return out
}

// settle answers one atom's verdict, and whether the atom was actually started.
func settle(ctx context.Context, e entry, in Input, timeout time.Duration) (checks.Verdict, bool) {
	id := e.def.ID
	if e.atom.Scope == ScopeChanged {
		if in.ChangedErr != nil {
			return checks.VerdictOf(e.def, int(checks.StateCannotRun), fmt.Sprintf(
				"%s: CANNOT RUN - the change set would not compute: %v", id, in.ChangedErr)), false
		}
		if len(in.Changed) == 0 {
			return checks.VerdictOf(e.def, int(checks.StatePass), id+
				": ABSENT - no file was added, modified or renamed in this change set. Nothing was checked and nothing needed to be."), false
		}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// BUFFERED, so an atom that answers after its deadline can still return and
	// its goroutine end; the verdict it computed is simply never read.
	done := make(chan checks.Verdict, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- checks.VerdictOf(e.def, int(checks.StateCannotRun), fmt.Sprintf(
					"%s: CANNOT RUN - the atom panicked: %v", id, p))
			}
		}()
		done <- e.atom.Run(ctx, e.def, in)
	}()
	select {
	case v := <-done:
		return v, true
	case <-ctx.Done():
		return checks.VerdictOf(e.def, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - no answer (%v, limit %s)", id, ctx.Err(), timeout)), true
	}
}
