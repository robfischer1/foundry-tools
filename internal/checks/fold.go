package checks

import (
	"fmt"
	"strings"
)

// ReusedGrading is a stored grading a lookup answered (Daedalus POST
// /ci/mutants): its key, the run that made it, and what its mutants did. The
// field names are Daedalus's cistore.StoredGrading on the wire.
type ReusedGrading struct {
	Lang      string         `json:"lang"`
	Unit      string         `json:"unit"`
	Hash      string         `json:"hash"`
	Ranges    string         `json:"ranges"`
	RunName   string         `json:"run_name"`
	RunNumber int64          `json:"run_number"`
	Lane      string         `json:"lane"`
	Sha       string         `json:"sha"`
	GradedAt  string         `json:"graded_at"`
	Counts    GradingCounts  `json:"counts"`
	Mutants   []ScoredMutant `json:"mutants"`
}

// reusedInModule answers the reused gradings' listed mutants with their
// files made relative to the module at dir — the paths a run in that module
// names its own mutants by — and the mutants they carry as counts alone.
func reusedInModule(reused []ReusedGrading, dir string) ([]ScoredMutant, GradingCounts) {
	var listed []ScoredMutant
	var extra GradingCounts
	for _, g := range reused {
		extra.Generated += g.Counts.Killed + g.Counts.Inert
		extra.Killed += g.Counts.Killed
		extra.Inert += g.Counts.Inert
		for _, m := range g.Mutants {
			m.File = strings.TrimPrefix(m.File, dir+"/")
			listed = append(listed, m)
		}
	}
	return listed, extra
}

// ReusedSection is the reason's account of what was not graded again: one row
// per reused unit — the run that graded it, at which commit, and what its
// mutants did.
func ReusedSection(reused []ReusedGrading) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### Reused — %d unit(s) graded earlier at the same content, scope and engine\n\n", len(reused))
	b.WriteString("| unit | graded by | at | killed | survived | other |\n|---|---|---|---|---|---|\n")
	for _, g := range reused {
		c := g.Counts
		fmt.Fprintf(&b, "| %s | %s run #%d | %.12s %s | %d | %d | %d |\n", g.Unit, g.Lane, g.RunNumber, g.Sha, g.GradedAt,
			c.Killed, c.Lived+c.NotCovered, c.Generated-c.Killed-c.Lived-c.NotCovered)
	}
	return b.String()
}
