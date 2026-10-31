package psshim

import (
	"errors"
	"syscall"
	"testing"
)

// runPS runs Run with execve replaced, and answers what it exited with and where
// the question was handed.
func runPS(t *testing.T, args ...string) (code int, path string, argv []string) {
	t.Helper()
	execve = func(p string, a []string, _ []string) error { path, argv = p, a; return errors.New("not here") }
	t.Cleanup(func() { execve = syscall.Exec })
	return Run(args), path, argv
}

// Tree-kill's question is answered here and never reaches the real ps.
func TestRunAnswersTheChildrenQuestion(t *testing.T) {
	code, path, _ := runPS(t, "-o", "pid", "--no-headers", "--ppid", "2147483646")
	if code != 1 || path != "" {
		t.Errorf("exit %d, handed to %q", code, path)
	}
}

// gomutants' old question (a group's resident set) is no longer answered: v0.7.1
// reads /proc itself, and the real ps gets it like any other.
func TestRunNoLongerAnswersTheGroupQuestion(t *testing.T) {
	code, path, _ := runPS(t, "-o", "rss=", "-g", "2147483646")
	if path != RealPS || code != 127 {
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
