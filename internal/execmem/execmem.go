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
// PEAK IS NOT ANON. memory.peak counts the cgroup's page cache: a build that
// reads and writes a few GiB of files shows a few GiB of "peak" that the kernel
// reclaims under pressure and that never contributes to an OOM kill (the 11 GB
// "builds" of a warm target volume were mostly this). So each reading also
// carries anon_bytes, memory.stat's anon at the moment the peak was last
// raised: the memory only the process can give back.
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
	// Anon is memory.stat's anon at the time of this reading, bytes; -1 when
	// memory.stat does not read. AnonAtPeak is the Anon of the reading at which
	// Peak last rose (tracker fills it; Read leaves it equal to Anon).
	Anon       int64
	AnonAtPeak int64
}

// Line is the reading as printed.
func (r Reading) Line() string {
	line := fmt.Sprintf("%s peak_bytes=%d memory_max=%s oom_kill=%d", Prefix, r.Peak, r.Max, r.OOMKill)
	if r.AnonAtPeak >= 0 {
		line += fmt.Sprintf(" anon_bytes=%d", r.AnonAtPeak)
	}
	return line
}

// Read reads dir's memory files. Only memory.peak is required: the others
// read as "unknown", zero and -1 (no anon) when absent.
func Read(dir string) (Reading, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "memory.peak"))
	if err != nil {
		return Reading{}, err
	}
	peak, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return Reading{}, fmt.Errorf("memory.peak: %w", err)
	}
	r := Reading{Peak: peak, Max: "unknown", Anon: -1, AnonAtPeak: -1}
	if raw, err := os.ReadFile(filepath.Join(dir, "memory.stat")); err == nil {
		for _, ln := range strings.Split(string(raw), "\n") {
			if n, ok := strings.CutPrefix(ln, "anon "); ok {
				if v, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64); err == nil {
					r.Anon, r.AnonAtPeak = v, v
				}
			}
		}
	}
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

// Main is execmem's process: the command after "--", the exec's own cgroup,
// a reading every second.
func Main(args []string, out io.Writer) int {
	return Run(Command(args), "/sys/fs/cgroup", time.Second, out)
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
	t := tracker{}.sample(dir, out)

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(out, "%s could not start %s: %v\n", Prefix, args[0], err)
		return 127
	}
	// The sampler is Run's. It owns the tracker while the command runs and
	// hands it back when stopped, so the last reading follows every tick and
	// out is never written from two goroutines at once.
	stop, done := make(chan struct{}), make(chan tracker)
	go func(t tracker) {
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				done <- t
				return
			case <-tick.C:
				t = t.sample(dir, out)
			}
		}
	}(t)
	err := cmd.Wait()
	close(stop)
	end := (<-done).sample(dir, out)
	end.flush(out)
	return exitCode(err)
}

// tracker is what has been read and what has been printed: last is the newest
// reading (peak and its anon carried forward), shown the one on the last line.
type tracker struct {
	last, shown Reading
	printed     bool
}

// sample reads dir and prints the reading when it is due, answering the
// tracker that follows. A cgroup that does not read prints nothing.
func (t tracker) sample(dir string, out io.Writer) tracker {
	now, err := Read(dir)
	if err != nil {
		return t
	}
	// The anon that goes with the peak is the one read when the peak rose; a
	// tick that raised nothing keeps the earlier figure (the process may have
	// shrunk since, and the anon AT THE PEAK is what is asked for).
	if t.printed && now.Peak <= t.last.Peak {
		now.Peak, now.AnonAtPeak = t.last.Peak, t.last.AnonAtPeak
	}
	t.last = now
	if !due(t.shown, now, t.printed) {
		return t
	}
	fmt.Fprintln(out, now.Line())
	t.shown, t.printed = now, true
	return t
}

// flush prints the newest reading when it differs from the last line, so the
// run's final peak and its anon are on record even inside the same GiB.
func (t tracker) flush(out io.Writer) {
	if t.printed && t.last != t.shown {
		fmt.Fprintln(out, t.last.Line())
	}
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
