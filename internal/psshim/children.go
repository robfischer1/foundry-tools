// Package psshim answers the one `ps` question Stryker's tree-kill asks, from
// /proc, and hands every other question to the real ps.
//
// WHY IT EXISTS. The executor's python, bun and node images carry no procps, and
// a procps mounted from another image cannot load its libraries, so Stryker's
// teardown (`ps -o pid --no-headers --ppid <pid>`) met ENOENT and died right
// after the dry run (theia #57). The Go mutation exec once put this on PATH for
// gomutants' `ps -o rss= -g <pgid>` too (v0.6.1, whose RSS cap never fired under
// procps); gomutants v0.7.1 reads /proc itself on Linux and that question is no
// longer answered here.
package psshim

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// THE OTHER QUESTION A MUTATION RUN ASKS ps. Stryker tears its test-runner
// processes down with tree-kill, which asks `ps -o pid --no-headers --ppid <pid>`
// for a process's children, and an unhandled ENOENT for a ps that is not there
// kills Stryker right after the dry run (theia #57). The executor's python, bun
// and node images carry no procps, and a procps mounted from another image cannot
// load its libraries, so the atom's own binary answers this one too, from /proc.

// errNoProcess is ps's own answer for a selection that matched nothing.
var errNoProcess = errors.New("no process in the group")

// RealPS is the ps every other question is handed to.
const RealPS = "/bin/ps"

// Parent answers the pid in args when args are exactly tree-kill's question, and
// false otherwise.
func Parent(args []string) (int, bool) {
	if len(args) != 5 || args[0] != "-o" || args[1] != "pid" || args[2] != "--no-headers" || args[3] != "--ppid" {
		return 0, false
	}
	ppid, err := strconv.Atoi(args[4])
	return ppid, err == nil && ppid > 0
}

// Children is the pid of every process under proc whose parent is ppid, in /proc
// order.
func Children(proc string, ppid int) ([]int, error) {
	entries, err := os.ReadDir(proc)
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, _ := os.ReadFile(filepath.Join(proc, e.Name(), "stat")) // a process that exited reads empty
		if parent, ok := parentOf(string(raw)); ok && parent == ppid {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// parentOf is the parent pid (field 4) of a /proc/<pid>/stat line, counted after
// the LAST ')' because the command name before it may hold parentheses.
func parentOf(stat string) (int, bool) {
	f := strings.Fields(stat[strings.LastIndexByte(stat, ')')+1:])
	if len(f) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(f[1])
	return ppid, err == nil
}

// AnswerChildren writes tree-kill's answer for ppid to out, one pid a line, and
// exits as ps does: 0 with a match, 1 with none.
func AnswerChildren(proc string, ppid int, out, errOut io.Writer) int {
	pids, err := Children(proc, ppid)
	if err == nil && len(pids) == 0 {
		err = errNoProcess
	}
	if err != nil {
		fmt.Fprintf(errOut, "ps (psshim): ppid %d: %v\n", ppid, err)
		return 1
	}
	for _, p := range pids {
		fmt.Fprintf(out, "%d\n", p)
	}
	return 0
}
