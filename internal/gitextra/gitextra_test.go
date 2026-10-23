package gitextra

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// realGit is a double for the real git: it prints a fixed diff and exits code.
type realGit struct {
	name string
	args []string
	out  string
	code int
}

func (g *realGit) run(name string, args []string, stdout, _ io.Writer) int {
	g.name, g.args = name, args
	_, _ = io.WriteString(stdout, g.out)
	return g.code
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

const realDiff = "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -3,0 +4 @@\n+x\n"

func TestTheMergeBaseDiffGainsTheExtraRanges(t *testing.T) {
	g := &realGit{out: realDiff}
	var out bytes.Buffer
	args := []string{"diff", "--unified=0", "--no-color", "--no-ext-diff", "--merge-base", "abc"}
	code := answer(args, env(map[string]string{EnvRealGit: "/usr/bin/git", EnvRanges: "b.go:7-8"}), g.run, &out, io.Discard)
	if code != 0 || g.name != "/usr/bin/git" || strings.Join(g.args, " ") != strings.Join(args, " ") {
		t.Fatalf("code %d, ran %s %v", code, g.name, g.args)
	}
	want := realDiff + "diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -0,0 +7,2 @@\n+\n+\n"
	if out.String() != want {
		t.Fatalf("out =\n%s\nwant\n%s", out.String(), want)
	}
}

func TestEveryOtherQuestionIsTheRealGitsAlone(t *testing.T) {
	for _, args := range [][]string{{"rev-parse", "--show-toplevel"}, {"diff", "--name-only", "abc"}} {
		g := &realGit{out: "/src\n"}
		var out bytes.Buffer
		if code := answer(args, env(map[string]string{EnvRealGit: "/usr/bin/git", EnvRanges: "b.go:7-8"}), g.run, &out, io.Discard); code != 0 || out.String() != "/src\n" {
			t.Fatalf("%v: code %d out %q", args, code, out.String())
		}
	}
}

func TestAFailedDiffAddsNothingAndKeepsItsExit(t *testing.T) {
	g := &realGit{out: "", code: 128}
	var out bytes.Buffer
	if code := answer([]string{"diff", "--merge-base", "x"}, env(map[string]string{EnvRealGit: "/g", EnvRanges: "b.go:1-1"}), g.run, &out, io.Discard); code != 128 || out.Len() != 0 {
		t.Fatalf("code %d out %q", code, out.String())
	}
}

func TestNoRealGitOrBadRangesExit127And2(t *testing.T) {
	var errs bytes.Buffer
	if code := answer([]string{"status"}, env(nil), (&realGit{}).run, io.Discard, &errs); code != 127 || !strings.Contains(errs.String(), EnvRealGit) {
		t.Fatalf("code %d: %s", code, errs.String())
	}
	errs.Reset()
	g := &realGit{out: realDiff}
	if code := answer([]string{"diff", "--merge-base", "x"}, env(map[string]string{EnvRealGit: "/g", EnvRanges: "b.go"}), g.run, io.Discard, &errs); code != 2 || !strings.Contains(errs.String(), "no line range") {
		t.Fatalf("code %d: %s", code, errs.String())
	}
}

func TestRunWithNoRealGitExits127(t *testing.T) {
	t.Setenv(EnvRealGit, "")
	if code := Run([]string{"status"}); code != 127 {
		t.Fatalf("code %d", code)
	}
}

func TestRunRealAnswersTheProgramsExit(t *testing.T) {
	var out, errs bytes.Buffer
	if code := runReal("go", []string{"env", "GOOS"}, &out, &errs); code != 0 || strings.TrimSpace(out.String()) == "" {
		t.Fatalf("go env: code %d out %q err %q", code, out.String(), errs.String())
	}
	if code := runReal("go", []string{"no-such-command"}, io.Discard, io.Discard); code == 0 || code == 127 {
		t.Fatalf("a program that ran and failed keeps its own exit, got %d", code)
	}
	errs.Reset()
	if code := runReal("/no/such/git", nil, io.Discard, &errs); code != 127 || !strings.Contains(errs.String(), "/no/such/git") {
		t.Fatalf("a program that would not start is 127, got %d: %s", code, errs.String())
	}
}
