package main

import (
	"context"
	"sync"
)

// THE BINARY'S WHOLE CPU, PER STAGE. Each atom's own CPU rides on its verdict
// (internal/atoms cpu.go); what the binary spent that no atom is answerable for
// (reading the tree, the runner, the profile itself) is only visible beside
// the process's total, which the binary prints after its vector
// (checks.RunTrailer). A lane can run the binary more than once — the pull
// path casts for each stage it grades — so the record carries the sum, carried
// to cast on the lane's context.

// cpuTally sums the binary's stage CPU over a lane's casts. A nil tally adds
// nothing and totals nothing.
type cpuTally struct {
	mu    sync.Mutex
	ms    int64
	known bool
}

// add adds one run's CPU; nil (the binary did not say) adds nothing.
func (t *cpuTally) add(ms *int64) {
	if t == nil || ms == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ms += *ms
	t.known = true
}

// total is the sum, or nil when no run said.
func (t *cpuTally) total() *int64 {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.known {
		return nil
	}
	ms := t.ms
	return &ms
}

type tallyKey struct{}

// withTally is ctx with the lane's tally on it.
func withTally(ctx context.Context, t *cpuTally) context.Context {
	return context.WithValue(ctx, tallyKey{}, t)
}

// tallyOf is the lane's tally, nil when the lane keeps none (a shadow's run,
// the local hook's stages).
func tallyOf(ctx context.Context) *cpuTally {
	t, _ := ctx.Value(tallyKey{}).(*cpuTally)
	return t
}
