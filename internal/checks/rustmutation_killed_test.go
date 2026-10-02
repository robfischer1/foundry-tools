package checks

import (
	"strings"
	"testing"
)

// THE KILLED RUNS. Each log is what the engine's ExecError carried for an exec
// that ended in the signal range, which is all that survives one: the exec's
// filesystem — mutants.out — goes with it.
//
// m137Killed is cargo-mutants 27.1.0 (the lane's pin) run with --caught
// --unviable over a five-function crate and SIGKILLed 55s in (2026-10-02):
// 12 of 21 graded, two missed, no summary. Its bytes are the tool's own.
const m137Killed = `Found 21 mutants to test
ok       Unmutated baseline in 0s build + 4s test
 INFO Auto-set test timeout to 21s
caught   src/lib.rs:1:37: replace add -> i32 with 0 in 0s build + 4s test
caught   src/lib.rs:1:37: replace add -> i32 with 1 in 0s build + 4s test
caught   src/lib.rs:1:37: replace add -> i32 with -1 in 0s build + 4s test
caught   src/lib.rs:1:39: replace + with - in add in 0s build + 4s test
caught   src/lib.rs:1:39: replace + with * in add in 0s build + 4s test
caught   src/lib.rs:2:33: replace is_big -> bool with true in 0s build + 4s test
caught   src/lib.rs:2:33: replace is_big -> bool with false in 0s build + 4s test
caught   src/lib.rs:2:35: replace > with == in is_big in 0s build + 4s test
caught   src/lib.rs:2:35: replace > with < in is_big in 0s build + 4s test
caught   src/lib.rs:2:35: replace > with >= in is_big in 0s build + 4s test
MISSED   src/lib.rs:3:34: replace untested -> i32 with 0 in 0s build + 4s test
MISSED   src/lib.rs:3:34: replace untested -> i32 with 1 in 0s build + 4s test`

// m137Summary is the same crate's run to completion — the shape of a kill
// that lands after the summary, during cleanup.
const m137Summary = m137Killed + `
MISSED   src/lib.rs:3:34: replace untested -> i32 with -1 in 0s build + 4s test
MISSED   src/lib.rs:3:36: replace * with + in untested in 0s build + 4s test
MISSED   src/lib.rs:3:36: replace * with / in untested in 0s build + 4s test
caught   src/lib.rs:4:30: replace slow -> u64 with 0 in 0s build + 0s test
caught   src/lib.rs:4:30: replace slow -> u64 with 1 in 0s build + 0s test
caught   src/lib.rs:4:87: replace + with - in slow in 0s build + 4s test
caught   src/lib.rs:4:87: replace + with * in slow in 0s build + 4s test
caught   src/lib.rs:5:27: replace name -> String with String::new() in 0s build + 4s test
caught   src/lib.rs:5:27: replace name -> String with "xyzzy".into() in 0s build + 4s test
21 mutants tested in 85s: 5 missed, 16 caught`

// panelessKilled is paneless@d988b9b's mutation Job forge/mutation-paneless-
// d988b9b-r5zjb (2026-10-02), exit 137 at 11m32s, before the lane printed
// caught mutants: seven survivors named, the rest uncounted. Its own bytes.
const panelessKilled = `Found 100 mutants to test
ok       Unmutated baseline in 33s build + 2s test
 INFO Auto-set test timeout to 60s
MISSED   crates/paneless/src/arrangement.rs:81:9: replace Registered::refusing -> Self with Default::default() in 10s build + 3s test
MISSED   crates/paneless/src/arrangement.rs:82:13: delete field refuse from struct Self expression in Registered::refusing in 8s build + 2s test
MISSED   crates/paneless/src/arrangement.rs:90:9: replace Registered::empty -> Self with Default::default() in 8s build + 2s test
MISSED   crates/paneless/src/arrangement.rs:114:41: replace && with || in <impl Arrangements for Registered>::arrangement_for in 9s build + 5s test
MISSED   crates/paneless/src/fleet.rs:351:9: replace <impl crate::arrangement::Arrangements for Fleet>::arrangement_for -> Result<Option<Arrangement>, String> with Ok(None) in 7s build + 1s test
MISSED   crates/paneless/src/tui/lenses/context.rs:616:9: replace <impl Lens for Context>::governed_by with () in 13s build + 2s test
MISSED   crates/paneless/src/tui/lenses/standing.rs:225:9: replace Panel::governed_by with () in 12s build + 1s test`

// The first 13 lines of m137Killed: the ten caught and nothing else.
var m137AllCaught = strings.Join(strings.Split(m137Killed, "\n")[:13], "\n")

func TestRustMutationKilled(t *testing.T) {
	for _, c := range []struct {
		name     string
		status   int
		log      string
		state    int
		head     string   // the reason's first line, whole
		has      []string // more the reason must say
		findings []string // causes, in order
	}{
		{
			name: "killed mid-stream with survivors is a survivor report", status: 137, log: m137Killed, state: 1,
			head: "cargo mutants was killed (SIGKILL 137) after 12 of 21 mutants, before its summary — 9 were never graded. 2 viable mutant(s) survived the suite before the kill — see the survivor list below",
			has: []string{"| 10 | 2 | 0 | 0 | 83% of 12 viable |", "src/lib.rs:3:34: replace untested -> i32 with 1\n```",
				"the last 20 lines it printed:\n```\nFound 21 mutants to test\n", "```\n\nthe last 20 lines"},
			findings: []string{"mutation-killed", "mutant-missed", "mutant-missed"},
		},
		{
			name: "paneless: seven survivors, no caught lines", status: 137, log: panelessKilled, state: 1,
			head:     "cargo mutants was killed (SIGKILL 137) after 7 of 100 mutants, before its summary — 93 were never graded. 7 viable mutant(s) survived the suite before the kill — see the survivor list below",
			has:      []string{"crates/paneless/src/tui/lenses/standing.rs:225:9: replace Panel::governed_by with ()\n```"},
			findings: []string{"mutation-killed", "mutant-missed", "mutant-missed", "mutant-missed", "mutant-missed", "mutant-missed", "mutant-missed", "mutant-missed"},
		},
		{
			name: "killed after the summary: every outcome is whole", status: 137, log: m137Summary, state: 1,
			head:     "cargo mutants was killed (SIGKILL 137) after 21 of 21 mutants, after its summary, so the outcomes are whole. 5 viable mutant(s) survived the suite — see the survivor list below",
			has:      []string{"| 16 | 5 | 0 | 0 | 76% of 21 viable |"},
			findings: []string{"mutant-missed", "mutant-missed", "mutant-missed", "mutant-missed", "mutant-missed"},
		},
		{
			name: "killed after a clean summary passes", status: 137, state: 0,
			log:  strings.Replace(m137AllCaught, "Found 21", "Found 10", 1) + "\n10 mutants tested in 40s: 10 caught",
			head: "cargo mutants was killed (SIGKILL 137) after 10 of 10 mutants, after its summary, so the outcomes are whole. every viable mutant was caught (10 of 10)",
		},
		{
			name: "everything graded so far was caught: not a pass", status: 137, log: m137AllCaught, state: 2,
			head: "CANNOT RUN - cargo mutants was killed (SIGKILL 137) after 10 of 21 mutants, before its summary — 11 were never graded. Every mutant it graded was caught, and a run that stopped short is not a pass",
			has:  []string{"| 10 | 0 | 0 | 0 | 100% of 10 viable |"},
		},
		{
			name: "nothing graded is could-not-run", status: 137, state: 2,
			log:  "Found 21 mutants to test\nok       Unmutated baseline in 0s build + 4s test\n INFO Auto-set test timeout to 21s",
			head: "CANNOT RUN - cargo mutants was killed (SIGKILL 137) after 0 of 21 mutants, before its summary — 21 were never graded. Nothing was measured",
			has:  []string{"the last 20 lines it printed:\n```\nFound 21 mutants to test"},
		},
		{
			name: "killed in the baseline, before the count", status: 137, state: 2, log: "",
			head: "CANNOT RUN - cargo mutants was killed (SIGKILL 137) after 0 mutants — how many there were is not in what the engine kept. Nothing was measured",
			has:  []string{"it printed nothing"},
		},
		{
			// anvil's transcript cut before its summary, with three caught
			// lines SYNTHESISED in the shape --caught prints them.
			name: "anvil cut before its summary: survivor and timeout", status: 137, state: 1,
			log: strings.Join(strings.Split(anvilLog, "\n")[:5], "\n") +
				"\ncaught   crates/anvil/src/inotify.rs:300:5: replace a in 0s build + 10s test" +
				"\ncaught   crates/anvil/src/inotify.rs:301:5: replace b in 0s build + 10s test" +
				"\ncaught   crates/anvil/src/inotify.rs:302:5: replace c in 0s build + 10s test",
			head:     "cargo mutants was killed (SIGKILL 137) after 5 of 58 mutants, before its summary — 53 were never graded. 1 viable mutant(s) survived the suite before the kill — see the survivor list below; 1 more timed out, counted as caught and listed below",
			findings: []string{"mutation-killed", "mutant-missed", MutantTimeoutCause},
		},
		{
			name: "a hang-dominated partial run keeps the guard", status: 137, state: 2,
			log:  "Found 9 mutants to test\nTIMEOUT  src/a.rs:1:1: replace f with g in 0s build + 60s test",
			head: "CANNOT RUN - cargo mutants was killed (SIGKILL 137) after 1 of 9 mutants, before its summary — 8 were never graded. 1 of 1 viable mutant(s) timed out and 0 were caught by a failing test — a suite or a runner that hangs, not evidence the suite works (could-not-run when timeouts are more than half the viable mutants, or nothing was caught but by timeout)",
		},
		{
			name: "the engine kept only the tail", status: 137, state: 1,
			log:      "[omitting 252511 bytes]...dding\nMISSED   src/a.rs:4:2: replace f with g in 1s build + 1s test",
			head:     "cargo mutants was killed (SIGKILL 137) after 1 mutants — how many there were is not in what the engine kept. 1 viable mutant(s) survived the suite before the kill — see the survivor list below",
			has:      []string{"The engine kept only the end of its output (it omitted 252511 bytes): outcomes printed before that are not in this record."},
			findings: []string{"mutation-killed", "mutant-missed"},
		},
		{
			name: "an unviable mutant was graded too", status: 137, state: 1,
			log:  m137Killed + "\nunviable src/lib.rs:9:1: replace q with r in 2s build",
			head: "cargo mutants was killed (SIGKILL 137) after 13 of 21 mutants, before its summary — 8 were never graded. 2 viable mutant(s) survived the suite before the kill — see the survivor list below",
			has:  []string{"| 10 | 2 | 1 | 0 | 83% of 12 viable |"}, findings: []string{"mutation-killed", "mutant-missed", "mutant-missed"},
		},
		{
			// A summary the engine kept with its head cut off: the outcomes
			// before the cut are not in the record, so they are not whole.
			name: "a kept summary behind a cut is not whole", status: 137, state: 1,
			log:      "[omitting 9 bytes]...xx\nMISSED   src/a.rs:4:2: replace f with g in 1s build + 1s test\n5 mutants tested in 9s: 1 missed, 4 caught",
			head:     "cargo mutants was killed (SIGKILL 137) after 1 mutants — how many there were is not in what the engine kept. 1 viable mutant(s) survived the suite before the kill — see the survivor list below",
			findings: []string{"mutation-killed", "mutant-missed"},
		},
		{
			name: "a count of zero is a count", status: 137, state: 2, log: "Found 0 mutants to test",
			head: "CANNOT RUN - cargo mutants was killed (SIGKILL 137) after 0 of 0 mutants, before its summary — 0 were never graded. Nothing was measured",
		},
		{
			name: "another signal is named as itself", status: 143, state: 2, log: "Found 3 mutants to test",
			head: "CANNOT RUN - cargo mutants was killed (SIGTERM 143) after 0 of 3 mutants, before its summary — 3 were never graded. Nothing was measured",
		},
		{
			name: "an unnamed signal says its number", status: 129 + 30, state: 2, log: "",
			head: "CANNOT RUN - cargo mutants was killed (signal 31, exit 159) after 0 mutants — how many there were is not in what the engine kept. Nothing was measured",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			state, reason, found := RustMutationKilled(c.status, c.log)
			if state != c.state {
				t.Errorf("state %d, want %d:\n%s", state, c.state, reason)
			}
			if head, _, _ := strings.Cut(reason, "\n"); head != c.head {
				t.Errorf("headline\n got %q\nwant %q", head, c.head)
			}
			for _, w := range c.has {
				if !strings.Contains(reason, w) {
					t.Errorf("reason lacks %q:\n%s", w, reason)
				}
			}
			if strings.Contains(reason, "never ran") {
				t.Errorf("a killed run RAN:\n%s", reason)
			}
			var causes []string
			for _, f := range found {
				causes = append(causes, f.Cause)
			}
			if strings.Join(causes, ",") != strings.Join(c.findings, ",") {
				t.Errorf("finding causes %v, want %v", causes, c.findings)
			}
		})
	}
}

// The killed finding says what was outstanding, and is unanalyzable: those
// mutants are evidence of nothing.
func TestRustMutationKilledFinding(t *testing.T) {
	_, _, found := RustMutationKilled(137, m137Killed)
	want := Finding{Verdict: VerdictUnanalyzable, Subject: "rust:mutation", Cause: "mutation-killed",
		Detail: "killed (SIGKILL 137) after 12 of 21 mutants — 9 were never graded; the outcomes found are the ones it printed before the kill",
		Probe:  "rust:mutation"}
	if len(found) == 0 || found[0] != want {
		t.Errorf("got %+v\nwant %+v", found, want)
	}
	// The survivors keep their own shape (rustMutantFindings).
	if found[1].Subject != "src/lib.rs:3" || !strings.HasPrefix(found[1].Detail, "replace untested -> i32 with 0 — survived") {
		t.Errorf("%+v", found[1])
	}
	// With the total unknown, the detail does not invent one.
	_, _, found = RustMutationKilled(137, "MISSED   src/a.rs:4:2: replace f with g in 1s build + 1s test")
	if found[0].Detail != "killed (SIGKILL 137) after 1 mutants — how many were never graded is unknown; the outcomes found are the ones it printed before the kill" {
		t.Errorf("%q", found[0].Detail)
	}
}

// The outcome lines read back into mutants.out's own list shape: the name
// without the times, one per line — the lists RustMutationVerdict reads.
func TestReadRustPrinted(t *testing.T) {
	p := readRustPrinted(m137Summary + "\nunviable src/lib.rs:9:1: replace q with r in 2s build\nTIMEOUT  src/lib.rs:8:1: replace h in k in 0s build + 21s test")
	if p.Total != 21 || p.Summary != "21 mutants tested in 85s: 5 missed, 16 caught" || p.Omitted != "" {
		t.Errorf("%+v", p)
	}
	if len(p.Caught) != 16 || len(p.Missed) != 5 || len(p.Unviable) != 1 || len(p.Timeout) != 1 {
		t.Errorf("counts %d %d %d %d", len(p.Caught), len(p.Missed), len(p.Unviable), len(p.Timeout))
	}
	for got, want := range map[string]string{
		p.Caught[3]:   "src/lib.rs:1:39: replace + with - in add",
		p.Unviable[0]: "src/lib.rs:9:1: replace q with r",
		p.Timeout[0]:  "src/lib.rs:8:1: replace h in k",
		p.Caught[15]:  `src/lib.rs:5:27: replace name -> String with "xyzzy".into()`,
	} {
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
	// The baseline's own line is not a mutant, and neither is a FAILED one.
	if q := readRustPrinted("ok       Unmutated baseline in 0s build + 4s test\nFAILED   Unmutated baseline in 3s build + 1s test"); len(q.Caught)+len(q.Missed) != 0 || q.Total != -1 {
		t.Errorf("%+v", q)
	}
	// Surrounding space and CRLF are not part of a line.
	if q := readRustPrinted("  Found 1 mutant to test\r\n caught   a.rs:1:1: x in 1s build + 1s test\r\n"); q.Total != 1 || len(q.Caught) != 1 || q.Caught[0] != "a.rs:1:1: x" {
		t.Errorf("%+v", q)
	}
}

func TestSignalOf(t *testing.T) {
	for code, want := range map[int]string{137: "SIGKILL 137", 143: "SIGTERM 143", 134: "SIGABRT 134", 139: "SIGSEGV 139", 130: "SIGINT 130", 129: "signal 1, exit 129", 128: "exit 128", 2: "exit 2"} {
		if got := signalOf(code); got != want {
			t.Errorf("signalOf(%d) = %q, want %q", code, got, want)
		}
	}
}
