package basesblade

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/bladefixed"
	"dagger/foundry-tools/internal/checks"
)

func TestTheDeclarationValidates(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StageBases || Def.Toolchain != "none" || Def.Group != checks.GroupOf(id) || Def.Timeout != 15*time.Minute {
		t.Errorf("%+v", Def)
	}
}

func fake(t *testing.T, res bladefixed.Result, err error) *string {
	t.Helper()
	old := validate
	var seen string
	validate = func(img string) (bladefixed.Result, error) {
		seen = img
		return res, err
	}
	t.Cleanup(func() { validate = old })
	return &seen
}

// The atom warns and does not refuse: problems pass with the list in the reason.
func TestTheVerdictWarnsAndPasses(t *testing.T) {
	t.Setenv(ImageEnv, "")
	seen := fake(t, bladefixed.Result{Checked: []string{"blade-base", "blade-differ"}, Checks: 30}, nil)
	atomtest.Expect(t, check(context.Background(), atomkit.In{}), checks.StatePass, atomtest.Pass, "PASS - 30 checks over blade-base, blade-differ")
	if *seen != "/work/image" {
		t.Errorf("read %s", *seen)
	}

	t.Setenv(ImageEnv, "/elsewhere")
	seen = fake(t, bladefixed.Result{Checked: []string{"blade-base"}, Problems: []string{"blade-base: /opt/blade is 0:0 0755", "second"}}, nil)
	atomtest.Expect(t, check(context.Background(), atomkit.In{}), checks.StatePass, atomtest.Pass, "WARN (not enforced yet) - 2 problem(s)", "/opt/blade is 0:0 0755")
	if *seen != "/elsewhere" {
		t.Errorf("read %s", *seen)
	}

	var many []string
	for i := 0; i < 100; i++ {
		many = append(many, fmt.Sprintf("problem %03d", i))
	}
	fake(t, bladefixed.Result{Checked: []string{"blade-base"}, Problems: many}, nil)
	v := check(context.Background(), atomkit.In{})
	atomtest.Expect(t, v, checks.StatePass, atomtest.Pass, "100 problem(s)", "problem 059", "... and 40 more")
	if strings.Contains(v.Reason+strings.Join(v.Logs, "\n"), "problem 060") {
		t.Error("the list was not cut")
	}
}

func TestNothingLaidIsAbsentAndAnUnreadableLayoutIsUnchecked(t *testing.T) {
	t.Setenv(ImageEnv, "/img")
	fake(t, bladefixed.Result{}, nil)
	atomtest.Expect(t, check(context.Background(), atomkit.In{}), checks.StatePass, atomtest.Absent, "ABSENT", "/img")
	fake(t, bladefixed.Result{}, errors.New("the layout of blade-base could not be read: gone"))
	atomtest.Expect(t, check(context.Background(), atomkit.In{}), checks.StatePass, atomtest.Pass, "went UNCHECKED, not clean", "could not be read")
}
