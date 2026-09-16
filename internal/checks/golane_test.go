package checks

import (
	"strings"
	"testing"
)

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

// The lane gates a module wherever it sits, and only a module the go command
// would build.
func TestGoModuleDirsFindsEveryModuleTheGoCommandWouldBuild(t *testing.T) {
	files := []string{
		"tools/forge/go.mod", "go.mod", "styx/go.mod", "bigintschema/go.mod",
		"tools/forge/internal/x.go", "go.sum", "bases/blade-proxy/go.mod",
		"third_party/vendor/lib/go.mod", "cmd/testdata/fixture/go.mod",
		"_scratch/go.mod", "x/.hidden/go.mod", "template/go.mod.jinja", "notgo.mod",
		"a/b/go.mod/c.go",
	}
	got := GoModuleDirs(files)
	want := []string{".", "bases/blade-proxy", "bigintschema", "styx", "tools/forge"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("GoModuleDirs = %v, want %v", got, want)
	}
	if got := GoModuleDirs([]string{"tools/forge/go.mod"}); strings.Join(got, ",") != "tools/forge" {
		t.Errorf("a nested module alone = %v, want [tools/forge] with no root", got)
	}
	if got := GoModuleDirs([]string{"pyproject.toml", "vendor/go.mod"}); len(got) != 0 {
		t.Errorf("no buildable go.mod = %v, want none", got)
	}
}

func TestFoldModulesLetsAFindingOutrankACannotRun(t *testing.T) {
	a := AtomByID("go:vet")
	pass := VerdictOf(a, 0, "")
	finding := VerdictOf(a, 1, "x.go:3: unreachable code")
	cannot := VerdictOf(a, 2, "proxy 502")

	v := FoldModules(a, []ModuleVerdict{{".", pass}, {"tools/forge", cannot}, {"styx", finding}})
	if v.State != int(StateFindings) || v.Result != "findings" || v.Atom != "go:vet" || v.Lane != "go" || v.Stage != a.Stage {
		t.Fatalf("fold = %+v, want findings on go:vet", v)
	}
	for _, want := range []string{"FINDINGS in 1 of 3 Go modules (styx)", "── module tools/forge ──", "proxy 502", "── module styx ──", "unreachable code", "── module . ──"} {
		if !strings.Contains(v.Reason, want) {
			t.Errorf("reason lacks %q:\n%s", want, v.Reason)
		}
	}

	v = FoldModules(a, []ModuleVerdict{{".", pass}, {"tools/forge", cannot}})
	if v.State != int(StateCannotRun) || !strings.Contains(v.Reason, "CANNOT RUN in 1 of 2 Go modules (tools/forge)") {
		t.Errorf("a cannot-run with no finding = %+v", v)
	}
	v = FoldModules(a, []ModuleVerdict{{"tools/forge", cannot}, {".", pass}, {"styx", cannot}})
	if v.State != int(StateCannotRun) || !strings.Contains(v.Reason, "(tools/forge, styx)") {
		t.Errorf("two cannot-runs = %+v, want both named", v)
	}
	v = FoldModules(a, []ModuleVerdict{{"styx", finding}, {".", finding}})
	if v.State != int(StateFindings) || !strings.Contains(v.Reason, "FINDINGS in 2 of 2 Go modules (styx, .)") {
		t.Errorf("two findings = %+v", v)
	}
	v = FoldModules(a, []ModuleVerdict{{"tools/forge", pass}})
	if v.State != int(StatePass) || !strings.Contains(v.Reason, "PASS in 1 Go module (tools/forge)") {
		t.Errorf("one nested module = %+v", v)
	}
	v = FoldModules(a, []ModuleVerdict{{".", pass}, {"styx", pass}})
	if v.State != int(StatePass) || v.Result != "pass" || !strings.Contains(v.Reason, "PASS in 2 Go modules (., styx)") {
		t.Errorf("all passing = %+v", v)
	}
}
