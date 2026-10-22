package devgofmtcheck

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

func TestTheDeclarationIsTheReadOnlyDevVerb(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != "dev:go-fmt-check" || Def.Stage != "dev" || Def.Lane != checks.LaneGo || Def.Group != checks.GroupLanguage ||
		Def.Toolchain != "go" || Def.Surface != "root:go.mod" || len(Def.Needs) != 0 {
		t.Errorf("%+v", Def)
	}
	if Def.WritesTree {
		t.Error("the check form of fmt must not deliver a tree diff: it writes nothing")
	}
}

// script answers every exec with one output and exit, and records the argv.
type script struct {
	out  string
	code int
	runs [][]string
}

func (s *script) exec(_ context.Context, c atoms.Cmd) (string, int) {
	s.runs = append(s.runs, append([]string{c.Name}, c.Args...))
	return s.out, s.code
}

func input(s *script, files ...string) atomkit.In {
	return atomkit.In{Root: "/src", Files: files, Exec: s.exec}
}

// A file gofmt lists is a finding; a clean listing passes. The verdict is the
// dev atom's own, not go:gofmt's.
func TestFindingsFailAndACleanTreePasses(t *testing.T) {
	s := &script{out: "main.go"}
	v := check(context.Background(), input(s, "main.go", "README.md"))
	if v.Atom != "dev:go-fmt-check" || v.Stage != "dev" || v.State != int(checks.StateFindings) || !strings.Contains(v.Reason, "dev:go-fmt-check: FINDINGS") || !strings.Contains(v.Reason, "main.go") {
		t.Errorf("a listed file: %+v", v)
	}
	s = &script{}
	v = check(context.Background(), input(s, "main.go"))
	if v.Atom != "dev:go-fmt-check" || v.State != int(checks.StatePass) || v.Reason != "dev:go-fmt-check: PASS" {
		t.Errorf("a clean tree: %+v", v)
	}
	s = &script{code: 2, out: "main.go:1: expected declaration"}
	if v = check(context.Background(), input(s, "main.go")); v.State != int(checks.StateCannotRun) || !strings.Contains(v.Reason, "dev:go-fmt-check: CANNOT RUN") {
		t.Errorf("a file gofmt could not parse: %+v", v)
	}
}

// The only gofmt it runs is `gofmt -l`: no -w, no -d, no -s.
func TestItListsAndNeverRewrites(t *testing.T) {
	s := &script{}
	check(context.Background(), input(s, "a.go", "b/c.go"))
	if len(s.runs) != 1 || strings.Join(s.runs[0], " ") != "gofmt -l a.go b/c.go" {
		t.Errorf("runs %v", s.runs)
	}
}

// THE TREE IS UNTOUCHED, under the real gofmt: a file that needs formatting is
// reported and its bytes are what they were.
func TestAnUnformattedFileIsReportedAndLeftAsItWas(t *testing.T) {
	dir := t.TempDir()
	const bad = "package x\nfunc  f( ) {\n}\n"
	p := filepath.Join(dir, "bad.go")
	if err := os.WriteFile(p, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	v := check(context.Background(), atomkit.In{Root: dir, Files: []string{"bad.go"}})
	if v.State != int(checks.StateFindings) {
		t.Fatalf("%+v", v)
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != bad {
		t.Errorf("the file was rewritten: %q (%v)", got, err)
	}
}
