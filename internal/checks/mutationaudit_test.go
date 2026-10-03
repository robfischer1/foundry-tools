package checks

import "testing"

func TestAnAuditHoldsEachReusedGradingAgainstTheColdOne(t *testing.T) {
	lived := ScoredMutant{File: "a/x.go", Line: 1, Op: "T", Outcome: OutcomeLived, Status: "LIVED"}
	cold := []Grading{
		{Unit: "a", Counts: GradingCounts{Generated: 2, Killed: 1, Lived: 1}, Mutants: []ScoredMutant{lived}},
		{Unit: "b", Counts: GradingCounts{Generated: 1, Killed: 1}, Mutants: []ScoredMutant{}},
	}
	same := []ReusedGrading{{Unit: "a", Counts: cold[0].Counts, Mutants: []ScoredMutant{lived}}, {Unit: "b", Counts: cold[1].Counts}}
	if got := AuditGradings(same, cold); got != AuditMatch {
		t.Fatalf("the same outcomes: %q", got)
	}
	differs := []ReusedGrading{
		{Unit: "a", Counts: GradingCounts{Generated: 2, Killed: 2}},
		{Unit: "b", Counts: cold[1].Counts},
		{Unit: "gone", Counts: cold[1].Counts},
	}
	if got := AuditGradings(differs, cold); got != "mismatch: a, gone" {
		t.Fatalf("got %q", got)
	}
	killedNow := []ReusedGrading{{Unit: "a", Counts: cold[0].Counts, Mutants: []ScoredMutant{{File: "a/x.go", Line: 1, Op: "T", Outcome: OutcomeNotCovered}}}}
	if got := AuditGradings(killedNow, cold); got != "mismatch: a" {
		t.Fatalf("a mutant whose outcome moved: %q", got)
	}
}

func TestAnAtomsAuditFoldsOverItsModules(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{nil, ""}, {[]string{"", ""}, ""}, {[]string{AuditMatch, ""}, AuditMatch},
		{[]string{AuditMatch, "mismatch: a", "", "mismatch: b, c"}, "mismatch: a, b, c"},
	} {
		if got := FoldAudits(c.in); got != c.want {
			t.Errorf("FoldAudits(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	v := FoldModules(AtomByID("go:mutation"), []ModuleVerdict{{Dir: ".", Verdict: Verdict{Audit: AuditMatch}}, {Dir: "x", Verdict: Verdict{Audit: "mismatch: x/y"}}})
	if v.Audit != "mismatch: x/y" {
		t.Fatalf("fold = %q", v.Audit)
	}
	st := SettleStage("mutation", []Verdict{{Atom: "go:mutation", Result: "pass", Audit: AuditMatch}})
	if st.Ran[0].Audit != AuditMatch {
		t.Fatal("the audit stopped at the stage")
	}
}
