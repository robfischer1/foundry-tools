// pgroupps is `ps` on the Go mutation exec's PATH: gomutants' process-group
// RSS question answered from /proc, every other question handed to the real
// ps. The logic, and why it exists, is in internal/pgroupps; this is only its
// process.
package main

import (
	"os"
	"syscall"

	"dagger/foundry-tools/internal/pgroupps"
)

// exit and execve are os.Exit and syscall.Exec, variables so main_test.go can
// run main: go:mutation grades a line no test executes as NOT COVERED.
var (
	exit   = os.Exit
	execve = syscall.Exec
)

func main() {
	args := os.Args[1:]
	if pgid, ok := pgroupps.Group(args); ok {
		exit(pgroupps.Answer("/proc", pgid, int64(os.Getpagesize()/1024), os.Stdout, os.Stderr))
		return
	}
	err := execve(pgroupps.RealPS, append([]string{"ps"}, args...), os.Environ())
	os.Stderr.WriteString("ps (pgroupps): " + pgroupps.RealPS + ": " + err.Error() + "\n")
	exit(127)
}
