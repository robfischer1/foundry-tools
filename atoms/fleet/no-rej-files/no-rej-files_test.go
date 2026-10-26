package fleetnorejfiles

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

func TestTheDeclarationValidates(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != "precommit" || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(Def.ID) || Def.Toolchain != "none" {
		t.Errorf("%+v", Def)
	}
}

func TestNoRejFiles(t *testing.T) {
	for _, tc := range []struct {
		name      string
		files     []string
		wantState int
		want      string
	}{
		{"an empty population is a pass", nil, 0, "fleet:no-rej-files: PASS"},
		{"no reject file", []string{"a.go", "pyproject.toml", "docs/rej.md"}, 0, "fleet:no-rej-files: PASS"},
		{"a reject file beside its target", []string{"pyproject.toml", "pyproject.toml.rej"}, 1, "pyproject.toml.rej: a reject file"},
		{"every reject file is named", []string{"a/x.rej", "b/y.yaml.rej", "c.go"}, 1, "a/x.rej: a reject file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := check(context.Background(), atomkit.In{Files: tc.files})
			if v.State != tc.wantState || !strings.Contains(v.Reason, tc.want) {
				t.Fatalf("state %d reason %q, want state %d containing %q", v.State, v.Reason, tc.wantState, tc.want)
			}
			if tc.name == "every reject file is named" && !strings.Contains(v.Reason, "b/y.yaml.rej") {
				t.Fatalf("second reject file not named: %q", v.Reason)
			}
		})
	}
}

func TestAFileListThatCannotBeReadIsNotAPass(t *testing.T) {
	v := check(context.Background(), atomkit.In{FilesErr: errors.New("git ls-files: boom")})
	if v.State == int(checks.StatePass) {
		t.Fatalf("an unenumerated tree passed: %+v", v)
	}
}
