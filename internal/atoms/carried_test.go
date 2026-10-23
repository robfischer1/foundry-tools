package atoms

import (
	"context"
	"os"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

func TestCarriedReadsMapsAndKeeps(t *testing.T) {
	t.Setenv(checks.EnvMutationRanges, "a.go:5-5,b.py:1-1")
	diff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,0 +2 @@\n+x\n"
	in := Input{Root: "/r", Exec: func(_ context.Context, c Cmd) (string, int) { return diff, 0 }}
	c, err := ReadCarried(context.Background(), in, "s", "go:mutation", func(f string) bool { return strings.HasSuffix(f, ".go") })
	if err != nil || len(c.Ranges) != 1 || c.Ranges["a.go"][0].Start != 6 || len(c.Moved) != 1 || c.Moved[0].Probe != "go:mutation" {
		t.Fatalf("%+v %v", c, err)
	}
	if got := c.Files(); len(got) != 1 || got[0] != "a.go" {
		t.Fatalf("files %v", got)
	}
	v := c.Into(checks.Verdict{State: 1})
	if len(v.Findings) != 1 {
		t.Fatalf("a graded verdict carries the moves: %+v", v.Findings)
	}
	if v := c.Into(checks.Verdict{State: 2}); len(v.Findings) != 0 {
		t.Fatal("a could-not-run carries nothing")
	}
	var none *Carried
	if !none.Empty() || none.Files() != nil || len(none.Into(checks.Verdict{}).Findings) != 0 || none.WithCarried("d", "/") != "d" {
		t.Fatal("nil carries nothing")
	}
	in.Exec = func(_ context.Context, c Cmd) (string, int) { return "fatal: bad", 128 }
	if _, err := ReadCarried(context.Background(), in, "s", "x", func(string) bool { return true }); err == nil || !strings.Contains(err.Error(), "fatal: bad") {
		t.Fatalf("err %v", err)
	}
	t.Setenv(checks.EnvMutationRanges, "")
	if c, err := ReadCarried(context.Background(), in, "s", "x", func(string) bool { return true }); c != nil || err != nil {
		t.Fatal("unset is none")
	}
}

func TestTreeLinesReadsOnceAndAnswersEmptyPastTheEnd(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+"/f.rs", []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	line := TreeLines(root)
	if line("f.rs", 2) != "two" || line("f.rs", 0) != "" || line("f.rs", 9) != "" || line("nope", 1) != "" {
		t.Fatal("tree lines")
	}
}
