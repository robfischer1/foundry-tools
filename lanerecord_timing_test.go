package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// A PHASE IS TIMED FROM WHEN THE PREVIOUS ONE SEALED (or the lane began) TO
// WHEN IT SEALED, read from atomClock. Build, cast and any lane built on
// phases settled with no started_at/finished_at at all (measured in
// erebus.ci_atom, 24 h to 2026-10-15: build 3,308 rows and cast 108, none
// timed), because seal built its AtomResult without them.
func TestAPhaseIsTimedFromThePreviousSealToItsOwn(t *testing.T) {
	tickingClock(t)
	p := newPhases("build", []string{"a", "b"})
	p.seal("a", 0, "")
	p.seal("b", 1, "")

	a, b := p.atoms[0], p.atoms[1]
	if a.StartedAt != "2026-10-03T06:00:01.000Z" || a.FinishedAt != "2026-10-03T06:00:02.000Z" {
		t.Fatalf("first phase: want the lane's start .. its seal, got %q .. %q", a.StartedAt, a.FinishedAt)
	}
	if b.StartedAt != a.FinishedAt || b.FinishedAt != "2026-10-03T06:00:03.000Z" {
		t.Fatalf("second phase starts where the first ended: got %q .. %q", b.StartedAt, b.FinishedAt)
	}
}

// A PHASES VALUE BUILT AS A LITERAL (the tests' form, and any future lane's
// mistake) STILL TIMES ITS PHASES: with no mark the first phase starts when it
// seals, a zero span rather than a missing time.
func TestAPhaseWithNoMarkIsStillTimed(t *testing.T) {
	tickingClock(t)
	p := phases{group: "g"}
	p.seal("g:one", 0, "")
	a := p.atoms[0]
	if a.StartedAt == "" || a.FinishedAt == "" {
		t.Fatalf("an unmarked phase carried no time: %+v", a)
	}
	if a.StartedAt != a.FinishedAt {
		t.Fatalf("an unmarked first phase is a zero span, got %q .. %q", a.StartedAt, a.FinishedAt)
	}
}

// EVERY ATOM A REAL BUILD AND A REAL CAST SEAL CARRIES BOTH TIMES, in order,
// and they reach the wire under the names Daedalus's ci_atom columns read.
func TestEveryBuildAndCastAtomIsTimed(t *testing.T) {
	tickingClock(t)
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	bl, _, _ := laneFor(t, m, false)
	assertTimed(t, bl.record("build", 0))

	engine.reset()
	cm := castOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	cl, _, _ := castLaneFor(t, cm, false)
	assertTimed(t, cl.record("cast", 0))
}

func assertTimed(t *testing.T, s *StageResult) {
	t.Helper()
	if len(s.Atoms) == 0 {
		t.Fatalf("%s sealed no atoms", s.Stage)
	}
	var prevEnd time.Time
	for _, a := range s.Atoms {
		start, err1 := time.Parse(checks.TimeLayout, a.StartedAt)
		end, err2 := time.Parse(checks.TimeLayout, a.FinishedAt)
		if err1 != nil || err2 != nil {
			t.Fatalf("%s %s: times do not parse: %q .. %q", s.Stage, a.Atom, a.StartedAt, a.FinishedAt)
		}
		if end.Before(start) || start.Before(prevEnd) {
			t.Fatalf("%s %s: out of order: %v .. %v after %v", s.Stage, a.Atom, start, end, prevEnd)
		}
		prevEnd = end
	}
	rec, err := s.Record()
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(rec, `"started_at"`); n != len(s.Atoms) {
		t.Fatalf("%s: %d atoms but %d started_at on the wire", s.Stage, len(s.Atoms), n)
	}
}

// EVERY ATOM THAT RAN, ON ANY PATH THAT REACHES THE RECORD, CARRIES BOTH TIMES.
// This renders the record the door reads and checks the wire, so it holds for
// a path no regex over the source would find (a `var a AtomResult`, a
// make([]AtomResult, n), a checks.Verdict literal): the cannot-run vector was
// exactly that, and settled untimed on the gate and orbit lanes.
func assertRecordTimed(t *testing.T, name string, s *StageResult) {
	t.Helper()
	rec, err := s.Record()
	if err != nil {
		t.Fatal(err)
	}
	var wire struct{ Atoms, Omitted []map[string]any }
	if err := json.Unmarshal([]byte(strings.TrimPrefix(rec, runRecordSentinel+" ")), &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Atoms)+len(wire.Omitted) == 0 {
		t.Fatalf("%s: the record carries no atoms", name)
	}
	for _, a := range append(wire.Atoms, wire.Omitted...) {
		if a["Result"] == "absent" {
			continue
		}
		for _, k := range []string{"started_at", "finished_at"} {
			if v, _ := a[k].(string); v == "" {
				t.Errorf("%s: atom %v settled with no %s: %v", name, a["Atom"], k, a)
			}
		}
	}
}

// A GATE THAT COULD NOT GRADE (no repo, a tree the engine could not fetch, a
// mismatched tree, a rider's panic) STILL SAYS WHEN IT TRIED.
func TestCannotRunVectorsAreTimedOnTheRecord(t *testing.T) {
	tickingClock(t)
	m := &FoundryTools{}
	for _, lane := range []string{"gate", "orbit", "check"} {
		vector, state := m.gradeTree(t.Context(), lane, "tree", "", "")
		if state != int(checks.StateCannotRun) {
			t.Fatalf("%s: want cannot-run, got %d", lane, state)
		}
		assertRecordTimed(t, lane+" cannot-run", stageResult(checks.SettleStage(lane, vector)))
	}
	at := atomClock()
	assertRecordTimed(t, "rider panic",
		stageResult(checks.SettleStage("orbit", checks.CannotRunVector("orbit", "prepush", "panicked", at, at))))
}

// A GRADED VECTOR'S ATOMS ARE TIMED, the ones that ran; absent ones carry none.
func TestAGradedVectorIsTimedOnTheRecord(t *testing.T) {
	tickingClock(t)
	ran := checks.Timed(checks.VerdictOf(checks.AtomDef{ID: "go:vet"}, 0, "ok"), atomClock(), atomClock())
	absent := checks.AbsentVerdict(checks.AtomDef{ID: "rust:fmt"})
	assertRecordTimed(t, "graded", stageResult(checks.SettleStage("gate", []checks.Verdict{ran, absent})))
}
