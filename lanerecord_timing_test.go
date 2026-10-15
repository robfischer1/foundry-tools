package main

import (
	"os"
	"path/filepath"
	"regexp"
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

// A NEW LANE OR ATOM PATH CANNOT SKIP TIMING SILENTLY. An AtomResult is built
// in exactly two places: phases.seal (the procedural lanes, timed there) and
// stageResult (the gate's atoms, which arrive timed by checks.Timed). A third
// literal is a path nobody stamps, so it fails here until it is routed through
// one of the two or added to this list with its reason.
func TestAtomResultsAreBuiltOnlyWhereTheyAreTimed(t *testing.T) {
	allowed := map[string]bool{"lanerecord.go": true, "stage.go": true}
	lit := regexp.MustCompile(`\bAtomResult\{`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "dagger.gen.go" || allowed[f] {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if lit.Match(src) {
			t.Errorf("%s builds an AtomResult outside phases.seal / stageResult: it would settle untimed", f)
		}
	}
}
