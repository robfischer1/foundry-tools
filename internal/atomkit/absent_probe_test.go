package atomkit

import (
	"context"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

func anyDef(check func(context.Context, In) checks.Verdict) Def {
	return Def{ID: "st:any", Stage: "precommit", Lane: checks.LaneAny, Surface: "any:x/*.toml", Toolchain: "none", Check: check}
}

// TestDEF_B3_079_TheIndexCarriesTheAtomsOwnStandDownSentence: an any-lane atom
// says why it stands down in its own Check, and the index carries that sentence.
func TestDEF_B3_079_TheIndexCarriesTheAtomsOwnStandDownSentence(t *testing.T) {
	const own = "st:any: ABSENT - this tree carries no x/*.toml"
	generic := checks.AbsentReasonFor("st:any", checks.LaneAny, false)
	for name, c := range map[string]struct {
		check func(context.Context, In) checks.Verdict
		want  string
	}{
		"it stands down over nothing": {func(context.Context, In) checks.Verdict {
			return checks.Verdict{Result: "absent", Reason: own}
		}, own},
		"it passes over nothing": {passCheck, generic},
		"it cannot run over nothing": {func(context.Context, In) checks.Verdict {
			return checks.Verdict{Result: "cannot-run", State: 2, Reason: "x"}
		}, generic},
		"it asks for a program": {func(ctx context.Context, in In) checks.Verdict {
			if _, code := in.Exec(ctx, atoms.Cmd{}); code != 127 {
				t.Errorf("the probe ran a program: %d", code)
			}
			return checks.Verdict{Result: "absent", Reason: own}
		}, own},
		"it panics": {func(context.Context, In) checks.Verdict { panic("no tree") }, generic},
	} {
		if got := described2(t, anyDef(c.check))["absent_reason"]; got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
	// An atom that does not answer is not waited for.
	probeBudget = 20 * time.Millisecond
	t.Cleanup(func() { probeBudget = 10 * time.Second })
	hang := anyDef(func(ctx context.Context, _ In) checks.Verdict { <-ctx.Done(); return checks.Verdict{} })
	if got := described2(t, hang)["absent_reason"]; got != generic {
		t.Errorf("a hanging check: %q", got)
	}
	// A lane atom is stood down by its lane, never by asking it.
	asked := false
	d := goDef()
	d.Check = func(context.Context, In) checks.Verdict {
		asked = true
		return checks.Verdict{Result: "absent", Reason: own}
	}
	if got := described2(t, d)["absent_reason"]; got != checks.AbsentReasonFor(d.ID, d.Lane, false) || asked {
		t.Errorf("a lane atom was asked: %q", got)
	}
}
