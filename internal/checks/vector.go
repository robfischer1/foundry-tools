package checks

import (
	"encoding/json"
	"fmt"
	"strings"
)

// THE LANE'S VECTOR — the gate lane's decisions as pure functions over a
// []Verdict: the one-atom cannot-run vector, the parse, the worst state (the
// lane's exit) and the summary the settle prints. These were
// internal/gatelane, a package that mirrored infra's ca-gate gate.py rule for
// rule; with the receipt gone (CA F16) four functions and no state were all
// that was left of it, and a package that exists to name the script it
// replaced is the port this plan's F1 called the anti-goal. They live beside
// Verdict and State now, which is what they are about.

// CannotRunVector is the one-atom vector a gate settles on when it could not
// grade: the lane's own name, state 2, and why (gate.py cannot_run_vector).
func CannotRunVector(lane, stage, reason string) []Verdict {
	if stage == "" {
		stage = "prepush"
	}
	// The reason IS this verdict's evidence: there was no run to produce
	// output, so the explanation is all there is, and it belongs in logs as
	// well as in the rendered reason.
	logs, truncated, originalBytes := CaptureLogs(reason)
	return []Verdict{{Atom: lane, Stage: stage, Lane: "any", State: int(StateCannotRun), Result: "cannot-run",
		Reason: truncate(reason, 1200), Logs: logs, Truncated: truncated, OriginalBytes: originalBytes}}
}

// ParseVector reads the module's vector, or the atoms binary's with its
// trailer, which it drops (ParseRun).
func ParseVector(raw string) ([]Verdict, error) {
	v, _, err := ParseRun(raw)
	return v, err
}

// RunTrailer is what the atoms binary prints after its vector: what the run
// cost that no single atom is answerable for.
type RunTrailer struct {
	// StageCPUMs is the binary's whole CPU — its own and every program it
	// reaped — in milliseconds; nil when the kernel would not say.
	StageCPUMs *int64 `json:"stage_cpu_ms,omitempty"`
}

// ParseRun reads a vector and, after it, at most one trailer object.
//
// THE VECTOR STAYS THE FIRST VALUE, a bare array, so the module's own vector
// (Verdicts prints none) reads the same as before. Anything after the trailer,
// a trailer with a key it does not know, or a second value that is not an
// object is a refusal: the output is the binary's or it is not a vector.
func ParseRun(raw string) ([]Verdict, RunTrailer, error) {
	refuse := fmt.Errorf("the module's output is not a verdict vector: %s", truncate(raw, 300))
	dec := json.NewDecoder(strings.NewReader(raw))
	var v []Verdict
	if dec.Decode(&v) != nil {
		return nil, RunTrailer{}, refuse
	}
	var tr RunTrailer
	if rest := strings.TrimSpace(raw[dec.InputOffset():]); rest != "" {
		td := json.NewDecoder(strings.NewReader(rest))
		td.DisallowUnknownFields()
		if td.Decode(&tr) != nil || td.InputOffset() != int64(len(rest)) {
			return nil, RunTrailer{}, refuse
		}
	}
	return v, tr, nil
}

// Worst is the vector's exit: its highest state, where any state other than
// the pass and the finding counts as a could-not-run (gate.py's max and clamp,
// failing closed on a negative state too).
func Worst(v []Verdict) int {
	worst := int(StatePass)
	for _, a := range v {
		s := a.State
		if s != int(StatePass) && s != int(StateFindings) {
			s = int(StateCannotRun)
		}
		worst = max(worst, s)
	}
	return worst
}

// Summary counts the vector and names every red atom with its reason.
func Summary(v []Verdict) string {
	var pass, findings, cannot int
	var red []string
	for _, a := range v {
		switch a.State {
		case int(StatePass):
			pass++
			continue
		case int(StateFindings):
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
