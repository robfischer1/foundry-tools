package devgovuln

import (
	"context"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

func TestTheDeclarationIsTheDevVerbOfGovulncheck(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != "dev:go-vuln" || Def.Stage != "dev" || Def.Lane != checks.LaneGo || Def.Group != checks.GroupLanguage ||
		Def.Toolchain != "go" || Def.Surface != "root:go.mod" || strings.Join(Def.Needs, ",") != "tool:govulncheck" || Def.WritesTree {
		t.Errorf("%+v", Def)
	}
}

type fake struct {
	out  string
	code int
	runs []string
}

func (f *fake) exec(_ context.Context, c atoms.Cmd) (string, int) {
	line := strings.Join(append([]string{c.Name}, c.Args...), " ")
	f.runs = append(f.runs, line)
	if line == "govulncheck ./..." {
		return f.out, f.code
	}
	return "", 0
}

func input(f *fake) atomkit.In {
	return atomkit.In{Root: "/src", Files: []string{"go.mod", "main.go"}, Exec: f.exec, Now: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)}
}

// A clean scan passes, a finding (govulncheck's exit 3) fails, and the verdict
// is the dev atom's own.
func TestACleanScanPassesAndAFindingFails(t *testing.T) {
	f := &fake{}
	v := check(context.Background(), input(f))
	if v.Atom != "dev:go-vuln" || v.Stage != "dev" || v.State != int(checks.StatePass) || v.Reason != "dev:go-vuln: PASS" {
		t.Errorf("clean: %+v", v)
	}
	if strings.Join(f.runs, "|") != "go mod download|govulncheck -version|govulncheck ./..." {
		t.Errorf("runs %v", f.runs)
	}
	f = &fake{out: "Vulnerability #1: GO-2026-0001\n", code: 3}
	v = check(context.Background(), input(f))
	if v.Atom != "dev:go-vuln" || v.State != int(checks.StateFindings) || !strings.Contains(v.Reason, "dev:go-vuln: FINDINGS") || !strings.Contains(v.Reason, "GO-2026-0001") {
		t.Errorf("finding: %+v", v)
	}
	f = &fake{out: "boom", code: 2}
	if v = check(context.Background(), input(f)); v.State != int(checks.StateCannotRun) {
		t.Errorf("a scan that did not complete: %+v", v)
	}
}
