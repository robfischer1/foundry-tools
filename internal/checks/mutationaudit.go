package checks

import (
	"encoding/json"
	"strings"
)

// THE SOUNDNESS AUDIT (incremental mutation, M6). A sampled reuse run grades
// every unit cold anyway, and each grading a lookup answered is held against
// the cold grading of the same unit. They must agree — counts and every
// listed mutant — because a reused grading is only sound if grading that
// unit again would have said the same. A disagreement is a hole in the reuse
// rule (a test reading outside its closure, the network, a flake) and is
// counted where the record lands (Daedalus) so it can be alerted on.

// Audit results: AuditMatch, or AuditMismatch followed by the units.
const (
	AuditMatch    = "match"
	AuditMismatch = "mismatch"
)

// AuditGradings holds each reused grading against the cold grading of its
// unit, and answers AuditMatch, or AuditMismatch and the units that differ.
// A unit the cold run did not grade at all differs.
func AuditGradings(reused []ReusedGrading, cold []Grading) string {
	byUnit := map[string]Grading{}
	for _, g := range cold {
		byUnit[g.Unit] = g
	}
	var differ []string
	for _, r := range reused {
		c, ok := byUnit[r.Unit]
		if !ok || canonical(r.Counts, r.Mutants) != canonical(c.Counts, c.Mutants) {
			differ = append(differ, r.Unit)
		}
	}
	if len(differ) > 0 {
		return AuditMismatch + ": " + strings.Join(differ, ", ")
	}
	return AuditMatch
}

// canonical is a grading's outcome as one comparable string.
func canonical(c GradingCounts, mutants []ScoredMutant) string {
	if mutants == nil {
		mutants = []ScoredMutant{}
	}
	raw, _ := json.Marshal([]any{c, mutants})
	return string(raw)
}

// FoldAudits is one atom's audit over its modules: every module's mismatch,
// else a match if any module audited, else nothing.
func FoldAudits(audits []string) string {
	var mismatched []string
	matched := false
	for _, a := range audits {
		if rest, ok := strings.CutPrefix(a, AuditMismatch+": "); ok {
			mismatched = append(mismatched, rest)
		}
		matched = matched || a == AuditMatch
	}
	if len(mismatched) > 0 {
		return AuditMismatch + ": " + strings.Join(mismatched, ", ")
	}
	if matched {
		return AuditMatch
	}
	return ""
}
