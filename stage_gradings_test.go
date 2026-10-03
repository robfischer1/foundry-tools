package main

import (
	"encoding/json"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// A MUTATION ATOM'S GRADINGS RIDE THE RECORD, under its own line, and no other
// atom grows the key.
func TestTheRecordCarriesTheMutationGradings(t *testing.T) {
	g := []checks.Grading{{Lang: "go", Unit: "internal/a", Hash: "h", Reusable: true, Mutants: []checks.ScoredMutant{}}}
	res := stageResult(checks.Stage{Name: "mutation", Ran: []checks.StageAtom{
		{Atom: "go:mutation", Result: "pass", Gradings: g},
		{Atom: "go:vet", Result: "pass"},
	}})
	rec, err := res.Record()
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Atoms []map[string]json.RawMessage
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(rec, runRecordSentinel+" ")), &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Atoms) != 2 {
		t.Fatalf("atoms: %s", rec)
	}
	var got []checks.Grading
	if err := json.Unmarshal(wire.Atoms[0]["gradings"], &got); err != nil || len(got) != 1 || got[0].Unit != "internal/a" || !got[0].Reusable {
		t.Fatalf("the mutation atom's gradings did not ride: %s (%v)", wire.Atoms[0]["gradings"], err)
	}
	if string(wire.Atoms[0]["Atom"]) != `"go:mutation"` {
		t.Fatalf("the atom's own fields moved: %s", rec)
	}
	if _, ok := wire.Atoms[1]["gradings"]; ok {
		t.Fatalf("an atom with no gradings grew the key: %s", rec)
	}
}
