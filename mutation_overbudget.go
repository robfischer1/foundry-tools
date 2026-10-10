package main

import "sync"

import "dagger/foundry-tools/internal/checks"

// overBudgetMemo holds go:mutation's over-budget timeout verdicts by commit.
// THE KEY IS THE COMMIT SHA, THE MODULE AND THE LANE'S CONFIGURATION
// (goMutationEngine: image, gomutants, workers, timeout budget, ...), so a new
// commit or a moved knob misses and runs. Only that one deterministic verdict
// is stored; a transient could-not-run, a finding or a pass never is.
var overBudgetMemo verdictMemo

type verdictMemo struct {
	mu sync.Mutex
	m  map[string]checks.Verdict
}

func (c *verdictMemo) get(key string) (checks.Verdict, bool) {
	if key == "" {
		return checks.Verdict{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[key]
	return v, ok
}

func (c *verdictMemo) put(key string, v checks.Verdict) {
	if key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]checks.Verdict{}
	}
	c.m[key] = v
}

// overBudgetKey is "" without a sha: no commit, nothing to key a memo on, and
// the run is asked as it always was.
func overBudgetKey(sha, dir string) string {
	if sha == "" {
		return ""
	}
	return sha + "|" + dir + "|" + goMutationEngine()
}
