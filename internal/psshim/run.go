package psshim

import (
	"os"
	"syscall"
)

// execve is syscall.Exec, a variable so a test can say where a question that is
// not gomutants' would have gone.
var execve = syscall.Exec

// Run is `ps` on the ts mutation atom's PATH, as a process: Stryker's question (a
// process's children) is answered from /proc, every other question is handed to
// the real ps, and a ps that cannot be run exits 127 as a shell does for a
// missing command. args are ps's own, without the program name.
//
// It is the atom's own binary, reached through a link named ps
// (atoms.LinkHelper), because an atom is one file and the executor's toolchain
// image carries no helper binaries.
func Run(args []string) int {
	if ppid, ok := Parent(args); ok {
		return AnswerChildren("/proc", ppid, os.Stdout, os.Stderr)
	}
	err := execve(RealPS, append([]string{"ps"}, args...), os.Environ())
	_, _ = os.Stderr.WriteString("ps (psshim): " + RealPS + ": " + err.Error() + "\n")
	return 127
}
