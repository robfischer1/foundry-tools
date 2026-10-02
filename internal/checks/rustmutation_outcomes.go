package checks

// Reading what a cargo mutants run printed and listed, for RustMutationVerdict.

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// rustSummaryLine is cargo-mutants' closing count, `58 mutants tested in 6m: 1
// missed, 51 caught, 5 unviable, 1 timeouts`.
var rustSummary = regexp.MustCompile(`^\d+ mutants? tested in .*$`)

func rustSummaryLine(log string) string {
	for _, ln := range strings.Split(log, "\n") {
		if m := rustSummary.FindString(strings.TrimSpace(ln)); m != "" {
			return m
		}
	}
	return ""
}

// boundedTail is the last n lines of s, and at most limit bytes of them, cut
// at a rune boundary from the front: a tool's failure is at its end.
func boundedTail(s string, n, limit int) string {
	t := tail(s, n)
	if len(t) <= limit {
		return t
	}
	from := len(t) - limit
	for from < len(t) && !utf8.RuneStart(t[from]) {
		from++
	}
	return "…" + t[from:]
}

// rustMutantLine is one line of a mutants.out outcome list:
// `crates/anvil/src/inotify.rs:148:5: replace poll_ms -> i32 with -1`.
var rustMutantLine = regexp.MustCompile(`^(\S+?):(\d+)(?::\d+)?: (.+)$`)

// rustMutantFindings is one finding per line of an outcome list. The subject
// is file:line, as every mutation finding's is (mutantFinding); the mutation
// itself leads the detail.
func rustMutantFindings(list, verdict, cause string, why func(name string) string) []Finding {
	var out []Finding
	for _, ln := range strings.Split(list, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		subject, mutation := ln, ln
		if m := rustMutantLine.FindStringSubmatch(ln); m != nil {
			subject, mutation = m[1]+":"+m[2], m[3]
		}
		out = append(out, Finding{Verdict: verdict, Subject: subject, Cause: cause, Detail: mutation + " — " + why(ln), Probe: "rust:mutation"})
	}
	return out
}

// rustTimeoutLine is cargo-mutants' progress line for a hang:
// `TIMEOUT  crates/anvil/src/inotify.rs:148:5: replace poll_ms -> i32 with -1 in 0s build + 60s test`.
var rustTimeoutLine = regexp.MustCompile(`^TIMEOUT\s+(.+) in \S+ build \+ (\S+) test\s*$`)

// rustTimeoutTimes maps each timed-out mutant, as its outcome list names it,
// to the test time its own line reports — the timeout it hit.
func rustTimeoutTimes(log string) map[string]string {
	out := map[string]string{}
	for _, ln := range strings.Split(log, "\n") {
		if m := rustTimeoutLine.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			out[m[1]] = m[2]
		}
	}
	return out
}
