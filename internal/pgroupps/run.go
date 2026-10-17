package pgroupps

import (
	"os"
	"syscall"
)

// execve is syscall.Exec, a variable so a test can say where a question that is
// not gomutants' would have gone.
var execve = syscall.Exec

// Run is `ps` on the mutation atoms' PATH, as a process: gomutants' question (a
// process group's resident set) and Stryker's (a process's children) are answered
// from /proc, every other question is handed to the real ps, and a
// ps that cannot be run exits 127 as a shell does for a missing command. args
// are ps's own, without the program name.
//
// It was a program of its own (pgroupps/) built into the lane's container; it is
// the atom's own binary now, reached through a link named ps (atoms.LinkHelper),
// because an atom is one file and the executor's toolchain image carries no
// helper binaries.
func Run(args []string) int {
	if pgid, ok := Group(args); ok {
		return Answer("/proc", pgid, int64(os.Getpagesize()/1024), os.Stdout, os.Stderr)
	}
	if ppid, ok := Parent(args); ok {
		return AnswerChildren("/proc", ppid, os.Stdout, os.Stderr)
	}
	err := execve(RealPS, append([]string{"ps"}, args...), os.Environ())
	_, _ = os.Stderr.WriteString("ps (pgroupps): " + RealPS + ": " + err.Error() + "\n")
	return 127
}
