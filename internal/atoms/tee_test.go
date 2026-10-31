package atoms

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// chanSink is a teeSink that reports each write on a channel.
type chanSink struct {
	mu    sync.Mutex
	lines chan string
	all   []string
}

func (s *chanSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.all = append(s.all, string(p))
	s.mu.Unlock()
	select {
	case s.lines <- string(p):
	default: // nobody is watching the channel; all has it
	}
	return len(p), nil
}

func sinkFor(t *testing.T) *chanSink {
	t.Helper()
	s := &chanSink{lines: make(chan string, 64)}
	old := teeSink
	teeSink = s
	t.Cleanup(func() { teeSink = old })
	return s
}

// A TEED PROGRAM'S FIRST LINE IS IN THE LOG BEFORE THE PROGRAM EXITS. go:mutation's
// pod log was empty for the hour because RunProgram held every byte until the end.
func TestTeeShowsALineBeforeTheProgramExits(t *testing.T) {
	sink := sinkFor(t)
	type result struct {
		out  string
		code int
	}
	done := make(chan result, 1)
	go func() {
		out, code := RunProgram(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "echo first; sleep 1; echo second"}, Tee: true, Both: true})
		done <- result{out, code}
	}()
	select {
	case got := <-sink.lines:
		if got != "sh: first\n" {
			t.Errorf("first teed write %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing teed within 5s")
	}
	select {
	case r := <-done:
		t.Fatalf("the program had already exited when its first line was seen: %+v", r)
	default:
	}
	r := <-done
	if r.code != 0 || r.out != "first\nsecond\n" {
		t.Errorf("the answer changed: %q, %d", r.out, r.code)
	}
}

// The tee is a copy: the answer is what it was without it, on every shape of Cmd,
// and a Cmd without Tee writes nothing to the log.
func TestTeeLeavesTheAnswerAsItWas(t *testing.T) {
	script := "echo out; echo err >&2; printf tail; exit 3"
	for name, c := range map[string]Cmd{"default": {}, "both": {Both: true}, "stdout only": {StdoutOnly: true}} {
		t.Run(name, func(t *testing.T) {
			sinkFor(t)
			c.Name, c.Args = "sh", []string{"-c", script}
			want, wantCode := RunProgram(context.Background(), c)
			c.Tee = true
			got, gotCode := RunProgram(context.Background(), c)
			if got != want || gotCode != wantCode || gotCode != 3 {
				t.Errorf("tee %q,%d; plain %q,%d", got, gotCode, want, wantCode)
			}
		})
	}
	t.Run("separate stderr", func(t *testing.T) {
		sinkFor(t)
		var plain, teed string
		c := Cmd{Name: "sh", Args: []string{"-c", script}, Stderr: &plain}
		want, _ := RunProgram(context.Background(), c)
		c.Tee, c.Stderr = true, &teed
		got, _ := RunProgram(context.Background(), c)
		if got != want || teed != plain {
			t.Errorf("stdout %q vs %q, stderr %q vs %q", got, want, teed, plain)
		}
	})
	t.Run("off", func(t *testing.T) {
		sink := sinkFor(t)
		RunProgram(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "echo quiet"}})
		if len(sink.all) != 0 {
			t.Errorf("an untee'd program wrote %q", sink.all)
		}
	})
}

// Concurrent programs never interleave inside a line, an unfinished last line is
// flushed at exit, and a line without a newline is bounded.
func TestTeeWritesWholeLines(t *testing.T) {
	sink := sinkFor(t)
	var wg sync.WaitGroup
	for _, name := range []string{"a", "b", "c", "d"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			RunProgram(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "i=0; while [ $i -lt 50 ]; do echo " + name + "$i-" + strings.Repeat("x", 40) + "; i=$((i+1)); done; printf unfinished"}, Tee: true})
		}()
	}
	wg.Wait()
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.all) != 4*51 {
		t.Fatalf("%d writes, want %d", len(sink.all), 4*51)
	}
	flushed := 0
	for _, w := range sink.all {
		if !strings.HasPrefix(w, "sh: ") || strings.Count(w, "\n") != 1 || !strings.HasSuffix(w, "\n") {
			t.Fatalf("not a whole line: %q", w)
		}
		if w == "sh: unfinished\n" {
			flushed++
		}
	}
	if flushed != 4 {
		t.Errorf("%d unfinished lines flushed, want 4", flushed)
	}
}

func TestTeeBoundsALineWithoutANewline(t *testing.T) {
	sink := sinkFor(t)
	tee := newLineTee("p")
	big := strings.Repeat("y", teeLineMax*2+5)
	if n, err := tee.Write([]byte(big)); n != len(big) || err != nil {
		t.Fatalf("write %d, %v", n, err)
	}
	if len(tee.part) >= teeLineMax {
		t.Errorf("held %d bytes", len(tee.part))
	}
	tee.Flush()
	total := 0
	close(sink.lines)
	for l := range sink.lines {
		total += len(l) - len("p: \n")
	}
	if total != len(big) {
		t.Errorf("teed %d bytes of %d", total, len(big))
	}
}
