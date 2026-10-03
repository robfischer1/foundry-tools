package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// tickingClock answers each read one second after the last, from a fixed UTC
// start, and restores the wall clock when the test ends.
func tickingClock(t *testing.T) {
	t.Helper()
	now := time.Date(2026, 10, 3, 6, 0, 0, 0, time.UTC)
	atomClock = func() time.Time {
		now = now.Add(time.Second)
		return now
	}
	t.Cleanup(func() { atomClock = time.Now })
}

// AN ATOM THAT RAN IS STAMPED WITH WHEN ITS RUNNER WAS ENTERED AND WHEN IT
// ANSWERED — read from atomClock, before and after, once each.
func TestVerdictForStampsWhenTheAtomRan(t *testing.T) {
	tickingClock(t)
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"go","vet"`, 1)
	v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "go:vet")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != 1 {
		t.Fatalf("the timing must not change the answer: %+v", v)
	}
	if v.StartedAt != "2026-10-03T06:00:01.000Z" || v.FinishedAt != "2026-10-03T06:00:02.000Z" {
		t.Fatalf("want the first read as the start and the second as the finish, got %q .. %q", v.StartedAt, v.FinishedAt)
	}
}

// A RE-ASKED ATOM'S TIME SPANS BOTH ASKS: it started when the first did, and
// finished when the answer it reports came back — two clock reads, not four.
func TestAReaskedAtomIsTimedAcrossBothAsks(t *testing.T) {
	tickingClock(t)
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"go","vet"`, 2)
	v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "go:vet")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != 2 || !strings.Contains(v.Reason, "asked twice") {
		t.Fatalf("still a twice-asked could-not-run: %+v", v)
	}
	if v.StartedAt != "2026-10-03T06:00:01.000Z" || v.FinishedAt != "2026-10-03T06:00:02.000Z" {
		t.Fatalf("got %q .. %q", v.StartedAt, v.FinishedAt)
	}
}

// AN ATOM WITH NO RUNNER IS AN AUTHORING ERROR, answered before any clock is
// read: there is no atom to time.
func TestAnUnregisteredAtomIsNeverTimed(t *testing.T) {
	reads := 0
	atomClock = func() time.Time { reads++; return time.Now() }
	t.Cleanup(func() { atomClock = time.Now })
	if _, err := verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "nope:never"); err == nil {
		t.Fatal("an unregistered atom must be an error")
	}
	if reads != 0 {
		t.Fatalf("the clock was read %d times for an atom that does not exist", reads)
	}
}

// THE RECORD CARRIES started_at AND finished_at ON EVERY ATOM THAT RAN, under
// exactly those names (Daedalus's ci_atom columns read them), and NEITHER on an
// atom that never started — the generated marshaller would write "" there,
// which is why Record writes through recordAtom.
func TestTheRecordCarriesEachAtomsTimes(t *testing.T) {
	st := checks.Stage{
		Name: "check", State: 1, Lanes: []string{"go"},
		Ran: []checks.StageAtom{{Atom: "go:vet", Group: "language", State: 1, Result: "findings", Logs: []string{},
			StartedAt: "2026-10-03T06:00:01.000Z", FinishedAt: "2026-10-03T06:00:02.500Z"}},
		Omitted: []checks.StageAtom{{Atom: "rust:fmt", Group: "language", Result: "absent", Logs: []string{}}},
	}
	rec, err := stageResult(st).Record()
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Atoms, Omitted []map[string]any
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(rec, runRecordSentinel+" ")), &wire); err != nil {
		t.Fatal(err)
	}
	ran := wire.Atoms[0]
	if ran["started_at"] != "2026-10-03T06:00:01.000Z" || ran["finished_at"] != "2026-10-03T06:00:02.500Z" {
		t.Fatalf("the ran atom's times are not on the wire: %v", ran)
	}
	if ran["Atom"] != "go:vet" || ran["Result"] != "findings" {
		t.Fatalf("the existing wire names moved: %v", ran)
	}
	if _, ok := wire.Omitted[0]["started_at"]; ok {
		t.Fatalf("an atom that never started carried a start: %v", wire.Omitted[0])
	}
	if _, ok := wire.Omitted[0]["finished_at"]; ok {
		t.Fatalf("an atom that never started carried a finish: %v", wire.Omitted[0])
	}
}

// THE RECORD'S ABSENT LISTS STAY null AND ITS EMPTY ONES STAY [], as they were
// when the generated marshaller wrote them: rewriting the atoms must not
// change what the rest of the record says.
func TestTheRecordKeepsNullAndEmptyAtomListsApart(t *testing.T) {
	rec, err := (&StageResult{Stage: "check", Omitted: []AtomResult{}}).Record()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec, `"Atoms":null`) || !strings.Contains(rec, `"Omitted":[]`) {
		t.Fatalf("null and [] must survive the rewrite: %s", rec)
	}
}
