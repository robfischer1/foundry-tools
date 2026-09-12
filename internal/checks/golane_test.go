package checks

import (
	"strings"
	"testing"
)

// The verdict the mutation lane answers with is the score phase's, and the two
// ways it can be missing are the two ways a mutation gate goes quietly green.
//
// ONE TABLE FOR FOUR LANES. Each lane port landed its own reader of these two
// files — MutationOutcome (python) and MutationScore (rust/ts) — because three
// branches could not declare one exported name without colliding at the merge.
// This is the survivor and this is the table; the cases below the blank line
// came off those two.
func TestMutationVerdictReadsWhatTheScorePhaseWrote(t *testing.T) {
	cases := []struct {
		name       string
		verdict    string
		reason     string
		wantState  int
		wantReason string
		wantErr    string
	}{
		{
			name:       "clean",
			verdict:    "0\n",
			reason:     "12 mutant(s), 12 killed\n",
			wantState:  0,
			wantReason: "12 mutant(s), 12 killed",
		},
		{
			name:       "survivors are findings",
			verdict:    "1",
			reason:     "14 mutant(s), 1 survived",
			wantState:  1,
			wantReason: "14 mutant(s), 1 survived",
		},
		{
			name:       "the script's own could-not-measure",
			verdict:    "2\n",
			reason:     "no usable PR base sha\n",
			wantState:  2,
			wantReason: "no usable PR base sha",
		},
		{
			name:    "a missing verdict file is not a pass",
			verdict: "",
			reason:  "12 mutant(s), 12 killed",
			wantErr: "wrote no verdict",
		},
		{
			name:    "whitespace is not a verdict either",
			verdict: "  \n ",
			reason:  "",
			wantErr: "wrote no verdict",
		},
		{
			name:    "a verdict that is not a number is refused, not truncated",
			verdict: "killed\n",
			reason:  "",
			wantErr: "not a verdict",
		},
		{
			name:       "a bare verdict still says it arrived bare",
			verdict:    "0",
			reason:     "   \n",
			wantState:  0,
			wantReason: "the score phase wrote verdict 0 and no reason",
		},
		{
			// StateFor maps anything past 1 to cannot-run, so nothing here
			// clamps: a script that one day writes 3 must not become a pass.
			name:       "an unknown verdict is passed through raw",
			verdict:    "3",
			reason:     "gremlins crashed",
			wantState:  3,
			wantReason: "gremlins crashed",
		},

		{
			name:       "padding either side is still a verdict",
			verdict:    " 2 \n",
			reason:     "cosmic-ray could not run",
			wantState:  2,
			wantReason: "cosmic-ray could not run",
		},
		{
			name:    "two numbers are not one verdict",
			verdict: "0 1",
			reason:  "whatever",
			wantErr: "not a verdict",
		},
		{
			name:    "a newline alone is not a verdict",
			verdict: "\n",
			reason:  "whatever",
			wantErr: "wrote no verdict",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state, reason, err := MutationVerdict(c.verdict, c.reason)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("MutationVerdict(%q, %q) = (%d, %q, nil); want an error naming %q",
						c.verdict, c.reason, state, reason, c.wantErr)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("error %q does not name %q", err, c.wantErr)
				}
				if State(state) == StatePass {
					t.Fatalf("a refused verdict answered state %d — a mutation gate that did not measure is never a pass", state)
				}
				return
			}
			if err != nil {
				t.Fatalf("MutationVerdict(%q, %q) errored: %v", c.verdict, c.reason, err)
			}
			if state != c.wantState {
				t.Errorf("state = %d, want %d", state, c.wantState)
			}
			if reason != c.wantReason {
				t.Errorf("reason = %q, want %q", reason, c.wantReason)
			}
		})
	}
}

// The budget decides which of two spellings of ONE gofmt run the atom uses, so
// what matters is that a realistic population takes the argument path and an
// unrealistic one still has somewhere to go.
func TestNeedsArgFileOnlyForAListNoArgvWouldCarry(t *testing.T) {
	if NeedsArgFile(nil) {
		t.Error("an empty population needs no arg file")
	}

	modest := make([]string, 0, 2000)
	for i := 0; i < 2000; i++ {
		modest = append(modest, "internal/checks/some/deep/package/file.go")
	}
	if NeedsArgFile(modest) {
		t.Errorf("%d files of %d bytes fit in an argv and must go as arguments",
			len(modest), len(modest[0]))
	}

	huge := make([]string, 0, 40000)
	for i := 0; i < 40000; i++ {
		huge = append(huge, "internal/checks/some/deep/package/file.go")
	}
	if !NeedsArgFile(huge) {
		t.Errorf("%d files is past the argv budget and must go through a file", len(huge))
	}

	// One path longer than the whole budget is enough on its own.
	if !NeedsArgFile([]string{strings.Repeat("a", argvBudget+1)}) {
		t.Error("a single path past the budget must go through a file")
	}
}

func TestGovulncheckExitReadsThreeAsAFinding(t *testing.T) {
	for code, want := range map[int]int{0: 0, 1: 1, 2: 2, 3: 1, 137: 137} {
		if got := GovulncheckExit(code); got != want {
			t.Errorf("GovulncheckExit(%d) = %d, want %d", code, got, want)
		}
	}
}
