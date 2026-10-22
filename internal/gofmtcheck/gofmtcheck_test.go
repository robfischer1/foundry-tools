package gofmtcheck

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// row is go:gofmt's, the row the tests below judge under; the dev atom's own is
// held by TestTheVerdictSaysTheRowThatAsked.
var row = checks.AtomDef{ID: "go:gofmt", Stage: checks.StagePrecommit, Lane: checks.LaneGo}

func check(ctx context.Context, in atomkit.In) checks.Verdict { return Check(ctx, in, row) }

// The verdict carries the row Check was handed: a second atom asking the same
// question reports under its own id and stage.
func TestTheVerdictSaysTheRowThatAsked(t *testing.T) {
	dev := checks.AtomDef{ID: "dev:go-fmt-check", Stage: "dev", Lane: checks.LaneGo}
	for _, tc := range []struct {
		out   string
		state int
		word  string
	}{{"", 0, "dev:go-fmt-check: PASS"}, {"main.go", 1, "dev:go-fmt-check: FINDINGS"}} {
		s := &script{answers: []answer{{out: tc.out}}}
		i := in("main.go")
		i.Exec = s.exec
		v := Check(context.Background(), i, dev)
		if v.Atom != dev.ID || v.Stage != "dev" || v.State != tc.state || !strings.Contains(v.Reason, tc.word) {
			t.Errorf("%+v", v)
		}
	}
	empty := Check(context.Background(), in("README.md"), dev)
	if empty.Atom != dev.ID || !strings.Contains(strings.Join(empty.Logs, "\n"), "dev:go-fmt-check: no Go files") {
		t.Errorf("%+v", empty)
	}
}

// script is an Exec that records the argv of each run and answers from a list.
type script struct {
	runs    [][]string
	answers []answer
}

type answer struct {
	out  string
	code int
}

func (s *script) exec(_ context.Context, c atoms.Cmd) (string, int) {
	s.runs = append(s.runs, append([]string{c.Name}, c.Args...))
	a := answer{}
	if len(s.answers) > 0 {
		a, s.answers = s.answers[0], s.answers[1:]
	}
	return a.out, a.code
}

func in(files ...string) atomkit.In { return atomkit.In{Root: "/src", Files: files} }

func TestGofmtReadsTheListingNotTheExit(t *testing.T) {
	s := &script{}
	i := in("README.md", "main.go", "cmd/x.go")
	i.Exec = s.exec
	v := check(context.Background(), i)
	if v.State != 0 || v.Reason != "go:gofmt: PASS" || len(s.runs) != 1 {
		t.Fatalf("%+v runs %v", v, s.runs)
	}
	if got := strings.Join(s.runs[0], " "); got != "gofmt -l main.go cmd/x.go" {
		t.Errorf("gofmt was run as %q: the .go files of the population, nothing else", got)
	}

	s = &script{answers: []answer{{out: "main.go", code: 0}}}
	i.Exec = s.exec
	v = check(context.Background(), i)
	if v.State != 1 || !strings.Contains(v.Reason, "go:gofmt: FINDINGS (exit 1)") || !strings.Contains(strings.Join(v.Logs, "\n"), "gofmt needed on:\nmain.go") {
		t.Errorf("%+v", v)
	}
}

func TestGofmtThatCannotParseIsACouldNotRun(t *testing.T) {
	s := &script{answers: []answer{{out: "main.go:1:1: expected 'package'", code: 2}}}
	i := in("main.go")
	i.Exec = s.exec
	v := check(context.Background(), i)
	if v.State != 2 || !strings.Contains(v.Reason, "gofmt exited 2; a file it could not parse is a file it did not check") || !strings.Contains(v.Reason, "expected 'package'") {
		t.Errorf("%+v", v)
	}
}

func TestNoGoFilesIsAPassThatRunsNothing(t *testing.T) {
	s := &script{}
	i := in("go.mod", "README.md")
	i.Exec = s.exec
	if v := check(context.Background(), i); v.State != 0 || len(s.runs) != 0 || !strings.Contains(strings.Join(v.Logs, "\n"), "go:gofmt: no Go files") {
		t.Errorf("%+v runs %v", v, s.runs)
	}
}

func TestATreeThatWouldNotEnumerateIsACouldNotRun(t *testing.T) {
	i := in("main.go")
	i.FilesErr = errors.New("boom")
	if v := check(context.Background(), i); v.State != 2 || !strings.Contains(v.Reason, "the tree would not enumerate: boom") {
		t.Errorf("%+v", v)
	}
}

// A population over the argv budget goes in as several runs, in order, and what
// each lists is the atom's finding.
func TestAHugePopulationIsRunInChunks(t *testing.T) {
	var files []string
	for n := range 2000 {
		files = append(files, fmt.Sprintf("pkg%04d/a_rather_long_file_name_to_fill_the_argv_budget_quickly_%04d.go", n, n))
	}
	s := &script{answers: []answer{{}, {out: "pkg1999/x.go"}}}
	i := in(files...)
	i.Exec = s.exec
	// the answers fall on the first two chunks whatever their number
	v := check(context.Background(), i)
	if len(s.runs) < 2 {
		t.Fatalf("%d runs for %d files", len(s.runs), len(files))
	}
	var total int
	for _, r := range s.runs {
		total += len(r) - 2
	}
	if total != len(files) {
		t.Errorf("the runs carried %d files, want %d", total, len(files))
	}
	if v.State != 1 || !strings.Contains(strings.Join(v.Logs, "\n"), "pkg1999/x.go") {
		t.Errorf("%+v", v)
	}
}

func TestAnEndedContextStopsBeforeTheNextRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &script{}
	i := in("main.go")
	i.Exec = s.exec
	if v := check(ctx, i); v.State != 2 || len(s.runs) != 0 || !strings.Contains(v.Reason, "stopped before the scan finished") {
		t.Errorf("%+v", v)
	}
}

// With no Exec given the atom runs the real gofmt, which the go toolchain
// container this atom runs in carries.
func TestTheRealGofmtFindsAnUnformattedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "good.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte("package x\nvar   a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := check(context.Background(), atomkit.In{Root: dir, Files: []string{"good.go", "bad.go"}})
	if v.State != 1 || !strings.Contains(strings.Join(v.Logs, "\n"), "bad.go") || strings.Contains(strings.Join(v.Logs, "\n"), "good.go") {
		t.Errorf("%+v", v)
	}
}
