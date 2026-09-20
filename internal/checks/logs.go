package checks

import (
	"strings"
	"unicode/utf8"
)

// AtomLogCap is the most output ONE atom may carry into the record. Roughly
// three times the largest atom measured across six real runs (`cargo test`,
// 115 KB), so the cap is a guard against a runaway, not a budget the ordinary
// case has to live inside — the median atom measured 82–89 bytes.
const AtomLogCap = 1 << 20

// CaptureLogs takes an atom's raw output and answers the lines it carries,
// whether the cap bit, and HOW BIG THE OUTPUT REALLY WAS.
//
// originalBytes IS THE TRUE SIZE, NOT THE CARRIED SIZE, and that is the whole
// point of returning it. When truncated is false the two coincide; when it is
// true they must not, and the difference is exactly what was dropped. A reader
// can then always answer "how much am I not seeing" from the result alone —
// which is precisely what Loki's `max_line_size_truncate: true` does not
// provide, and why a repo's file list was being cut mid-path with nothing
// saying so (infra #10719). A cut this code makes says so.
//
// THE TAIL SURVIVES, NOT THE HEAD. A tool that failed explains itself at the
// end; the start of its output is setup noise. Keeping the first megabyte of a
// runaway would reliably keep the least useful megabyte.
func CaptureLogs(output string) (logs []string, truncated bool, originalBytes int) {
	originalBytes = len(output)
	if originalBytes == 0 {
		// EXPLICITLY EMPTY, NEVER NIL. "This atom printed nothing" and "this
		// field was never set" are different facts, and a nil slice marshals
		// to null where an empty one marshals to []. The distinction is the
		// same one the ABSENT result exists to protect one shelf up.
		return []string{}, false, 0
	}
	kept := output
	if originalBytes > AtomLogCap {
		truncated = true
		kept = kept[originalBytes-AtomLogCap:]
		// THE CUT LANDS ON A RUNE BOUNDARY. Slicing by byte severs a
		// multi-byte character, and this repo's fleet already paid for that
		// once: a reason carrying box-drawing characters was sliced mid-rune
		// and Postgres refused the row outright — "invalid byte sequence for
		// encoding UTF8" — so a red settle with a real finding was never
		// written (ourea, measured 2026-09-10). Walk forward off the partial
		// rune rather than hand anyone a broken one.
		//
		// The test is DecodeRuneInString, not ValidString on one byte: the
		// LEAD byte of a perfectly good multi-byte rune is also invalid on
		// its own (0xC3 of "é"), so a one-byte validity walk eats the very
		// characters it is meant to protect. RuneError with size 1 is the
		// only thing that actually means "this byte starts no rune".
		for len(kept) > 0 {
			if r, size := utf8.DecodeRuneInString(kept); r != utf8.RuneError || size > 1 {
				break
			}
			kept = kept[1:]
		}
	}
	if kept == "" {
		// EVERY BYTE WAS UNREADABLE. An atom can emit binary — a core dump, a
		// mis-set encoding — and the rune walk then consumes all of it. Say
		// that honestly: no lines, the cut stated, and originalBytes still
		// naming how much there was. "There were N bytes and I can show you
		// none of them" is a true answer; one empty line pretending to be
		// content is not.
		return []string{}, truncated, originalBytes
	}
	return strings.Split(strings.TrimSuffix(kept, "\n"), "\n"), truncated, originalBytes
}
