package checks

import (
	"sort"
	"strings"
)

// The reads a scan over named files shares, with the other helpers two or more
// atoms use (scan.go).

// ReadAll reads every named file through read. A file that would not read is
// the answer: the first of them, sorted, with why, since a file never read is a
// file never judged.
func ReadAll(read func(string) (string, error), paths []string) (map[string]string, string) {
	out := make(map[string]string, len(paths))
	var failed []string
	for _, p := range paths {
		body, err := read(p)
		if err != nil {
			failed = append(failed, p+" ("+err.Error()+")")
			continue
		}
		out[p] = body
	}
	if len(failed) == 0 {
		return out, ""
	}
	sort.Strings(failed)
	return nil, failed[0] + " would not read"
}

// WithSuffix is the files whose name ends in one of the suffixes, in order.
func WithSuffix(files []string, suffixes ...string) []string {
	var out []string
	for _, f := range files {
		for _, s := range suffixes {
			if strings.HasSuffix(f, s) {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// AbsentRuleset answers for a tree with no rules/sast, given the contents of its
// copier answers file ("" when it has none or it would not read).
//
// ABSENCE IS READ AGAINST INTENT: a tree nobody stamped may legitimately have no
// SAST and passes; one of the fleet's templates stamped it, so it is supposed to
// carry the ruleset that template ships, and its absence is a scan that did not
// happen (2).
func AbsentRuleset(a AtomDef, answers string) Verdict {
	tpl := CopierTemplate(answers)
	if tpl == "" {
		return VerdictOf(a, int(StatePass), a.ID+
			": ABSENT - no rules/sast in this tree, and no SAST-shipping fleet template stamped it")
	}
	return VerdictOf(a, int(StateCannotRun), SastAbsentStamped(a.ID, tpl))
}
