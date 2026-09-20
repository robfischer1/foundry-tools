package checks

import (
	"encoding/json"
	"strings"
	"testing"
)

// atomFor is a minimal AtomDef for the verdict-shape tests.
func atomFor(id string) AtomDef { return AtomDef{ID: id, Stage: "precommit", Lane: LaneGo} }

// EVERY STATE CARRIES ITS LINES — including a PASS, which is the state whose
// output the old code threw away. Rob: "we don't trust absence as evidence."
func TestEveryStateCarriesItsLines(t *testing.T) {
	for _, c := range []struct {
		name   string
		exit   int
		output string
		want   string
	}{
		{"a pass with output keeps it", 0, "ok\tdagger/foundry-tools\t0.4s", "ok\tdagger/foundry-tools\t0.4s"},
		{"findings", 1, "main.go:12: undefined: x", "main.go:12: undefined: x"},
		{"cannot run", 2, "go: cannot find module", "go: cannot find module"},
	} {
		t.Run(c.name, func(t *testing.T) {
			v := VerdictOf(atomFor("go:vet"), c.exit, c.output)
			if len(v.Logs) == 0 {
				t.Fatalf("state %d carried no logs at all", v.State)
			}
			if got := strings.Join(v.Logs, "\n"); got != c.want {
				t.Fatalf("logs = %q, want %q", got, c.want)
			}
			if v.OriginalBytes != len(c.output) {
				t.Fatalf("originalBytes = %d, want %d", v.OriginalBytes, len(c.output))
			}
			if v.Truncated {
				t.Fatal("a small output must not claim truncation")
			}
		})
	}
}

// AN ABSENCE CARRIES AN EMPTY LIST, NOT NIL — and an atom that ran silently
// does too. Both are "printed nothing", which is not "field never set".
func TestSilenceAndAbsenceCarryEmptyNotNil(t *testing.T) {
	if v := VerdictOf(atomFor("go:vet"), 0, ""); v.Logs == nil {
		t.Fatal("an atom that printed nothing must carry [], not nil")
	}
	if v := AbsentVerdict(atomFor("rust:fmt")); v.Logs == nil {
		t.Fatal("an absent atom must carry [], not nil")
	}
}

// THE JSON TAG MUST NOT BE omitempty. An atom that printed nothing has to stay
// distinguishable on the wire from one nobody asked — null and [] are
// different facts, and F4/F5 read this shape.
func TestLogsSurviveJSONAsAnEmptyArray(t *testing.T) {
	v := VerdictOf(atomFor("go:vet"), 0, "")
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"logs":[]`) {
		t.Fatalf("an empty logs list must marshal as [], not vanish: %s", raw)
	}
	var back Verdict
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Logs == nil {
		t.Fatal("logs came back nil through a JSON round-trip")
	}
}

// REASON DOES NOT MOVE. This feature adds fields BESIDE the rendered reason and
// rewrites none of it — things downstream read it by shape today (spec FR-008).
func TestReasonIsUnchangedByTheCapture(t *testing.T) {
	for _, c := range []struct {
		exit int
		want string
	}{
		{0, "go:vet: PASS"},
		{1, "go:vet: FINDINGS (exit 1)\nmain.go:12: bad"},
		{2, "go:vet: CANNOT RUN (exit 2) — a check that could not run is not a check that passed.\nmain.go:12: bad"},
	} {
		v := VerdictOf(atomFor("go:vet"), c.exit, "main.go:12: bad")
		if c.exit == 0 {
			// reasonFor discards the output for a pass — that is exactly why
			// the capture cannot be a split of Reason.
			if v.Reason != "go:vet: PASS" {
				t.Fatalf("exit 0 reason = %q, want %q", v.Reason, "go:vet: PASS")
			}
			if len(v.Logs) == 0 {
				t.Fatal("...and yet the logs must still be there")
			}
			continue
		}
		if v.Reason != c.want {
			t.Fatalf("exit %d reason = %q, want %q", c.exit, v.Reason, c.want)
		}
	}
}

// A CANNOT-RUN VECTOR'S REASON IS ITS EVIDENCE — there was no run to produce
// output, so the explanation is all there is.
func TestCannotRunVectorCarriesItsReasonAsLines(t *testing.T) {
	vs := CannotRunVector("gate", "prepush", "the engine could not fetch the tree")
	if len(vs) != 1 {
		t.Fatalf("want one verdict, got %d", len(vs))
	}
	if len(vs[0].Logs) == 0 || !strings.Contains(strings.Join(vs[0].Logs, "\n"), "could not fetch") {
		t.Fatalf("a cannot-run vector must carry its reason as lines: %+v", vs[0].Logs)
	}
}

// THE CAP IS PER ATOM, AND THE CUT IS STATED ALL THE WAY THROUGH THE VERDICT.
func TestAnOversizeAtomStatesItsCutOnTheVerdict(t *testing.T) {
	big := strings.Repeat("x\n", AtomLogCap)
	v := VerdictOf(atomFor("rust:test"), 1, big)
	if !v.Truncated {
		t.Fatal("an atom over the cap must say it was truncated")
	}
	if v.OriginalBytes != len(big) {
		t.Fatalf("originalBytes = %d, want the TRUE size %d", v.OriginalBytes, len(big))
	}
	if n := len(strings.Join(v.Logs, "\n")); n > AtomLogCap {
		t.Fatalf("carried %d bytes, cap is %d", n, AtomLogCap)
	}
}

// NO ATOM CARRIES ANOTHER ATOM'S LINES (spec FR-010).
func TestAtomsDoNotShareABuffer(t *testing.T) {
	a := VerdictOf(atomFor("go:vet"), 1, "AAA")
	b := VerdictOf(atomFor("go:test"), 1, "BBB")
	if strings.Contains(strings.Join(a.Logs, "\n"), "BBB") || strings.Contains(strings.Join(b.Logs, "\n"), "AAA") {
		t.Fatalf("atoms shared a buffer: a=%q b=%q", a.Logs, b.Logs)
	}
}

// THE LINES SURVIVE THE COPY INTO THE STAGE. A field that stops at the first
// copy is a field the run record never sees.
func TestTheStageCopyCarriesTheLines(t *testing.T) {
	v := VerdictOf(atomFor("go:vet"), 1, "main.go:12: bad")
	a := StageAtom{Atom: v.Atom, Group: GroupLanguage, State: v.State, Result: v.Result, Reason: v.Reason,
		Logs: v.Logs, Truncated: v.Truncated, OriginalBytes: v.OriginalBytes}
	if len(a.Logs) == 0 || a.OriginalBytes != v.OriginalBytes {
		t.Fatalf("the stage copy dropped the evidence: %+v", a)
	}
}
