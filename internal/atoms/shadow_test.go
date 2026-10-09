package atoms

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"dagger/foundry-tools/internal/checks"
)

func verdict(id string, state int, out string) checks.Verdict {
	return checks.VerdictOf(checks.AtomByID(id), state, out)
}

func TestRenderCarriesTheShadowsWallTime(t *testing.T) {
	if got := (Report{Elapsed: 2500 * time.Millisecond}).Render(); !strings.Contains(got, "missing from the chains, took 2.5s\n") {
		t.Errorf("report %q", got)
	}
	if got := (Report{}).Render(); strings.Contains(got, "took") {
		t.Errorf("an unmeasured report printed a time: %q", got)
	}
}

func TestCompare(t *testing.T) {
	const a, b, c = "fleet:check-yaml", "fleet:check-added-large-files", "fleet:check-merge-conflict"
	today := []checks.Verdict{verdict(a, 0, ""), verdict(b, 1, "files over 2048 KB:\nx"), verdict(c, 0, "fleet:check-merge-conflict: no conflict markers")}
	// Timing is the one thing two runs never share.
	today[0].StartedAt, today[0].FinishedAt = "2026-10-08T00:00:00.000Z", "2026-10-08T00:00:01.000Z"
	shadow := []checks.Verdict{verdict(a, 0, ""), verdict(b, 1, "files over 2048 KB:\nx"), verdict(c, 0, "fleet:check-merge-conflict: no conflict markers")}
	shadow[0].StartedAt, shadow[0].FinishedAt = "2026-10-08T05:00:00.000Z", "2026-10-08T05:00:09.000Z"

	rep := Compare(today, shadow)
	if len(rep.Agree) != 3 || len(rep.Differ) != 0 || len(rep.MissingShadow) != 0 || len(rep.MissingToday) != 0 {
		t.Fatalf("identical vectors, different clocks: %+v", rep)
	}
	if rep.StatesAgree() != 3 {
		t.Errorf("states agree %d, want 3", rep.StatesAgree())
	}
}

func TestCompareNamesTheFieldsThatDiffer(t *testing.T) {
	const id = "fleet:check-yaml"
	base := func() checks.Verdict { return verdict(id, 1, "bad.yml: broken") }
	for _, tc := range []struct {
		name      string
		mutate    func(v *checks.Verdict)
		wantField string
		wantState bool
	}{
		{"state", func(v *checks.Verdict) { v.State = 2; v.Result = "cannot-run" }, "state", true},
		{"result", func(v *checks.Verdict) { v.Result = "absent" }, "result", false},
		{"reason", func(v *checks.Verdict) { v.Reason = "other" }, "reason", false},
		{"logs", func(v *checks.Verdict) { v.Logs = []string{"other"} }, "logs", false},
		{"stage", func(v *checks.Verdict) { v.Stage = "prepush" }, "stage", false},
		{"lane", func(v *checks.Verdict) { v.Lane = "go" }, "lane", false},
		{"truncated", func(v *checks.Verdict) { v.Truncated = true }, "truncated", false},
		{"original_bytes", func(v *checks.Verdict) { v.OriginalBytes++ }, "original_bytes", false},
		{"findings", func(v *checks.Verdict) { v.Findings = []checks.Finding{{}} }, "findings", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := base()
			tc.mutate(&s)
			rep := Compare([]checks.Verdict{base()}, []checks.Verdict{s})
			if len(rep.Differ) != 1 || len(rep.Agree) != 0 {
				t.Fatalf("report %+v", rep)
			}
			d := rep.Differ[0]
			if !strings.Contains(strings.Join(d.Fields, ","), tc.wantField) || d.StateDiffers != tc.wantState {
				t.Errorf("fields %q stateDiffers=%v, want %q / %v", d.Fields, d.StateDiffers, tc.wantField, tc.wantState)
			}
		})
	}
}

func TestCompareMissingOnEitherSide(t *testing.T) {
	rep := Compare(
		[]checks.Verdict{verdict("fleet:check-yaml", 0, ""), verdict("fleet:check-merge-conflict", 0, "")},
		[]checks.Verdict{verdict("fleet:check-yaml", 0, ""), verdict("fleet:stop-justifications", 0, "")},
	)
	if len(rep.Agree) != 1 || len(rep.MissingShadow) != 1 || rep.MissingShadow[0] != "fleet:check-merge-conflict" ||
		len(rep.MissingToday) != 1 || rep.MissingToday[0] != "fleet:stop-justifications" {
		t.Errorf("report %+v", rep)
	}
}

func TestRender(t *testing.T) {
	const a, b = "fleet:check-yaml", "fleet:check-added-large-files"
	rep := Compare(
		[]checks.Verdict{verdict(a, 1, "chain says\nso"), verdict(b, 0, "same"), verdict("fleet:check-merge-conflict", 0, "x")},
		[]checks.Verdict{verdict(a, 2, "binary says"), verdict(b, 0, "same"), verdict("fleet:stop-justifications", 0, "y")},
	)
	got := rep.Render()
	for _, want := range []string{
		"shadow atoms: 2 compared, 1 identical, 0 same state, 1 state differs, 1 missing from the binary, 1 missing from the chains\n",
		"fleet:check-yaml: STATE DIFFERS (state, result, reason, logs",
		"chain  state=1 fleet:check-yaml: FINDINGS (exit 1) | chain says | so",
		"binary state=2 fleet:check-yaml: CANNOT RUN",
		"fleet:check-merge-conflict: the chain answered and the binary did not",
		"fleet:stop-justifications: the binary answered and the chain did not",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, b+":") {
		t.Errorf("an atom that agreed is not listed:\n%s", got)
	}
}

func TestRenderCallsATextOnlyDifferenceByThatName(t *testing.T) {
	const id = "fleet:check-yaml"
	got := Compare([]checks.Verdict{verdict(id, 1, "pre-commit's wording")}, []checks.Verdict{verdict(id, 1, "yaml.v3's wording")}).Render()
	if !strings.Contains(got, "0 identical, 1 same state, 0 state differs") || !strings.Contains(got, id+": text differs") || strings.Contains(got, "STATE DIFFERS") {
		t.Errorf("report:\n%s", got)
	}
}

func TestClipBoundsAReasonAndFlattensItsLines(t *testing.T) {
	long := strings.Repeat("x", 400)
	if got := clip(long); len(got) != 303 || !strings.HasSuffix(got, "...") {
		t.Errorf("clip of 400 bytes is %d long", len(got))
	}
	// 299 ASCII bytes then a 3-byte rune straddling the cut at 300.
	multi := strings.Repeat("z", 299) + "\u20ac" + "tail"
	if got := clip(multi); !utf8.ValidString(got) || got != strings.Repeat("z", 299)+"..." {
		t.Errorf("clip cut through a rune: %q", got[len(got)-8:])
	}
	// Nothing but continuation bytes: the walk back stops at 0, it does not
	// index below it.
	if got := clip(strings.Repeat("\x80", 400)); got != "..." {
		t.Errorf("clip of 400 continuation bytes is %q", got)
	}
	exactly := strings.Repeat("y", 300)
	if got := clip(exactly); got != exactly {
		t.Errorf("a reason of exactly 300 bytes is not cut: %d", len(got))
	}
	if got := clip("a\nb"); got != "a | b" {
		t.Errorf("clip(a\\nb) = %q", got)
	}
}

func witnessTable(rows ...string) string {
	s := "narcissus — fleet:witness: clean — 2 file(s) witnessed, novel on both axes; skipped 1 test file(s)\n\n| file | verdict | class | reason |\n|---|---|---|---|\n"
	for _, r := range rows {
		s += "| " + r + " | novel | clean | novel on both axes |\n"
	}
	return s
}

func dryWitnessVerdict(skippedTests int, paths ...string) checks.Verdict {
	tests := make([]string, skippedTests)
	return verdict("fleet:witness", 2, checks.WitnessDryText(paths, "ci:gate:x@HEAD", nil, nil, tests))
}

func TestCompareHoldsADryWitnessApartFromTheCounts(t *testing.T) {
	const y = "fleet:check-yaml"
	chain := verdict("fleet:witness", 0, witnessTable("a.go", "b.py"))
	t.Run("agreement is one line and no count", func(t *testing.T) {
		rep := Compare([]checks.Verdict{verdict(y, 0, ""), chain}, []checks.Verdict{verdict(y, 0, ""), dryWitnessVerdict(1, "a.go", "b.py")})
		if len(rep.Agree) != 1 || len(rep.Differ) != 0 || len(rep.MissingShadow)+len(rep.MissingToday) != 0 || rep.Dry == nil {
			t.Fatalf("%+v", rep)
		}
		want := "fleet:witness (dry): would ask 2, chain asked 2; paths agree | skipped/vendored/tests agree\n"
		if out := rep.Render(); !strings.Contains(out, want) || !strings.Contains(out, "1 compared, 1 identical") {
			t.Errorf("render:\n%s", out)
		}
	})
	t.Run("a path the binary would not ask is flagged", func(t *testing.T) {
		rep := Compare([]checks.Verdict{chain}, []checks.Verdict{dryWitnessVerdict(1, "a.go", "c.go")})
		if got := rep.Render(); !strings.Contains(got, "paths differ: +1 -1 (+ c.go) (- b.py)") || strings.Contains(got, "paths agree") {
			t.Errorf("render:\n%s", got)
		}
	})
	t.Run("counts that differ are named", func(t *testing.T) {
		rep := Compare([]checks.Verdict{chain}, []checks.Verdict{dryWitnessVerdict(3, "a.go", "b.py")})
		if got := rep.Render(); !strings.Contains(got, "paths agree | skipped/vendored/tests differ") {
			t.Errorf("render:\n%s", got)
		}
	})
	t.Run("a chain that found something carries no counts to compare", func(t *testing.T) {
		found := verdict("fleet:witness", 1, "findings in 1 of 2 file(s): a.go: dup\n\n| file | verdict | class | reason |\n|---|---|---|---|\n| a.go | standard | finding | dup |\n| b.py | novel | clean | x |\n")
		rep := Compare([]checks.Verdict{found}, []checks.Verdict{dryWitnessVerdict(0, "a.go", "b.py")})
		if got := rep.Render(); !strings.Contains(got, "paths agree | skipped/vendored/tests not comparable") {
			t.Errorf("render:\n%s", got)
		}
	})
	t.Run("a truncated table claims no agreement", func(t *testing.T) {
		cut := chain
		cut.Truncated = true
		if got := Compare([]checks.Verdict{cut}, []checks.Verdict{dryWitnessVerdict(1, "a.go", "b.py")}).Render(); !strings.Contains(got, "not comparable (the chain's table was truncated)") {
			t.Errorf("render:\n%s", got)
		}
	})
	t.Run("a dry witness the chain did not run is the binary answering alone", func(t *testing.T) {
		rep := Compare([]checks.Verdict{verdict(y, 0, "")}, []checks.Verdict{verdict(y, 0, ""), dryWitnessVerdict(0, "a.go")})
		if rep.Dry != nil || len(rep.MissingToday) != 1 {
			t.Errorf("%+v", rep)
		}
	})
}

func TestDryWitnessIsRecognisedByAtomAndMarkTogether(t *testing.T) {
	dryText := checks.WitnessDryText([]string{"a.go"}, "c", nil, nil, nil)
	if !isDryWitness(checks.Verdict{Atom: "fleet:witness", Reason: dryText}) {
		t.Error("a dry witness with its words in the reason alone (no logs) was not recognised")
	}
	if got := witnessText(checks.Verdict{Reason: "only the reason"}); got != "only the reason" {
		t.Errorf("text %q", got)
	}
	if isDryWitness(checks.Verdict{Atom: "fleet:check-yaml", Reason: dryText, Logs: []string{dryText}}) {
		t.Error("another atom that quotes the mark was taken for the dry witness")
	}
	if isDryWitness(checks.Verdict{Atom: "fleet:witness", Reason: "clean"}) {
		t.Error("a witness that asked was taken for a dry one")
	}
	rep := Compare([]checks.Verdict{verdict("fleet:check-yaml", 0, "")}, []checks.Verdict{verdict("fleet:check-yaml", 0, dryText)})
	if rep.Dry != nil {
		t.Errorf("a quoted mark made a dry comparison: %+v", rep.Dry)
	}
}
