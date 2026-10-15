package execmem

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
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
	if err != nil || got != (Reading{Peak: 17179869184, Max: "17179869184", OOMKill: 1, Anon: -1, AnonAtPeak: -1}) {
		t.Fatalf("%+v %v", got, err)
	}
	if got.Line() != "exec-memory: peak_bytes=17179869184 memory_max=17179869184 oom_kill=1" {
		t.Errorf("%q", got.Line())
	}
	if _, err := Read(t.TempDir()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("no cgroup: %v", err)
	}
	if _, err := Read(cgroup(t, "lots", "max", "0")); !errors.Is(err, strconv.ErrSyntax) || !strings.HasPrefix(err.Error(), "memory.peak: ") {
		t.Errorf("peak not a size: %v", err)
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
	if got, err := Read(dir); err != nil || got != (Reading{Peak: 5, Max: "unknown", OOMKill: 0, Anon: -1, AnonAtPeak: -1}) {
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
// kill of the whole exec is the last one printed before it. The command
// waits for the test to release it, so a reading the test sees first can only
// have come from a tick.
func TestRunSamplesWhileTheCommandRuns(t *testing.T) {
	dir := cgroup(t, "1", "max", "0")
	var out syncBuffer
	done := make(chan int)
	release := filepath.Join(dir, "release")
	go func() {
		done <- Run([]string{"sh", "-c", `while [ ! -e "$0" ]; do sleep 0.01; done`, release}, dir, 10*time.Millisecond, &out)
	}()
	waitFor := func(line string) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for !strings.Contains(out.String(), line) {
			select {
			case <-deadline:
				t.Fatalf("no %q while the command ran:\n%s", line, out.String())
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	waitFor("oom_kill=0\n") // the first reading, before the command starts
	if err := os.WriteFile(filepath.Join(dir, "memory.events"), []byte("oom_kill 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor("oom_kill=1\n")
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := <-done; code != 0 {
		t.Fatalf("code %d", code)
	}
	if got := out.String(); strings.Count(got, "oom_kill=1\n") != 1 {
		t.Errorf("nothing is printed twice:\n%s", got)
	}
}

// A reading that is not due leaves the tracker as it was: what was printed
// stays printed, so the same reading is never printed twice.
func TestTrackerSample(t *testing.T) {
	dir := cgroup(t, "7", "max", "0")
	var out bytes.Buffer
	first := tracker{}.sample(dir, &out)
	seven := Reading{Peak: 7, Max: "max", Anon: -1, AnonAtPeak: -1}
	if first != (tracker{last: seven, shown: seven, printed: true}) {
		t.Fatalf("%+v", first)
	}
	if again := first.sample(dir, &out); again != first {
		t.Errorf("not due, and the tracker moved: %+v", again)
	}
	if none := (tracker{}).sample(t.TempDir(), &out); none != (tracker{}) {
		t.Errorf("an unreadable cgroup moved the tracker: %+v", none)
	}
	if out.String() != "exec-memory: peak_bytes=7 memory_max=max oom_kill=0\n" {
		t.Errorf("%q", out.String())
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
	for _, args := range [][]string{{"--"}, nil, {}} {
		if got := Command(args); len(got) != 0 {
			t.Errorf("Command(%q) = %q", args, got)
		}
	}
}

func TestMainRunsTheCommand(t *testing.T) {
	var out bytes.Buffer
	if code := Main([]string{"--"}, &out); code != 2 || out.String() != "usage: execmem -- <command> [args...]\n" {
		t.Errorf("code %d: %q", code, out.String())
	}
	if code := Main([]string{"--", "sh", "-c", "exit 5"}, io.Discard); code != 5 {
		t.Errorf("code %d", code)
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

// writeStat lays memory.stat as the kernel shows it: anon beside file, the
// page cache that memory.peak counts and an OOM kill does not.
func writeStat(t *testing.T, dir string, anon, file int) {
	t.Helper()
	body := "anon " + strconv.Itoa(anon) + "\nfile " + strconv.Itoa(file) + "\nkernel 4096\n"
	if err := os.WriteFile(filepath.Join(dir, "memory.stat"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The anon on the line is memory.stat's anon at the reading that raised the
// peak, not the page cache that the peak includes, and not a later, smaller
// figure once the process shrank.
func TestAnonAtPeak(t *testing.T) {
	const gib = 1 << 30
	dir := cgroup(t, strconv.Itoa(11*gib), "max", "0")
	writeStat(t, dir, 3*gib, 8*gib)
	got, err := Read(dir)
	if err != nil || got.Anon != 3*gib || got.AnonAtPeak != 3*gib {
		t.Fatalf("%+v %v", got, err)
	}
	if want := "exec-memory: peak_bytes=11811160064 memory_max=max oom_kill=0 anon_bytes=3221225472"; got.Line() != want {
		t.Errorf("%q, want %q", got.Line(), want)
	}

	var out bytes.Buffer
	tr := tracker{}.sample(dir, &out)
	// The peak rises within the same GiB, with more anon: nothing printed yet.
	if err := os.WriteFile(filepath.Join(dir, "memory.peak"), []byte(strconv.Itoa(11*gib+4096)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeStat(t, dir, 4*gib, 7*gib)
	tr = tr.sample(dir, &out)
	// The process shrinks; the peak holds. The peak's anon stays the larger.
	writeStat(t, dir, gib, 7*gib)
	tr = tr.sample(dir, &out)
	if tr.last.AnonAtPeak != 4*gib {
		t.Errorf("anon at peak %d, want %d", tr.last.AnonAtPeak, 4*gib)
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Errorf("only the first reading is due:\n%s", out.String())
	}
	// The run's end prints the final peak and its anon.
	tr.flush(&out)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if want := "exec-memory: peak_bytes=" + strconv.Itoa(11*gib+4096) + " memory_max=max oom_kill=0 anon_bytes=" + strconv.Itoa(4*gib); len(lines) != 2 || lines[1] != want {
		t.Errorf("flush: %q, want last %q", lines, want)
	}
	// No memory.stat: no anon on the line, which is the old line exactly.
	if line := (Reading{Peak: 1, Max: "max", Anon: -1, AnonAtPeak: -1}).Line(); strings.Contains(line, "anon") {
		t.Errorf("%q", line)
	}
}
