package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

func ms(n int64) *int64 { return &n }

// recordWire is a record as the door reads it: every key, raw.
type recordWire struct {
	StageCPU json.RawMessage `json:"stage_cpu_ms"`
	Atoms    []map[string]json.RawMessage
	Omitted  []map[string]json.RawMessage
}

func readRecord(t *testing.T, rec string) recordWire {
	t.Helper()
	var w recordWire
	if err := json.Unmarshal([]byte(strings.TrimPrefix(rec, runRecordSentinel+" ")), &w); err != nil {
		t.Fatal(err)
	}
	return w
}

// AN ATOM'S CPU RIDES THE RECORD AS cpu_ms, the binary's whole CPU as
// stage_cpu_ms, and an atom or a stage whose CPU is not known grows no key —
// which Daedalus stores as NULL, not as zero.
func TestTheRecordCarriesTheCPUThatIsKnown(t *testing.T) {
	st := checks.SettleStage("gate", []checks.Verdict{
		{Atom: "fleet:check-yaml", Result: "pass", CPUMs: ms(12)},
		{Atom: "fleet:hadolint", Result: "pass", CPUMs: ms(0)},
		{Atom: "go:vet", Result: "pass"},
		{Atom: "fleet:wit-topics", Result: "absent", CPUMs: ms(3)},
	})
	res := stageResult(st)
	res.stageCPU = ms(40)
	rec, err := res.Record()
	if err != nil {
		t.Fatal(err)
	}
	w := readRecord(t, rec)
	if string(w.StageCPU) != "40" {
		t.Errorf("stage_cpu_ms %s, want 40: %s", w.StageCPU, rec)
	}
	want := map[string]string{"fleet:check-yaml": "12", "fleet:hadolint": "0", "go:vet": ""}
	for _, a := range w.Atoms {
		var id string
		_ = json.Unmarshal(a["Atom"], &id)
		if got := string(a["cpu_ms"]); got != want[id] {
			t.Errorf("%s: cpu_ms %q, want %q", id, got, want[id])
		}
	}
	if len(w.Atoms) != 3 || len(w.Omitted) != 1 {
		t.Fatalf("atoms %d omitted %d: %s", len(w.Atoms), len(w.Omitted), rec)
	}
	if _, ok := w.Omitted[0]["cpu_ms"]; ok {
		t.Errorf("an absent atom carries a CPU: %s", rec)
	}

	res.stageCPU = nil
	rec, _ = res.Record()
	if strings.Contains(rec, "stage_cpu_ms") {
		t.Errorf("a stage whose CPU is not known grew the key: %s", rec)
	}
}

func TestATallySumsWhatTheRunsSaid(t *testing.T) {
	var none *cpuTally
	none.add(ms(5))
	if none.total() != nil {
		t.Error("a nil tally totalled something")
	}
	tally := new(cpuTally)
	if tally.total() != nil {
		t.Error("a tally no run spoke to is not unknown")
	}
	tally.add(nil)
	if tally.total() != nil {
		t.Error("a run that did not say made the tally known")
	}
	tally.add(ms(0))
	if got := tally.total(); got == nil || *got != 0 {
		t.Errorf("a run that said zero: %v", got)
	}
	tally.add(ms(5))
	tally.add(ms(7))
	if got := tally.total(); got == nil || *got != 12 {
		t.Errorf("total %v, want 12", got)
	}
	if tallyOf(context.Background()) != nil || tallyOf(withTally(context.Background(), tally)) != tally {
		t.Error("the tally does not ride the context")
	}
}

// THE VOTE READS THE TRAILER and tallies it onto the lane's context; the
// verdicts keep the CPU the binary put on them.
func TestTheCastTalliesTheBinarysStageCPU(t *testing.T) {
	want := []checks.AtomDef{checks.AtomByID("fleet:check-yaml")}
	v := checks.VerdictOf(want[0], 0, "")
	v.CPUMs = ms(9)
	raw, _ := json.Marshal([]checks.Verdict{v})
	run := func(context.Context, *FoundryTools, string, string) (string, error) {
		return string(raw) + "\n" + `{"stage_cpu_ms":31}`, nil
	}
	tally := new(cpuTally)
	m := bareModule()
	m.cast(withTally(context.Background(), tally), run, "precommit", "", want)
	votes := m.cast(withTally(context.Background(), tally), run, "precommit", "", want)
	if got := tally.total(); got == nil || *got != 62 {
		t.Errorf("tally %v, want both runs' 31", got)
	}
	if c := votes["fleet:check-yaml"].CPUMs; c == nil || *c != 9 {
		t.Errorf("the vote lost its CPU: %v", c)
	}
	// A lane that keeps no tally (the shadow's run) still votes.
	if votes := m.cast(context.Background(), run, "precommit", "", want); votes["fleet:check-yaml"].State != 0 {
		t.Errorf("an untallied cast voted %+v", votes)
	}
}

// THE GATE'S RECORD CARRIES WHAT ITS CASTS TALLIED: gateStage hands the vector
// a tally, and the stage it settles states the sum.
func TestTheGatesRecordCarriesTheStageCPU(t *testing.T) {
	const vector = `[{"atom":"go:vet","stage":"precommit","lane":"go","state":0,"result":"pass","cpu_ms":4}]`
	m := gateOn(t, vector)
	gateVector = func(ctx context.Context, _ *FoundryTools, _, _ string) (string, error) {
		tallyOf(ctx).add(ms(20))
		tallyOf(ctx).add(ms(3))
		return vector, nil
	}
	w := readRecord(t, runGate(t, m, ""))
	if string(w.StageCPU) != "23" {
		t.Errorf("stage_cpu_ms %s, want 23", w.StageCPU)
	}
	if len(w.Atoms) != 1 || string(w.Atoms[0]["cpu_ms"]) != "4" {
		t.Errorf("the atom's cpu_ms did not ride: %v", w.Atoms)
	}
}
