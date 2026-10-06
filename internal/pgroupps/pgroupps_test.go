package pgroupps

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestGroupIsOnlyGomutantsQuestion(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		pgid int
		ok   bool
	}{
		{"gomutants' question", []string{"-o", "rss=", "-g", "42"}, 42, true},
		{"too few", []string{"-o", "rss=", "-g"}, 0, false},
		{"too many", []string{"-o", "rss=", "-g", "42", "x"}, 0, false},
		{"another flag first", []string{"-e", "rss=", "-g", "42"}, 0, false},
		{"another column", []string{"-o", "pid=", "-g", "42"}, 0, false},
		{"another selector", []string{"-o", "rss=", "-p", "42"}, 0, false},
		{"a group name", []string{"-o", "rss=", "-g", "wheel"}, 0, false},
		{"pgid zero", []string{"-o", "rss=", "-g", "0"}, 0, false},
		{"pgid one", []string{"-o", "rss=", "-g", "1"}, 1, true},
	} {
		pgid, ok := Group(c.args)
		if ok != c.ok || (ok && pgid != c.pgid) {
			t.Errorf("%s: got %d %v, want %d %v", c.name, pgid, ok, c.pgid, c.ok)
		}
	}
}

// stat is a /proc/<pid>/stat line with the given name, pgrp and rss pages.
func stat(name string, pgrp, rss int) string {
	// state ppid pgrp session tty tpgid flags minflt cminflt majflt cmajflt
	// utime stime cutime cstime priority nice threads itrealvalue starttime
	// vsize rss rsslim
	return "7 (" + name + ") S 1 " + strconv.Itoa(pgrp) + " 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 0 0 " + strconv.Itoa(rss) + " 18446744073709551615\n"
}

func fakeProc(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRSSSumsTheGroupInKiB(t *testing.T) {
	proc := fakeProc(t, map[string]string{
		"10/stat":   stat("go", 10, 100),
		"11/stat":   stat("circuitcore.tes", 10, 2000),
		"12/stat":   stat("gomutants", 5, 9999), // another group
		"13/stat":   stat("a) (b) c", 10, 3),    // a name with spaces and parentheses
		"14/stat":   "14 (truncated) S 1 10",    // too short to read
		"15/stat":   "no parenthesis at all",    // not a stat line
		"16/stat":   "16 (x) S 1 notanumber 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 0 0 5 0\n",
		"17/stat":   "17 (x) S 1 10 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 0 0 lots 0\n",
		"self/stat": stat("self", 10, 77),                                      // not a pid directory
		"18/status": "no stat file here",                                       // a process gone before its read
		"19/stat":   "19 (x) S 1 10 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 0 0 6\n", // ends at rss
	})
	got, err := RSS(proc, 10, 4)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{400, 8000, 12, 24}; !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if _, err := RSS(filepath.Join(proc, "missing"), 10, 4); err == nil {
		t.Error("an unreadable proc root answered no error")
	}
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAnswerPrintsAColumnAndExitsAsPsDoes(t *testing.T) {
	proc := fakeProc(t, map[string]string{"10/stat": stat("go", 10, 1), "11/stat": stat("t", 10, 2)})
	var out, errOut bytes.Buffer
	if code := Answer(proc, 10, 4, &out, &errOut); code != 0 || out.String() != "4\n8\n" || errOut.Len() != 0 {
		t.Errorf("match: exit %d out %q err %q", code, out.String(), errOut.String())
	}
	out.Reset()
	if code := Answer(proc, 99, 4, &out, &errOut); code != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), "pgid 99: no process in the group") {
		t.Errorf("no match: exit %d out %q err %q", code, out.String(), errOut.String())
	}
	errOut.Reset()
	if code := Answer(filepath.Join(proc, "missing"), 10, 4, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "pgid 10: open ") {
		t.Errorf("unreadable: exit %d err %q", code, errOut.String())
	}
}

// Against the real /proc: a child in its own process group is found, with a
// resident set, which is exactly what procps' `ps -g` could not do. The read
// is polled because a child sampled before its exec completes can read 0.
func TestRSSFindsARealProcessGroup(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	var got []int64
	var err error
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if got, err = RSS("/proc", cmd.Process.Pid, int64(os.Getpagesize()/1024)); err == nil && len(got) == 1 && got[0] > 0 {
			return
		}
	}
	t.Errorf("got %v %v, want one process with a resident set", got, err)
}
