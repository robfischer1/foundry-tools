package checks

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ReasonCap is the most of an atom's raw output its Reason carries. The whole
// output is in Logs (itself capped at AtomLogCap); Reason is the sentence a
// reader sees first, and an unbounded one broke the record: a python:pytest atom
// on stellar-core-go 6b986c9 wrote 23,047,351 bytes of Reason and the settle
// blew the 4 MiB post cap.
const ReasonCap = 64 << 10

// reasonHead is the share of ReasonCap taken from the start of the output; the
// rest is the tail. A tool that failed explains itself at the end (the same
// reason CaptureLogs keeps the tail), but a structured summary opens with its
// tables, so a head survives too.
const reasonHead = 16 << 10

// BoundOutput answers the output unchanged when it fits ReasonCap, and
// otherwise its head and its tail, cut on line boundaries, with a marker where
// the middle was and how many bytes it held. Atoms that print a structured
// summary (a mutation run's tables and survivors) print little beyond it, so
// the summary is what survives; only raw tool output is trimmed.
func BoundOutput(output string) string {
	if len(output) <= ReasonCap {
		return output
	}
	head := output[:reasonHead]
	if i := strings.LastIndexByte(head, '\n'); i >= 0 {
		head = head[:i+1]
	}
	// A cut with no newline to land on can still sit inside a rune: back off it.
	for len(head) > 0 && !utf8.RuneStart(output[len(head)]) {
		head = head[:len(head)-1]
	}
	tail := output[len(output)-(ReasonCap-reasonHead):]
	if i := strings.IndexByte(tail, '\n'); i >= 0 {
		tail = tail[i+1:]
	}
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	elided := len(output) - len(head) - len(tail)
	return fmt.Sprintf("%s… %d bytes elided — full output in Logs/artifact …\n%s", head, elided, tail)
}
