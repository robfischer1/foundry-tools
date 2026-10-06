// Package pgroupps answers gomutants' one `ps` question the way gomutants
// means it: the resident set of every process in a process GROUP.
//
// WHY THIS EXISTS. gomutants v0.6.1 caps each mutant's `go test` tree at
// 2 GiB resident by polling `ps -o rss= -g <pgid>` once a second and killing
// the group past the cap (internal/runner/worker_unix.go). `-g` selects by
// process group on BSD and macOS only. procps-ng, the Linux ps, reads `-g` as
// "session or effective group name", so for a pgid that leads no session it
// selects nothing and exits 1; gomutants reads any error as 0 bytes, and the
// cap never fires. MEASURED 2026-10-05: procps-ng 4.0.4, a setpgid child —
// `ps -o rss= -p <pid>` answers 1972, `ps -o rss= -g <pgid>` answers nothing,
// exit 1, and procps has no flag that selects by pgid at all.
//
// WHAT IT COST. A mutant that makes a test allocate without bound grows until
// the exec's 16 GiB cgroup runs out, and the engine's reaper then ends every
// process in the exec (internal/execmem says how), gomutants included: the
// lane records "the atom never ran: exit code: 137" and nothing is measured.
// 19 go:mutation runs since 2026-09-21 ended that way; on stellar-core-go
// 4d767c3 both runs died the second the kernel killed a 5–8 GiB
// circuitcore.test, a size the 2 GiB cap was written to stop.
//
// So the Go mutation exec puts this binary first on its PATH as `ps`. The one
// argument vector gomutants sends is answered from /proc; anything else is
// handed to the real ps unchanged, so a test that runs ps sees ps.
package pgroupps

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// RealPS is the ps every other question is handed to.
const RealPS = "/bin/ps"

// Group answers the pgid in args when args are exactly gomutants' question,
// `-o rss= -g <pgid>`, and false otherwise.
func Group(args []string) (int, bool) {
	if len(args) != 4 || args[0] != "-o" || args[1] != "rss=" || args[2] != "-g" {
		return 0, false
	}
	pgid, err := strconv.Atoi(args[3])
	return pgid, err == nil && pgid > 0
}

// RSS is the resident set, in KiB, of every process under proc whose process
// group is pgid, one per process in /proc order — the column ps prints.
// pageKiB is the page size in KiB, which /proc/<pid>/stat counts rss in.
func RSS(proc string, pgid int, pageKiB int64) ([]int64, error) {
	entries, err := os.ReadDir(proc)
	if err != nil {
		return nil, err
	}
	var kib []int64
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		// A process that exits between the listing and the read is simply
		// not in the group any more.
		raw, err := os.ReadFile(filepath.Join(proc, e.Name(), "stat"))
		if err != nil {
			continue
		}
		group, pages, ok := statFields(string(raw))
		if ok && group == pgid {
			kib = append(kib, pages*pageKiB)
		}
	}
	return kib, nil
}

// statFields reads the process group (field 5) and resident pages (field 24)
// out of a /proc/<pid>/stat line. Fields are counted after the LAST ')',
// because the command name before it is parenthesised and may hold spaces and
// parentheses of its own.
func statFields(stat string) (pgid int, pages int64, ok bool) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, 0, false
	}
	// After the name: state(3) ppid(4) pgrp(5) … rss(24), so pgrp is the
	// third field and rss the twenty-second.
	f := strings.Fields(stat[i+1:])
	if len(f) < 22 {
		return 0, 0, false
	}
	pgid, err1 := strconv.Atoi(f[2])
	pages, err2 := strconv.ParseInt(f[21], 10, 64)
	return pgid, pages, err1 == nil && err2 == nil
}

// errNoProcess is ps's own answer for a selection that matched nothing.
var errNoProcess = errors.New("no process in the group")

// Answer writes gomutants' answer for pgid to out, one KiB figure a line, and
// exits as ps does: 0 with a match, 1 with none.
func Answer(proc string, pgid int, pageKiB int64, out, errOut io.Writer) int {
	kib, err := RSS(proc, pgid, pageKiB)
	if err == nil && len(kib) == 0 {
		err = errNoProcess
	}
	if err != nil {
		fmt.Fprintf(errOut, "ps (pgroupps): pgid %d: %v\n", pgid, err)
		return 1
	}
	for _, k := range kib {
		fmt.Fprintln(out, k)
	}
	return 0
}
