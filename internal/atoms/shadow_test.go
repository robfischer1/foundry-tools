package atoms

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

func verdict(id string, state int, out string) checks.Verdict {
	return checks.VerdictOf(checks.AtomByID(id), state, out)
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
	exactly := strings.Repeat("y", 300)
	if got := clip(exactly); got != exactly {
		t.Errorf("a reason of exactly 300 bytes is not cut: %d", len(got))
	}
	if got := clip("a\nb"); got != "a | b" {
		t.Errorf("clip(a\\nb) = %q", got)
	}
}
