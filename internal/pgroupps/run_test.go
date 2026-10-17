package pgroupps

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runPS runs Run with execve replaced, and answers what it exited with and where
// the question was handed.
func runPS(t *testing.T, args ...string) (code int, path string, argv []string) {
	t.Helper()
	execve = func(p string, a []string, _ []string) error { path, argv = p, a; return errors.New("not here") }
	t.Cleanup(func() { execve = syscall.Exec })
	return Run(args), path, argv
}

// gomutants' question is answered here and never reaches the real ps.
func TestRunAnswersTheGroupQuestion(t *testing.T) {
	code, path, _ := runPS(t, "-o", "rss=", "-g", "2147483646")
	if code != 1 || path != "" {
		t.Errorf("exit %d, handed to %q", code, path)
	}
}

// Every other question goes to the real ps unchanged; a ps that cannot be run
// exits 127, as a shell does for a missing command.
func TestRunHandsAnythingElseToTheRealPS(t *testing.T) {
	code, path, argv := runPS(t, "aux")
	if path != RealPS || len(argv) != 2 || argv[0] != "ps" || argv[1] != "aux" || code != 127 {
		t.Errorf("exit %d, handed %q %q", code, path, argv)
	}
}

// Run answers in KiB, as ps does: a sleeping process is resident in well under a
// GiB, which is 1<<20 KiB.
func TestRunAnswersInKiB(t *testing.T) {
	child := exec.Command("sleep", "30")
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = saved })
	var kib int64
	for deadline := time.Now().Add(5 * time.Second); kib == 0 && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if code, _, _ := runPS(t, "-o", "rss=", "-g", strconv.Itoa(child.Process.Pid)); code != 0 {
			t.Fatalf("exit %d", code)
		}
		buf := make([]byte, 64)
		n, _ := r.Read(buf)
		kib, _ = strconv.ParseInt(strings.TrimSpace(string(buf[:n])), 10, 64)
	}
	if kib <= 0 || kib >= 1<<20 {
		t.Errorf("answered %d KiB for a sleeping process", kib)
	}
}
