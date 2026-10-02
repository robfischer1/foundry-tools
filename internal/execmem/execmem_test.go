package execmem

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// cgroup writes a cgroup v2 directory's three memory files as the kernel
// shows them inside an exec (measured 2026-10-02 in a cluster engine exec:
// memory.peak "12644352", memory.max "max", memory.events with oom_kill).
func cgroup(t *testing.T, peak, max string, oomKill string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"memory.peak":   peak + "\n",
		"memory.max":    max + "\n",
		"memory.events": "low 0\nhigh 0\nmax 3\noom 1\noom_kill " + oomKill + "\noom_group_kill 0\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRead(t *testing.T) {
	got, err := Read(cgroup(t, "17179869184", "17179869184", "1"))
	if err != nil || got != (Reading{Peak: 17179869184, Max: "17179869184", OOMKill: 1}) {
		t.Fatalf("%+v %v", got, err)
	}
	if got.Line() != "exec-memory: peak_bytes=17179869184 memory_max=17179869184 oom_kill=1" {
		t.Errorf("%q", got.Line())
	}
	for name, dir := range map[string]string{
		"no cgroup":       t.TempDir(),
		"peak not a size": cgroup(t, "lots", "max", "0"),
	} {
		if _, err := Read(dir); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	// A memory.events without the counter (an older kernel) and an unreadable
	// memory.max are partial readings, not failures: the peak is the point.
	dir := cgroup(t, "5", "max", "0")
	if err := os.WriteFile(filepath.Join(dir, "memory.events"), []byte("low 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "memory.max")); err != nil {
		t.Fatal(err)
	}
	if got, err := Read(dir); err != nil || got != (Reading{Peak: 5, Max: "unknown", OOMKill: 0}) {
		t.Errorf("%+v %v", got, err)
	}
}

// A line is printed for the first reading, every new GiB of peak, and every
// change of the OOM-kill count — a few lines a run, never one a second.
func TestDue(t *testing.T) {
	const gib = 1 << 30
	for _, c := range []struct {
		name      string
		last, now Reading
		printed   bool
		want      bool
	}{
		{"the first reading", Reading{}, Reading{Peak: 10}, false, true},
		{"nothing new", Reading{Peak: gib + 1}, Reading{Peak: 2*gib - 1}, true, false},
		{"a new GiB", Reading{Peak: gib + 1}, Reading{Peak: 2 * gib}, true, true},
		{"the same GiB exactly", Reading{Peak: 2 * gib}, Reading{Peak: 2 * gib}, true, false},
		{"an OOM kill", Reading{Peak: 5}, Reading{Peak: 5, OOMKill: 1}, true, true},
	} {
		if got := due(c.last, c.now, c.printed); got != c.want {
			t.Errorf("%s: due = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRun(t *testing.T) {
	dir := cgroup(t, "4096", "max", "0")
	for _, c := range []struct {
		name string
		args []string
		code int
		has  string
	}{
		{"a clean exit", []string{"true"}, 0, ""},
		{"the tool's own code", []string{"sh", "-c", "exit 3"}, 3, ""},
		{"a signal is 128 plus it", []string{"sh", "-c", "kill -9 $$"}, 137, ""},
		{"a command that does not start", []string{"/no/such/tool"}, 127, "exec-memory: could not start /no/such/tool: "},
		{"no command", nil, 2, "usage: execmem -- <command> [args...]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			code := Run(c.args, dir, time.Hour, &out)
			if code != c.code {
				t.Errorf("code %d, want %d", code, c.code)
			}
			if !strings.Contains(out.String(), c.has) {
				t.Errorf("output lacks %q:\n%s", c.has, out.String())
			}
			if c.code != 127 && c.code != 2 && out.String() != "exec-memory: peak_bytes=4096 memory_max=max oom_kill=0\n" {
				t.Errorf("one first reading and nothing else for a run shorter than the interval:\n%q", out.String())
			}
		})
	}
}

// The sampler prints while the command runs — the reading that survives a
// kill of the whole exec is the last one printed before it.
func TestRunSamplesWhileTheCommandRuns(t *testing.T) {
	dir := cgroup(t, "1", "max", "0")
	var out syncBuffer
	done := make(chan int)
	go func() { done <- Run([]string{"sleep", "1"}, dir, 10*time.Millisecond, &out) }()
	deadline := time.After(5 * time.Second)
	for !strings.Contains(out.String(), "peak_bytes=1 ") {
		select {
		case <-deadline:
			t.Fatalf("no reading while the command ran:\n%s", out.String())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "memory.events"), []byte("oom_kill 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := <-done; code != 0 {
		t.Fatalf("code %d", code)
	}
	if got := out.String(); !strings.Contains(got, "oom_kill=1\n") || strings.Count(got, "\n") > 3 {
		t.Errorf("an OOM kill mid-run is printed, and nothing is printed twice:\n%s", got)
	}
}

// A cgroup with no memory files prints nothing, and the command still runs.
func TestRunWithoutACgroup(t *testing.T) {
	var out bytes.Buffer
	if code := Run([]string{"sh", "-c", "exit 4"}, t.TempDir(), time.Hour, &out); code != 4 || out.Len() != 0 {
		t.Errorf("code %d, output %q", code, out.String())
	}
}

// The reading after the command ends is taken too: a change the sampler never
// ticked over is still printed.
func TestRunReadsOnceMoreAtTheEnd(t *testing.T) {
	dir := cgroup(t, "1", "max", "0")
	var out bytes.Buffer
	code := Run([]string{"sh", "-c", `printf 'oom_kill 1\n' > "$0/memory.events"`, dir}, dir, time.Hour, &out)
	if code != 0 || out.String() != "exec-memory: peak_bytes=1 memory_max=max oom_kill=0\nexec-memory: peak_bytes=1 memory_max=max oom_kill=1\n" {
		t.Errorf("code %d:\n%s", code, out.String())
	}
}

func TestExitCode(t *testing.T) {
	if exitCode(nil) != 0 || exitCode(os.ErrClosed) != 1 {
		t.Error("nil is 0; an error that is not an exit is 1")
	}
}

func TestCommand(t *testing.T) {
	if got := Command([]string{"--", "a", "b"}); strings.Join(got, " ") != "a b" {
		t.Errorf("%q", got)
	}
	if got := Command([]string{"a"}); strings.Join(got, " ") != "a" {
		t.Errorf("no separator: %q", got)
	}
	if got := Command([]string{"--"}); len(got) != 0 {
		t.Errorf("%q", got)
	}
}

// syncBuffer is a bytes.Buffer a test can read while Run's sampler writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
