package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// AN ATOM'S PEAK MEMORY RIDES THE RECORD AS mem_peak_bytes beside its cpu_ms,
// and an atom whose peak is not known — a chain's, an absent one — grows no
// key, which Daedalus stores as NULL, not as zero.
func TestTheRecordCarriesThePeakMemoryThatIsKnown(t *testing.T) {
	st := checks.SettleStage("gate", []checks.Verdict{
		{Atom: "dies:resource-referents", Result: "pass", CPUMs: ms(12), MemPeakBytes: ms(52428800)},
		{Atom: "fleet:hadolint", Result: "pass", MemPeakBytes: ms(0)},
		{Atom: "go:vet", Result: "pass"},
		{Atom: "fleet:wit-topics", Result: "absent", MemPeakBytes: ms(3)},
	})
	rec, err := stageResult(st).Record()
	if err != nil {
		t.Fatal(err)
	}
	w := readRecord(t, rec)
	want := map[string]string{"dies:resource-referents": "52428800", "fleet:hadolint": "0", "go:vet": ""}
	for _, a := range w.Atoms {
		var id string
		_ = json.Unmarshal(a["Atom"], &id)
		if got := string(a["mem_peak_bytes"]); got != want[id] {
			t.Errorf("%s: mem_peak_bytes %q, want %q", id, got, want[id])
		}
	}
	if len(w.Atoms) != 3 || len(w.Omitted) != 1 {
		t.Fatalf("atoms %d omitted %d: %s", len(w.Atoms), len(w.Omitted), rec)
	}
	if strings.Contains(rec, `"mem_peak_bytes":3`) {
		t.Errorf("an absent atom carries a peak: %s", rec)
	}
}

// THE GATE'S RECORD CARRIES THE PEAK THE BINARY PUT ON ITS VERDICT, read off
// the vector's own key.
func TestTheGatesRecordCarriesThePeakMemory(t *testing.T) {
	const vector = `[{"atom":"go:vet","stage":"precommit","lane":"go","state":0,"result":"pass","cpu_ms":4,"mem_peak_bytes":8192}]`
	m := gateOn(t, vector)
	gateVector = func(context.Context, *FoundryTools, string, string) (string, error) { return vector, nil }
	w := readRecord(t, runGate(t, m, ""))
	if len(w.Atoms) != 1 || string(w.Atoms[0]["mem_peak_bytes"]) != "8192" {
		t.Errorf("the atom's mem_peak_bytes did not ride: %v", w.Atoms)
	}
}
