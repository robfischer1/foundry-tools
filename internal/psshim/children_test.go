package psshim

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestParentRecognisesTreeKillsQuestionOnly(t *testing.T) {
	if p, ok := Parent([]string{"-o", "pid", "--no-headers", "--ppid", "42"}); !ok || p != 42 {
		t.Errorf("%d %v", p, ok)
	}
	for _, args := range [][]string{nil, {"aux"}, {"-o", "pid", "--no-headers", "--ppid", "x"}, {"-o", "pid", "--no-headers", "--ppid", "0"}, {"-o", "rss=", "-g", "1", "2"}} {
		if _, ok := Parent(args); ok {
			t.Errorf("%q was taken for tree-kill's question", args)
		}
	}
}

func TestChildrenAreTheProcessesWhoseParentIsAsked(t *testing.T) {
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	var out, errOut bytes.Buffer
	if code := AnswerChildren("/proc", os.Getpid(), &out, &errOut); code != 0 || !strings.Contains("\n"+out.String(), "\n"+strconv.Itoa(child.Process.Pid)+"\n") {
		t.Errorf("exit %d, %q %q", code, out.String(), errOut.String())
	}
	// a process nobody is the parent of: ps exits 1, saying nothing on stdout
	out.Reset()
	if code := AnswerChildren("/proc", 2147483646, &out, &errOut); code != 1 || out.Len() != 0 {
		t.Errorf("exit %d, %q", code, out.String())
	}
	// a /proc that cannot be read
	if code := AnswerChildren(t.TempDir()+"/nope", 1, &out, &errOut); code != 1 {
		t.Errorf("exit %d", code)
	}
}

func TestParentOfReadsFieldFourAfterTheLastParen(t *testing.T) {
	if p, ok := parentOf("12 (a (b) c) S 7 12 12 0"); !ok || p != 7 {
		t.Errorf("%d %v", p, ok)
	}
	for _, bad := range []string{"", "12 (x) S", "12 (x) S zz"} {
		if _, ok := parentOf(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestRunAnswersTreeKillsQuestion(t *testing.T) {
	code, path, _ := runPS(t, "-o", "pid", "--no-headers", "--ppid", "2147483646")
	if code != 1 || path != "" {
		t.Errorf("exit %d, handed to %q", code, path)
	}
}
