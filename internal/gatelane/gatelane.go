// Package gatelane holds the gate lane's decisions as pure functions: the
// receipt the door's join reads, a cannot-run vector, the vector's worst state
// and summary, and what hades answered the attestation. gate.go at the module
// root is left with the chain.
//
// Ported from infra's ca-gate gate.py and attest.py, rule for rule; each
// function names what it replaces.
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

// Receipt is what tartarus_attest_emit lands on ci-attest and the door's join
// reads (ourea internal/gatejoin AttestEvent). svid is not a field here:
// hadescall sets it from the SVID it holds, so the receipt names the identity
// that actually delivered it.
type Receipt struct {
	Tree      string           `json:"tree"`
	ModulePin string           `json:"module_pin"`
	ClientID  string           `json:"client_id"`
	Verdict   []checks.Verdict `json:"verdict"`
}

// ClientID is the receipt's stamp, "<attester>:<tree>". The join reads the
// attester as everything before the FIRST colon and refuses a record whose
// stamp names a different tree than it claims (gatejoin.StampAgrees), so the
// attester carries no colon.
func ClientID(star, tree string) string {
	return "ca-gate/" + strings.ReplaceAll(star, ":", "_") + ":" + tree
}

// CannotRunVector is the one-atom vector a gate attests when it could not
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

// Outcome is what became of one attestation attempt.
type Outcome int

const (
	// Landed: ci-attest has the record (attest_emit answers only once it is
	// durable), so the door's projector will see it.
	Landed Outcome = iota
	// HeldDown: hades asked the caller to wait; the attempt is repeated.
	HeldDown
	// Refused: nothing a retry changes.
	Refused
)

// Attested folds hades's answer to tartarus_attest_emit (attest.py): 200 has
// landed; a 5xx hold-down is waited out; a 403 says whether hades could not
// derive a principal or its policy refused the one it derived; anything else
// is refused, failing closed.
func Attested(status int, body string) (Outcome, string) {
	switch {
	case status == 200:
		return Landed, "attested on ci-attest"
	case status == 403 && strings.Contains(body, "unidentifiable caller"):
		return Refused, "hades did not DERIVE a principal from this pod's SVID, so the PDP was never consulted — a shape bug in the ClusterSPIFFEID, not a missing grant"
	case status == 403:
		return Refused, "hades identified this pod and the POLICY refused tartarus_attest_emit — check policy/authz_grants in foundry-dies: " + truncate(body, 300)
	case status >= 500 && strings.Contains(body, "hold-down"):
		return HeldDown, "attest_emit held down: " + truncate(body, 200)
	}
	return Refused, fmt.Sprintf("attest_emit HTTP %d: %s", status, truncate(body, 400))
}

func truncate(s string, n int) string { return s[:min(len(s), n)] }
