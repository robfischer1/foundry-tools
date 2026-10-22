package gogovulncheck

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

func TestTheDeclarationValidatesAndNamesItsRow(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrepush || Def.Lane != checks.LaneGo || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "go" || Def.Surface != "any:**/go.mod" || Def.NotOnForks != "" ||
		strings.Join(Def.Needs, ",") != "tool:govulncheck" {
		t.Errorf("%+v", Def)
	}
}

// The atom runs internal/vulncheck's scan under its own row (the scan's cases are
// held there).
func TestTheAtomRunsTheSharedScanUnderItsOwnRow(t *testing.T) {
	exec := func(_ context.Context, c atoms.Cmd) (string, int) {
		if c.Name == "govulncheck" && len(c.Args) == 1 && c.Args[0] == "./..." {
			return "Vulnerability #1: GO-2026-0001\n", 3
		}
		return "", 0
	}
	v := check(t.Context(), atomkit.In{Root: "/src", Files: []string{"go.mod"}, Exec: exec})
	if v.Atom != "go:govulncheck" || v.Stage != checks.StagePrepush || v.State != 1 || !strings.Contains(v.Reason, "go:govulncheck: FINDINGS (exit 1)") {
		t.Errorf("%+v", v)
	}
}
