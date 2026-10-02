// Package execmem runs one command and prints the memory of the cgroup it
// runs in, while it runs — so an exec the engine kills for memory says so in
// the output its error carries (foundry-tools #13052).
//
// WHY INSIDE THE EXEC, AND WHY WHILE IT RUNS. Each engine exec is its own
// cgroup v2 child, and inside it /sys/fs/cgroup IS that cgroup (measured
// 2026-10-02 in a cluster engine exec: memory.peak, memory.max and
// memory.events all readable, no engine access needed). The engine's own
// `buildkit-exec-peak:` line carries the same numbers, but it goes to the
// engine's log, which the runtime keeps for minutes. And a reading taken AFTER
// the command cannot be relied on: when the kernel's OOM killer fires in an
// exec, the engine's reaper ends every process left in it with cgroup.kill
// (flux forge/dagger-engine-helm.yaml), this one included. So the readings are
// printed as they change, and the last one printed before a kill is the one
// the record keeps.
//
// A line is printed for the first reading, each new GiB of peak, and each
// change of the OOM-kill count: a handful per run on stderr, never one a
// second. READ IT AS A LOWER BOUND, as the engine's own line is: a peak
// reached in the final interval before a kill is not seen.
package execmem

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Prefix starts every line this package prints; checks reads it back.
const Prefix = "exec-memory:"

// Reading is one read of a cgroup's memory files.
type Reading struct {
	Peak    int64  // memory.peak, bytes
	Max     string // memory.max as written: bytes, or "max"
	OOMKill int64  // memory.events oom_kill
}

// Line is the reading as printed.
func (r Reading) Line() string {
	return fmt.Sprintf("%s peak_bytes=%d memory_max=%s oom_kill=%d", Prefix, r.Peak, r.Max, r.OOMKill)
}

// Read reads dir's memory files. Only memory.peak is required: the other two
// read as "unknown" and zero when absent.
func Read(dir string) (Reading, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "memory.peak"))
	if err != nil {
		return Reading{}, err
	}
	peak, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return Reading{}, fmt.Errorf("memory.peak: %w", err)
	}
	r := Reading{Peak: peak, Max: "unknown"}
	if raw, err := os.ReadFile(filepath.Join(dir, "memory.max")); err == nil {
		r.Max = strings.TrimSpace(string(raw))
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "memory.events")); err == nil {
		for _, ln := range strings.Split(string(raw), "\n") {
			if n, ok := strings.CutPrefix(ln, "oom_kill "); ok {
				r.OOMKill, _ = strconv.ParseInt(strings.TrimSpace(n), 10, 64) // a counter that does not parse reads as none
			}
		}
	}
	return r, nil
}

// due says whether now is worth a line after last: the first reading, a new
// GiB of peak, or a changed OOM-kill count.
func due(last, now Reading, printed bool) bool {
	return !printed || now.Peak>>30 > last.Peak>>30 || now.OOMKill != last.OOMKill
}

// Command is the command in execmem's arguments, after an optional "--".
func Command(args []string) []string {
	if len(args) > 0 && args[0] == "--" {
		return args[1:]
	}
	return args
}

// Run runs args with the process's own stdin, stdout and stderr, prints
// dir's readings to out every interval while it runs, and answers its exit
// code: the tool's own, 128 plus the signal that ended it, 127 when it did not
// start, 2 when there is nothing to run.
func Run(args []string, dir string, every time.Duration, out io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(out, "usage: execmem -- <command> [args...]")
		return 2
	}
	var last Reading
	printed := false
	sample := func() {
		now, err := Read(dir)
		if err != nil || !due(last, now, printed) {
			return
		}
		fmt.Fprintln(out, now.Line())
		last, printed = now, true
	}
	sample()

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(out, "%s could not start %s: %v\n", Prefix, args[0], err)
		return 127
	}
	// The sampler is Run's: it stops when the command has been waited for,
	// and Run waits for it before the last reading, so out is never written
	// from two goroutines at once.
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				sample()
			}
		}
	}()
	err := cmd.Wait()
	close(stop)
	<-stopped
	sample()
	return exitCode(err)
}

// exitCode is a waited command's code as a shell would report it.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return 1
	}
	if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return exit.ExitCode()
}
