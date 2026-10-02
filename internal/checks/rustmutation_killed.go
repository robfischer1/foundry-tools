package checks

// A CARGO MUTANTS RUN THE ENGINE KILLED, for the rust:mutation atom.
//
// An exec that ends in the signal range (137 is 128+9, SIGKILL) is answered by
// the engine as an error on every read, never as an exit code, and its
// filesystem goes with it: mutants.out is gone. What survives is the output
// the error carries (the SDK's ExecError: stdout and stderr, the engine keeping
// the last 100KB of each). paneless@d988b9b (2026-10-02, foundry-tools #13052)
// was killed three times after printing the same seven survivors, and each
// record said "the atom never ran". So the outcomes are read back out of what
// the tool printed — the lane runs it with --caught --unviable, so every graded
// mutant has a line — and settled the way RustMutationVerdict settles a run
// that finished, with the kill in the headline:
//
//   - nothing graded: could-not-run, nothing was measured;
//   - a survivor already printed: findings, the survivors and the kill named;
//   - everything graded so far caught: could-not-run — a run that stopped
//     short is not a pass, however clean its first half;
//   - killed after its summary line: the outcomes are whole, settled as they
//     read.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/execmem"
)

// rustPrinted is what a cargo mutants run printed, read back into the lists
// mutants.out would have held.
type rustPrinted struct {
	// Total is cargo-mutants' "Found N mutants to test"; -1 when the output
	// the engine kept does not carry it.
	Total                             int
	Missed, Caught, Unviable, Timeout []string
	// Summary is the closing count line, when the run got that far.
	Summary string
	// Omitted is the byte count the engine's "[omitting N bytes]..." marker
	// names when it kept only the tail.
	Omitted string
}

var (
	rustFoundLine = regexp.MustCompile(`^Found (\d+) mutants? to test`)
	// rustOutcome is one graded mutant's line: its outcome, its name as the
	// outcome lists write it, and its times. The times hold no colon and the
	// name always does, which is what lets a name contain " in ".
	rustOutcome  = regexp.MustCompile(`^(caught|MISSED|TIMEOUT|unviable)\s+(.+) in [^:]*build[^:]*$`)
	rustOmission = regexp.MustCompile(`^\[omitting (\d+) bytes\]\.\.\.`)
)

func readRustPrinted(log string) rustPrinted {
	p := rustPrinted{Total: -1}
	lines := strings.Split(log, "\n")
	if m := rustOmission.FindStringSubmatch(log); m != nil {
		// The kept tail starts mid-line; that fragment is no line at all.
		p.Omitted, lines = m[1], lines[1:]
	}
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if m := rustFoundLine.FindStringSubmatch(ln); m != nil {
			// \d+ always parses; one past int's range keeps "unknown".
			if n, err := strconv.Atoi(m[1]); err == nil {
				p.Total = n
			}
			continue
		}
		if rustSummary.MatchString(ln) {
			p.Summary = ln
			continue
		}
		m := rustOutcome.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		switch m[1] {
		case "caught":
			p.Caught = append(p.Caught, m[2])
		case "MISSED":
			p.Missed = append(p.Missed, m[2])
		case "TIMEOUT":
			p.Timeout = append(p.Timeout, m[2])
		case "unviable":
			p.Unviable = append(p.Unviable, m[2])
		}
	}
	return p
}

// signalOf names an exit in the signal range as the signal it is: 137 is
// "SIGKILL 137".
func signalOf(status int) string {
	if status <= 128 {
		return fmt.Sprintf("exit %d", status)
	}
	if name, ok := map[int]string{2: "SIGINT", 6: "SIGABRT", 9: "SIGKILL", 11: "SIGSEGV", 15: "SIGTERM"}[status-128]; ok {
		return fmt.Sprintf("%s %d", name, status)
	}
	return fmt.Sprintf("signal %d, exit %d", status-128, status)
}

// RustMutationKilled settles a cargo mutants exec the engine ended with a
// signal, from the output its error carried.
func RustMutationKilled(status int, log string) (int, string, []Finding) {
	return rustMutationKilled(status, log, TimedOutMutantIsDetected)
}

func rustMutationKilled(status int, log string, timeoutDetected bool) (int, string, []Finding) {
	p := readRustPrinted(log)
	graded := len(p.Missed) + len(p.Caught) + len(p.Unviable) + len(p.Timeout)
	whole := p.Summary != "" && p.Omitted == ""

	count, left := fmt.Sprintf("%d", graded), "how many were never graded is unknown"
	where := " — how many there were is not in what the engine kept"
	if p.Total >= 0 {
		count = fmt.Sprintf("%d of %d", graded, p.Total)
		left = fmt.Sprintf("%d were never graded", p.Total-graded)
		where = ", before its summary — " + left
	}
	if whole {
		where = ", after its summary, so the outcomes are whole"
	}
	mem := execMemoryReading(log)
	oomNote := ""
	if mem != nil && mem[4] != "0" {
		oomNote = fmt.Sprintf(", out of memory (oom_kill=%s at peak_bytes=%s of memory_max=%s)", mem[4], mem[2], mem[3])
	}
	killed := fmt.Sprintf("killed (%s) after %s mutants%s", signalOf(status), count, oomNote)
	head := "cargo mutants was " + killed + where + ". "

	var after strings.Builder
	switch {
	case mem == nil:
		after.WriteString("\n\nthe exec printed no memory reading")
	case oomNote != "":
		after.WriteString("\n\n" + execMemoryLead + mem[1])
	default:
		after.WriteString("\n\n" + execMemoryLead + mem[1] + ". No OOM kill was counted by then; a kill inside the final second is not seen.")
	}
	if p.Omitted != "" {
		fmt.Fprintf(&after, "\n\nThe engine kept only the end of its output (it omitted %s bytes): outcomes printed before that are not in this record.", p.Omitted)
	}
	printed := "it printed nothing"
	if strings.TrimSpace(log) != "" {
		printed = "the last 20 lines it printed:\n```\n" + boundedTail(log, 20, 4000) + "\n```"
	}
	fmt.Fprintf(&after, "\n\n%s", printed)

	if graded == 0 {
		return 2, "CANNOT RUN - " + head + "Nothing was measured" + after.String(), nil
	}

	run := RustMutationRun{Log: log, Missed: lines(p.Missed), Caught: lines(p.Caught), Unviable: lines(p.Unviable), Timeout: lines(p.Timeout)}
	switch {
	case len(p.Timeout) > 0:
		run.Status = cargoMutantsTimeout
	case len(p.Missed) > 0:
		run.Status = cargoMutantsMissed
	}
	state, reason, found := rustMutationVerdict(run, timeoutDetected)
	if whole {
		return state, head + reason, found
	}
	reason = strings.TrimRight(reason, "\n")

	switch state {
	case 0:
		_, table, _ := strings.Cut(reason, "\n\n")
		return 2, "CANNOT RUN - " + head + "Every mutant it graded was caught, and a run that stopped short is not a pass\n\n" + table + after.String(), nil
	case 1:
		reason = strings.Replace(reason, "survived the suite", "survived the suite before the kill", 1)
		found = append([]Finding{{Verdict: VerdictUnanalyzable, Subject: "rust:mutation", Cause: "mutation-killed",
			Detail: killed + " — " + left + "; the outcomes found are the ones it printed before the kill", Probe: "rust:mutation"}}, found...)
		return 1, head + reason + after.String(), found
	}
	return state, "CANNOT RUN - " + head + strings.TrimPrefix(reason, "CANNOT RUN - ") + after.String(), nil
}

// THE EXEC'S MEMORY, as execmem printed it while cargo mutants ran: the
// cgroup's peak, its cap and its OOM-kill count, read every second. The last
// line printed before a kill is the record; nothing can be read after one.
var execMemoryLine = regexp.MustCompile(`^` + regexp.QuoteMeta(execmem.Prefix) + ` (peak_bytes=(\d+) memory_max=(\S+) oom_kill=(\d+))$`)

const execMemoryLead = "The exec's memory at its last reading (execmem, every second — a lower bound): "

// execMemoryReading is the last reading in log as its submatches — the
// reading, peak, cap, OOM-kill count — or nil when there is none.
func execMemoryReading(log string) []string {
	var last []string
	for _, ln := range strings.Split(log, "\n") {
		if m := execMemoryLine.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			last = m
		}
	}
	return last
}

// lastExecMemory is the last reading without its prefix, and whether it
// counted an OOM kill.
func lastExecMemory(log string) (string, bool) {
	m := execMemoryReading(log)
	if m == nil {
		return "", false
	}
	return m[1], m[4] != "0"
}

// lines is a list as mutants.out writes one: a name per line. An empty list
// is a lone newline, which lineCount reads as the zero it is.
func lines(names []string) string {
	return strings.Join(names, "\n") + "\n"
}
