// What an atom FOUND, as structure rather than as prose.
//
// An atom said two things and the gap between them was the whole problem: a
// VERDICT (three states, one word) and its LINES (prose, and the thing a reader
// has to parse to learn anything). Every consumer closed that gap by re-parsing
// the lines — a session reading a transcript, a person reading a scroll — and
// paid for it again on every read.
//
// THE PARSING HAPPENS ONCE, HERE, IN THE MODULE THAT ALREADY HAS THE RAW OUTPUT.
// That is not a contradiction of "stop re-parsing tool output": the point was
// never that parsing is wrong, it is that parsing REPEATEDLY, downstream, from a
// lossy transcript, by every reader, is. The lane has the exact bytes the tool
// wrote, in the container that ran it, with the atom's identity in hand. It is
// the only place the work can be done once and kept.
//
// THE SHAPE IS foundry-dies' schema/findings.schema.json AND NOTHING ELSE. The
// fleet derived that shape four times and reconciled it once; a fifth derivation
// here would be the fifth. `$defs/finding` requires verdict and subject, allows
// cause, detail and probe, and forbids the rest.
//
// AN ATOM WHOSE FORMAT IS NOT RECOGNISED EMITS NOTHING, and that is deliberate.
// Sixty-seven atoms run tools with sixty-seven output formats, and a parser that
// guessed would put fabricated subjects in a record the door stores as fact. No
// findings is an honest answer the reader already handles — ci_logs depth 4 says
// "this lane does not emit findings yet" and names the lane. Expansion is
// expected; invention is not.
package checks

import (
	"regexp"
	"strings"
)

// Finding is one of an atom's findings, in the schema's shape.
//
// NO JSON TAGS, like every other type this module marshals into the run record:
// the wire carries Go's exported field names verbatim, and ourea's RecordFinding
// mirrors them. A tag here would silently rename a field on the wire and the
// door would read zeroes forever — the `original_bytes` scar, one type over.
type Finding struct {
	// Verdict is the schema's lattice word: holds, drifted, violated, excluded,
	// unanalyzable, inert.
	Verdict string
	// Subject is what a reader has to act on — a test's name, a file and line.
	Subject string
	// Cause is a slug a machine can group on. It is what makes a findings
	// answer cheaper than the lines: forty findings with one cause read as one
	// sentence and a count.
	Cause string
	// Detail is one human sentence.
	Detail string
	// Probe names what produced the finding — the atom's own id.
	Probe string
}

// findingCap bounds how many findings one atom contributes.
//
// A RUNAWAY SUITE MUST NOT BECOME A RUNAWAY RECORD. The record travels on ONE
// line of stdout and the door reads it whole, so an atom that found ten thousand
// things would make the record the very payload this feature exists to shrink.
// The cap is generous against real runs — the largest failing gate measured
// tonight carried one finding — and when it bites the atom says so with an
// `excluded` finding of its own rather than silently keeping the first N.
const findingCap = 200

// FindingsOf extracts an atom's findings from its raw output.
//
// DISPATCH ON THE ATOM, NOT ON THE OUTPUT'S SHAPE. Sniffing the text would make
// one tool's output readable as another's — a Go test failure line and a Python
// traceback both contain `file:line` — and a misattributed finding is worse than
// none, because the record is stored as fact.
func FindingsOf(atomID, output string) []Finding {
	if strings.TrimSpace(output) == "" {
		return nil
	}
	var found []Finding
	switch atomID {
	case "go:test", "go:test-race":
		found = goTestFindings(output, atomID)
	case "go:vet", "go:staticcheck":
		found = positionalFindings(output, atomID)
	default:
		// Recognised nowhere: no findings, and depth 4 says which lane.
		return nil
	}
	return capFindings(found, atomID)
}

// capFindings bounds the list and SAYS SO when it cut, with a finding of its own.
//
// An `excluded` verdict with a named cause is the schema's own way to say "this
// was set aside deliberately" — and the schema is explicit that an exclusion
// nobody can read is a suppression, which is why the cut is a finding rather
// than a silent truncation.
func capFindings(found []Finding, atomID string) []Finding {
	if len(found) <= findingCap {
		return found
	}
	kept := append([]Finding(nil), found[:findingCap]...)
	return append(kept, Finding{
		Verdict: "excluded",
		Subject: atomID,
		Cause:   "finding-cap",
		Detail:  "this atom found more than the record carries; the rest are in its lines",
		Probe:   atomID,
	})
}

// goFailLine matches `go test`'s failure header: `--- FAIL: TestName (0.35s)`,
// at any indentation, because a subtest's is indented under its parent.
//
// MEASURED, NOT GUESSED. This session's own gate produced
// `--- FAIL: TestThePollerLingersAndThenStops (0.35s)` and the indented detail
// line beneath it; both shapes are in the test.
var goFailLine = regexp.MustCompile(`^\s*--- FAIL: (\S+)`)

// goTestFindings reads `go test`'s output: one finding per failing test, with
// the first line of its own output as the detail.
//
// THE SUBJECT IS THE TEST'S NAME, which is the thing a reader acts on — they run
// it again. The cause is `test-failed` for all of them, because `go test` does
// not say WHY in a form anything can group on; a parser that tried to classify
// an arbitrary assertion message would be inventing the one field that is
// supposed to be reliable.
func goTestFindings(output, atomID string) []Finding {
	var out []Finding
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		m := goFailLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		f := Finding{Verdict: "violated", Subject: m[1], Cause: "test-failed", Probe: atomID}
		// The detail is the next line that is INDENTED and not itself a FAIL
		// header — go test indents a test's own output beneath its result.
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "" {
				continue
			}
			// UNINDENTED MEANS THE TEST'S OWN OUTPUT IS OVER — the next
			// package line, the bare `FAIL`, the next test's header. Asked as
			// one question rather than two byte comparisons: a line that is
			// unchanged by stripping its indent never had any. The two-operand
			// form also read `next[0]` for its bounds safety off the blank
			// check above, which is a coupling nothing stated.
			if next == strings.TrimLeft(next, " \t") {
				break
			}
			if goFailLine.MatchString(next) {
				break
			}
			f.Detail = strings.TrimSpace(next)
			break
		}
		out = append(out, f)
	}
	return out
}

// positionalLine matches the `file:line[:col]: message` family — the shape go
// vet and staticcheck both use.
//
// MEASURED: this session's gate produced
// `internal/verbs/cilogs_runs.go:132:15: func Deps.runSetSummary is unused (U1000)`.
var positionalLine = regexp.MustCompile(`^(\S+?\.go):(\d+)(?::(\d+))?: (.+)$`)

// checkCode matches a trailing `(U1000)` — staticcheck names its check there,
// and that name is a far better grouping key than the prose before it.
var checkCode = regexp.MustCompile(`\(([A-Z]+\d+)\)\s*$`)

// positionalFindings reads the `file:line:col: message` family.
//
// THE CAUSE IS THE CHECK'S OWN CODE WHERE THERE IS ONE. staticcheck writes
// `(U1000)` at the end of every message, and `U1000` is exactly "a slug a machine
// can group on" — thirty unused functions become `30 × U1000` instead of thirty
// sentences. Without a code the cause is the atom's id, which still groups the
// findings by the tool that found them rather than leaving them uncategorised.
func positionalFindings(output, atomID string) []Finding {
	var out []Finding
	for _, line := range strings.Split(output, "\n") {
		m := positionalLine.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		file, lineNo, msg := m[1], m[2], m[4]
		cause := atomID
		if code := checkCode.FindStringSubmatch(msg); code != nil {
			cause = code[1]
		}
		out = append(out, Finding{
			Verdict: "violated",
			Subject: file + ":" + lineNo,
			Cause:   cause,
			Detail:  msg,
			Probe:   atomID,
		})
	}
	return out
}
