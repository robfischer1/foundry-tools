package checks

import (
	"strings"
	"testing"
)

func TestSelectNarrowsToTheNamedAtoms(t *testing.T) {
	got, err := Select(SweepAtoms(), "sweep:kubeconform")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(got) != 1 || got[0].ID != "sweep:kubeconform" {
		t.Fatalf("Select chose %v, want just sweep:kubeconform", ids(got))
	}
}

func TestSelectTakesAListAndTolerantWhitespace(t *testing.T) {
	got, err := Select(SweepAtoms(), " sweep:kubeconform , sweep:kube-linter ")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Select chose %v, want two", ids(got))
	}
}

// A selector is written by hand into a CronJob's env, where a typo is
// invisible. An unknown id must be an ERROR: an empty vector would report
// nothing, and nothing reported reads exactly like nothing wrong — the same
// shape as a ruleset matching zero files.
func TestSelectRefusesAnUnknownAtom(t *testing.T) {
	_, err := Select(SweepAtoms(), "sweep:kube_linter")
	if err == nil {
		t.Fatal("a typo'd atom id selected silently")
	}
	if !strings.Contains(err.Error(), "no atom") {
		t.Fatalf("unhelpful error: %v", err)
	}
}

// The other half of the same trap: a REAL atom that this stage does not admit.
// Naming a prepush atom in the sweep's selector must say so rather than answer
// an empty vector — and it must not quietly pull a pull-path atom into a sweep.
func TestSelectRefusesAnAtomTheStageDoesNotAdmit(t *testing.T) {
	_, err := Select(SweepAtoms(), "go:staticcheck")
	if err == nil {
		t.Fatal("a prepush atom was admitted into the sweep's vector")
	}
	if !strings.Contains(err.Error(), "does not admit") {
		t.Fatalf("the error must distinguish 'wrong stage' from 'no such atom': %v", err)
	}
}

func TestSelectRefusesAnEmptyChoice(t *testing.T) {
	if _, err := Select(SweepAtoms(), " , , "); err == nil {
		t.Fatal("a selector naming nothing answered a vector anyway")
	}
}

func TestAtomExistsAgreesWithTheTable(t *testing.T) {
	for _, a := range Atoms {
		if !AtomExists(a.ID) {
			t.Errorf("AtomExists(%q) = false for an atom in the table", a.ID)
		}
	}
	if AtomExists("go:nope") {
		t.Error("AtomExists invented an atom")
	}
}

func ids(as []AtomDef) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.ID)
	}
	return out
}
