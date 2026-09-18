// Package gatelane holds the gate lane's decisions as pure functions: a
// cannot-run vector, the vector's worst state and summary. gate.go at the
// module root is left with the chain. (The receipt the door's join read and
// the fold of hades's attest answer lived here until CA F16; the gate settles
// from its exit now.)
//
// Ported from infra's ca-gate gate.py, rule for rule; each function names
// what it replaces.
package gatelane

import (
	"encoding/json"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// The door's three verdicts for a lane.
const (
	Clean       = 0
	Findings    = 1
	CouldNotRun = 2
)

// CannotRunVector is the one-atom vector a gate settles on when it could not
// grade: the lane's own name, state 2, and why (gate.py cannot_run_vector).
func CannotRunVector(lane, stage, reason string) []checks.Verdict {
	if stage == "" {
		stage = "prepush"
	}
	return []checks.Verdict{{Atom: lane, Stage: stage, Lane: "any", State: CouldNotRun, Result: "cannot-run", Reason: truncate(reason, 1200)}}
}

// ParseVector reads the module's vector.
func ParseVector(raw string) ([]checks.Verdict, error) {
	var v []checks.Verdict
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("the module's output is not a verdict vector: %s", truncate(raw, 300))
	}
	return v, nil
}

// Worst is the vector's exit: its highest state, where any state other than
// the pass and the finding counts as a could-not-run (gate.py's max and clamp,
// failing closed on a negative state too).
func Worst(v []checks.Verdict) int {
	worst := Clean
	for _, a := range v {
		s := a.State
		if s != Clean && s != Findings {
			s = CouldNotRun
		}
		worst = max(worst, s)
	}
	return worst
}

// Summary counts the vector and names every red atom with its reason.
func Summary(v []checks.Verdict) string {
	var pass, findings, cannot int
	var red []string
	for _, a := range v {
		switch a.State {
		case Clean:
			pass++
			continue
		case Findings:
			findings++
		default:
			cannot++
		}
		red = append(red, fmt.Sprintf("%s state=%d %s", a.Atom, a.State, truncate(a.Reason, 300)))
	}
	s := fmt.Sprintf("%d atom(s): %d pass, %d findings, %d cannot-run", len(v), pass, findings, cannot)
	if len(red) > 0 {
		s += "\n" + strings.Join(red, "\n")
	}
	return s
}

func truncate(s string, n int) string { return s[:min(len(s), n)] }
