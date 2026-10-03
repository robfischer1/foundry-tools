package checks

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// AN ATOM'S TIMES ARE RFC 3339 UTC TO THE MILLISECOND, whatever zone the clock
// that read them carried: Daedalus's columns read the string as written.
func TestTimedWritesUTCToTheMillisecond(t *testing.T) {
	est := time.FixedZone("EST", -5*3600)
	started := time.Date(2026, 10, 3, 1, 2, 3, 456_789_000, est)
	finished := started.Add(1500 * time.Millisecond)
	v := Timed(Verdict{Atom: "go:vet", State: 1, Result: "findings"}, started, finished)
	if v.StartedAt != "2026-10-03T06:02:03.456Z" || v.FinishedAt != "2026-10-03T06:02:04.956Z" {
		t.Fatalf("want UTC millisecond stamps, got %q .. %q", v.StartedAt, v.FinishedAt)
	}
	if v.Atom != "go:vet" || v.State != 1 || v.Result != "findings" {
		t.Fatalf("stamping a verdict must not change its answer: %+v", v)
	}
	for _, s := range []string{v.StartedAt, v.FinishedAt} {
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			t.Fatalf("%q is not RFC 3339: %v", s, err)
		}
	}
}

// THE WIRE NAMES ARE started_at AND finished_at, AND AN ATOM THAT NEVER
// STARTED CARRIES NEITHER — not an empty string a reader would have to parse.
func TestAVerdictsTimesAreOmittedUntilStamped(t *testing.T) {
	raw, err := json.Marshal(Verdict{Atom: "rust:fmt", Result: "absent", Logs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "started_at") || strings.Contains(string(raw), "finished_at") {
		t.Fatalf("an unstarted atom carried a time: %s", raw)
	}
	raw, err = json.Marshal(Timed(Verdict{Atom: "go:vet"}, time.Unix(0, 0), time.Unix(1, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"started_at":"1970-01-01T00:00:00.000Z"`) ||
		!strings.Contains(string(raw), `"finished_at":"1970-01-01T00:00:01.000Z"`) {
		t.Fatalf("the stamped times are missing from the wire: %s", raw)
	}
}

// THE TIMES CROSS THE STAGE'S COPY, for ran and omitted atoms alike — the copy
// is one of the two hops a field can silently stop at.
func TestSettleStageCarriesEachAtomsTimes(t *testing.T) {
	st := SettleStage("check", []Verdict{
		{Atom: "go:vet", State: 0, Result: "pass", StartedAt: "a", FinishedAt: "b"},
		{Atom: "rust:fmt", State: 0, Result: "absent"},
	})
	if len(st.Ran) != 1 || st.Ran[0].StartedAt != "a" || st.Ran[0].FinishedAt != "b" {
		t.Fatalf("a ran atom's times stopped at the copy: %+v", st.Ran)
	}
	if len(st.Omitted) != 1 || st.Omitted[0].StartedAt != "" || st.Omitted[0].FinishedAt != "" {
		t.Fatalf("an omitted atom invented times: %+v", st.Omitted)
	}
}
