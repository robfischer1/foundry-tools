package atoms

import (
	"bytes"
	"io"
	"os"
	"sync"
)

// teeSink is where a Cmd with Tee set copies its program's output as it arrives:
// the atom's own stderr, which is the log an operator reads while the atom runs.
// A variable so a test can say where it goes.
var teeSink io.Writer = os.Stderr

// teeMu serialises whole lines into teeSink, so two programs running at once
// (go:mutation runs several modules' controls and the mutation itself) never
// interleave inside a line.
var teeMu sync.Mutex

// teeLineMax bounds what a lineTee holds for one unfinished line: a program that
// prints megabytes without a newline is flushed in pieces, not held.
const teeLineMax = 64 << 10

// lineTee is an io.Writer that hands teeSink the lines written to it, each
// whole and prefixed with the program's name. It keeps only the unfinished tail
// of the last write, never the output: the Cmd's own buffers stay the answer.
type lineTee struct {
	prefix string
	part   []byte
}

func newLineTee(name string) *lineTee { return &lineTee{prefix: name + ": "} }

// Write never fails and never short-writes: a broken log must not fail the run
// it is only watching.
func (t *lineTee) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			t.part = append(t.part, p...)
			if len(t.part) >= teeLineMax {
				t.emit(t.part, true)
				t.part = t.part[:0]
			}
			break
		}
		t.part = append(t.part, p[:i]...)
		t.emit(t.part, true)
		t.part = t.part[:0]
		p = p[i+1:]
	}
	return n, nil
}

// Flush writes the unfinished line, once the program has ended.
func (t *lineTee) Flush() {
	if len(t.part) > 0 {
		t.emit(t.part, true)
		t.part = t.part[:0]
	}
}

func (t *lineTee) emit(line []byte, nl bool) {
	buf := make([]byte, 0, len(t.prefix)+len(line)+1)
	buf = append(append(buf, t.prefix...), line...)
	if nl {
		buf = append(buf, '\n')
	}
	teeMu.Lock()
	defer teeMu.Unlock()
	_, _ = teeSink.Write(buf)
}
