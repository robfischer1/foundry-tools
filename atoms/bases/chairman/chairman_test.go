package baseschairman

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/chairmanfixed"
	"dagger/foundry-tools/internal/checks"
)

func TestTheDeclarationValidates(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StageBases || Def.Toolchain != "none" || Def.Group != checks.GroupOf(id) || Def.Timeout != 15*time.Minute ||
		Def.Surface != "all:bases/chairman/Dockerfile,bases/chairman/packages.lock" {
		t.Errorf("%+v", Def)
	}
}

func fake(t *testing.T, res chairmanfixed.Result, err error) *string {
	t.Helper()
	old := validate
	var seen string
	validate = func(root, img string) (chairmanfixed.Result, error) {
		seen = root + "|" + img
		return res, err
	}
	t.Cleanup(func() { validate = old })
	return &seen
}

// The verdict is the kit's answer: a pass, a finding per problem, or a could-not-run.
func TestTheVerdictIsTheKitsAnswer(t *testing.T) {
	t.Setenv(ImageEnv, "")
	seen := fake(t, chairmanfixed.Result{Checks: 13, Rows: 114, Locked: 114, Note: "the assembled image held"}, nil)
	v := check(context.Background(), atoms.Input{Root: "/src"})
	atomtest.Expect(t, v, checks.StatePass, atomtest.Pass, "13 checks over 114 fixed-set rows and 114 locked packages", "the assembled image held")
	if *seen != "/src|/work/image" {
		t.Errorf("read %s", *seen)
	}

	t.Setenv(ImageEnv, "/elsewhere")
	seen = fake(t, chairmanfixed.Result{Problems: []string{"image: etc/shadow is 0644", "second"}}, nil)
	atomtest.Expect(t, check(context.Background(), atoms.Input{Root: "/r"}), checks.StateFindings, atomtest.Findings, "2 problem(s) in the chairman base", "image: etc/shadow is 0644")
	if *seen != "/r|/elsewhere" {
		t.Errorf("read %s", *seen)
	}

	var many []string
	for i := 0; i < 100; i++ {
		many = append(many, fmt.Sprintf("problem %03d", i))
	}
	fake(t, chairmanfixed.Result{Problems: many}, nil)
	v = check(context.Background(), atoms.Input{Root: "/r"})
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "100 problem(s)", "problem 059", "... and 40 more")
	if strings.Contains(v.Reason+strings.Join(v.Logs, "\n"), "problem 060") {
		t.Error("the list was not cut")
	}

	fake(t, chairmanfixed.Result{}, errors.New("the base's tables could not be read: gone"))
	atomtest.Expect(t, check(context.Background(), atoms.Input{Root: "/r"}), checks.StateCannotRun, atomtest.Cannot, "bases:chairman: CANNOT RUN", "tables could not be read")
}
