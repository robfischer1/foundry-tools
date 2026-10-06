package main

import (
	"errors"
	"os"
	"syscall"
	"testing"

	"dagger/foundry-tools/internal/pgroupps"
)

func run(t *testing.T, args ...string) (code int, path string, argv []string) {
	t.Helper()
	saved := os.Args
	code, path = -1, ""
	exit = func(c int) { code = c }
	execve = func(p string, a []string, _ []string) error { path, argv = p, a; return errors.New("not here") }
	t.Cleanup(func() { exit, execve, os.Args = os.Exit, syscall.Exec, saved })
	os.Args = append([]string{"ps"}, args...)
	main()
	return code, path, argv
}

// gomutants' question is answered here and never reaches the real ps.
func TestMainAnswersTheGroupQuestion(t *testing.T) {
	code, path, _ := run(t, "-o", "rss=", "-g", "2147483646")
	if code != 1 || path != "" {
		t.Errorf("exit %d, handed to %q", code, path)
	}
}

// Every other question goes to the real ps unchanged; a ps that cannot be
// run exits 127, as a shell does for a missing command.
func TestMainHandsAnythingElseToTheRealPS(t *testing.T) {
	code, path, argv := run(t, "aux")
	if path != pgroupps.RealPS || len(argv) != 2 || argv[0] != "ps" || argv[1] != "aux" || code != 127 {
		t.Errorf("exit %d, handed %q %q", code, path, argv)
	}
}
