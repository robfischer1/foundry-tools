package checks

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// THE RUST MUTATION GATE'S DECISIONS, in Go. They were foundry-stocks'
// ci/lib/mutation/rust.sh resolve and score phases (the workspace mapping was a
// python3 heredoc inside resolve); the atom (atoms_rust.go rustMutation) now
// runs git, cargo metadata and cargo mutants as plain execs and settles here.
//
// THERE IS NO PERCENTAGE THRESHOLD. cargo-mutants exits 2 on ANY viable
// survivor and rustc drops the unviable for free, so the exit code is the
// answer and the counts are for the reader. Exit 3 — a mutant timed out — is a
// report too, read the same way (TimedOutMutantIsDetected, RustTimeoutGuard).
// Every other non-zero exit — a baseline that did not build, a diff that did
// not match the tree (5) — measured nothing, and is a could-not-run, never a
// pass.
//
// Only what the atom reached was ported. rust.sh also took a full mode, a
// workdir, a named package list and a gate switch; the atom set none of them,
// and a repo has no say in any of them (MutationScope).

// RustMutationSpecs is the pathspec a pull's diff is taken over: the declared
// critical modules when there are any, otherwise every rust source outside the
// integration tests. A plain `*` in a git pathspec crosses `/`.
func RustMutationSpecs(mods string) []string {
	if specs := strings.Fields(mods); len(specs) > 0 {
		return specs
	}
	return []string{"*.rs", ":!tests/"}
}

// DiffAddsLines reports whether a unified diff adds any line. A diff that
// only removes lines stands down before cargo-mutants' baseline build: it would
// find nothing to mutate ("No mutants to filter", exit 0) after spending
// 20-40s to say so. A `+++` file header is not an added line.
func DiffAddsLines(diff string) bool {
	for _, ln := range strings.Split(diff, "\n") {
		if len(ln) >= 2 && ln[0] == '+' && ln[1] != '+' {
			return true
		}
	}
	return false
}

// RustTouchedMembers names every workspace member a pull's files belong to,
// sorted, for cargo mutants -p.
//
// cargo-mutants at a workspace root that is also a package mutates THAT
// package and no other, as cargo does, and -D only filters the mutants it
// generated: stellar-core-rust#8 changed src/ and tools/kafka-topics-gen, the
// lane scored 69 mutants all from src/, and `-p kafka-topics-gen` over the same
// diff scored 26 more (foundry/foundry-stocks#9293). So each file is mapped to
// the member whose manifest directory holds it — the DEEPEST one, since
// tools/gen/ inside the root package's directory belongs to tools/gen.
//
// A single-package crate names nothing and runs as it always has. A file no
// member holds is compiled by none and names nothing. metadata is
// `cargo metadata --no-deps --format-version 1`; files are relative to root,
// the directory cargo ran in. Metadata that does not parse is an error, never
// a fallback to the root package: the fallback IS the bug.
func RustTouchedMembers(metadata []byte, root string, files []string) ([]string, error) {
	var meta struct {
		Packages []struct {
			Name         string `json:"name"`
			ID           string `json:"id"`
			ManifestPath string `json:"manifest_path"`
		} `json:"packages"`
		WorkspaceMembers []string `json:"workspace_members"`
	}
	if err := json.Unmarshal(metadata, &meta); err != nil {
		return nil, fmt.Errorf("cargo metadata did not parse: %w", err)
	}
	members := map[string]bool{}
	for _, id := range meta.WorkspaceMembers {
		members[id] = true
	}
	byDir := map[string]string{}
	for _, p := range meta.Packages {
		if members[p.ID] {
			byDir[path.Dir(path.Clean(p.ManifestPath))] = p.Name
		}
	}
	if len(byDir) < 2 {
		return nil, nil
	}

	// Each file's owner is the first member directory met walking up from it:
	// the nearest ancestor is the deepest.
	touched := map[string]bool{}
	for _, rel := range files {
		if rel == "" {
			continue
		}
		for d := path.Dir(path.Join(root, rel)); ; d = path.Dir(d) {
			if name, ok := byDir[d]; ok {
				touched[name] = true
				break
			}
			if d == "/" || d == "." {
				break
			}
		}
	}
	names := make([]string, 0, len(touched))
	for n := range touched {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

// RustMutationRun is what one cargo mutants run left behind: its exit, its
// output, and the four outcome lists it writes under mutants.out/ (a list it
// did not write reads as empty).
type RustMutationRun struct {
	Status                            int
	Log                               string
	Missed, Caught, Unviable, Timeout string
	// Baseline is mutants.out/log/baseline.log — the unmutated build and
	// test run. Under nextest every test line carries its wall time.
	Baseline string
}

// SlowestTestsShown is how many of the baseline's slowest tests the verdict
// names. Ten is a screen, not a suite.
const SlowestTestsShown = 10

var (
	// A nextest status line: `        PASS [   0.412s] crate::bin test::name`,
	// FAIL the same. The bracket is the test's own wall time; a SLOW line's
	// `[> 5.000s]` is a threshold, not a time, and is not matched.
	nextestStatus = regexp.MustCompile(`^\s*(PASS|FAIL|TIMEOUT|LEAK)\s+\[\s*([0-9]+\.[0-9]+)s\]\s+(\S.*?)\s*$`)
	// nextest's closing line: `     Summary [  27.9s] 154 tests run: 154 passed, 0 skipped`.
	nextestSummary = regexp.MustCompile(`^\s*Summary\s+\[\s*[0-9.]+s\].*$`)
)

// SlowestTests reads the baseline test run and names the n slowest tests,
// longest first, with nextest's own summary line above them. Empty when the
// log carries no nextest lines — a libtest run, or a baseline that never
// reached its tests — so a verdict never grows a section it cannot fill.
func SlowestTests(baseline string, n int) string {
	type timed struct {
		secs float64
		name string
	}
	var tests []timed
	summary := ""
	for _, line := range strings.Split(baseline, "\n") {
		if m := nextestStatus.FindStringSubmatch(line); m != nil {
			secs, err := strconv.ParseFloat(m[2], 64)
			if err != nil {
				continue
			}
			tests = append(tests, timed{secs, m[3]})
			continue
		}
		if m := nextestSummary.FindString(line); m != "" {
			summary = strings.TrimSpace(m)
		}
	}
	if len(tests) == 0 {
		return ""
	}
	// Stable, strictly greater: two tests with one wall time keep the order
	// nextest printed them in.
	sort.SliceStable(tests, func(i, j int) bool { return tests[i].secs > tests[j].secs })
	tests = tests[:min(n, len(tests))]
	var b strings.Builder
	b.WriteString("\n**Where the test half of each mutant goes** — the baseline run's slowest tests; every mutant pays for these again.\n\n```\n")
	if summary != "" {
		b.WriteString(summary + "\n")
	}
	for _, t := range tests {
		fmt.Fprintf(&b, "%7.2fs  %s\n", t.secs, t.name)
	}
	b.WriteString("```\n")
	return b.String()
}

// cargo-mutants' exit codes, as its own exit_code.rs names them. Only the two
// OUTCOME exits carry a report: 2 (a viable mutant was missed) and 3 (a mutant
// timed out — and 3 wins when both happened, which is how anvil@0c5355f's one
// missed mutant came to be thrown away with the run). 4 is a baseline that
// failed or hung, so nothing was tested; 1 a usage error; 5 and 6 a diff that
// did not match the tree; 70 a crash.
const (
	cargoMutantsMissed  = 2
	cargoMutantsTimeout = 3
)

// RustTimeoutGuard: a run whose timed-out mutants are MORE THAN HALF of the
// viable ones (caught + missed + timed out), or that caught none except by
// timeout, did not show that the suite works — it showed a suite, or a runner,
// that hangs. That run is could-not-run with the counts in its reason, never a
// green. Half is the line because below it the suite demonstrably answered
// most mutants by failing; at or above it, "detected by hanging" is the
// majority of the evidence and no longer the exception.
func RustTimeoutGuard(caught, missed, timeout int) bool {
	return timeout > 0 && (caught == 0 || 2*timeout > caught+missed+timeout)
}

// RustMutationVerdict settles a cargo mutants run: 0 when every viable mutant
// was caught (a timed-out one counts as caught, TimedOutMutantIsDetected), 1
// when any survived, 2 when nothing was measured. The reason is the verdict's
// line, then the table and the survivor and timeout lists; the findings are
// one per survivor and one per timed-out mutant.
func RustMutationVerdict(run RustMutationRun) (int, string, []Finding) {
	return rustMutationVerdict(run, TimedOutMutantIsDetected)
}

func rustMutationVerdict(run RustMutationRun, timeoutDetected bool) (int, string, []Finding) {
	missed, caught := lineCount(run.Missed), lineCount(run.Caught)
	unviable, timeout := lineCount(run.Unviable), lineCount(run.Timeout)
	viable := missed + caught + timeout
	detected := caught
	if timeoutDetected {
		detected += timeout
	}
	pct := 0
	if viable > 0 {
		pct = detected * 100 / viable
	}

	var b strings.Builder
	fmt.Fprintf(&b, "### Mutation gate — rust (diff)\n\n| caught | missed | unviable | timeout | kill rate |\n|---|---|---|---|---|\n| %d | %d | %d | %d | %d%% of %d viable |\n",
		caught, missed, unviable, timeout, pct, viable)
	if missed > 0 {
		b.WriteString("\n**Survivors** — each is a HYPOTHESIS, not a finding. A mutant that lives may be a real test gap, or it may be equivalent to the original, which is undecidable in general. Read it before you write a test for it; if it is genuinely equivalent, the answer is an `exclude_re` entry in `.cargo/mutants.toml` with the reason beside it.\n\n```\n")
		b.WriteString(withNewline(run.Missed))
		b.WriteString("```\n")
	}
	if timeout > 0 {
		b.WriteString("\n**Timed out** — the suite noticed each of these by hanging until the test timeout killed it. ")
		if timeoutDetected {
			b.WriteString("Counted as caught, so they do not red the lane; ")
		} else {
			b.WriteString("Not counted as caught: a timeout is unmeasured; ")
		}
		b.WriteString(MutantTimeoutAdvice + ".\n\n```\n")
		b.WriteString(withNewline(run.Timeout))
		b.WriteString("```\n")
	}
	b.WriteString(SlowestTests(run.Baseline, SlowestTestsShown))
	summary := b.String()

	switch run.Status {
	case 0:
		return 0, fmt.Sprintf("every viable mutant was caught (%d of %d)\n\n%s", caught, viable, summary), nil
	case cargoMutantsMissed, cargoMutantsTimeout:
	default:
		return 2, cannotRunRust(run), nil
	}

	// AN OUTCOME EXIT WITH NO OUTCOME BEHIND IT is a run whose lists did not
	// read, not a report: exit 3 means mutants.out/timeout.txt names at least
	// one mutant, exit 2 that missed.txt does.
	if (run.Status == cargoMutantsTimeout && timeout == 0) || (run.Status == cargoMutantsMissed && missed == 0) {
		return 2, fmt.Sprintf("%s — and mutants.out lists no mutant with that outcome, so the outcomes could not be read\n\n%s", cannotRunRust(run), summary), nil
	}
	if timeout > 0 && !timeoutDetected {
		return 2, fmt.Sprintf("CANNOT RUN - %d mutant(s) timed out and a timeout is unmeasured (TimedOutMutantIsDetected is off) — %d caught, %d missed\n\n%s",
			timeout, caught, missed, summary), nil
	}
	if RustTimeoutGuard(caught, missed, timeout) {
		return 2, fmt.Sprintf("CANNOT RUN - %d of %d viable mutant(s) timed out and %d were caught by a failing test — a suite or a runner that hangs, not evidence the suite works (could-not-run when timeouts are more than half the viable mutants, or nothing was caught but by timeout)\n\n%s",
			timeout, viable, caught, summary), nil
	}

	found := rustMutantFindings(run.Missed, VerdictDrifted, "mutant-missed", func(string) string {
		return "survived the suite — a test gap or an equivalent mutant; read it before writing a test for it"
	})
	timeouts := rustTimeoutTimes(run.Log)
	found = append(found, rustMutantFindings(run.Timeout, timedOutMutantVerdict(timeoutDetected), MutantTimeoutCause, func(name string) string {
		after := "at the test timeout"
		if secs, ok := timeouts[name]; ok {
			after = "after " + secs + " of test, the timeout"
		}
		return "timed out " + after + ": " + MutantTimeoutAdvice
	})...)
	found = capFindings(found, "rust:mutation")

	hung := ""
	if timeout > 0 {
		hung = fmt.Sprintf("; %d more timed out, counted as caught and listed below", timeout)
	}
	if missed > 0 {
		return 1, fmt.Sprintf("%d viable mutant(s) survived the suite — see the survivor list below%s\n\n%s", missed, hung, summary), found
	}
	return 0, fmt.Sprintf("every viable mutant was caught (%d by a failing test, %d by a timeout, of %d)\n\n%s", caught, timeout, viable, summary), found
}

// cannotRunRust is a run that measured nothing, IN THE TOOL'S OWN WORDS. It
// used to end at the first output line that said "error" — and a run that
// says no such thing (anvil@0c5355f's exit 3: TIMEOUT, MISSED and a summary)
// left the sentence ending in a colon and nothing after it. So the headline
// carries the tool's summary line or its first error, and the tail of its
// output follows, bounded.
func cannotRunRust(run RustMutationRun) string {
	said := firstErrorLine(run.Log)
	if s := rustSummaryLine(run.Log); s != "" {
		said = s
	}
	if said == "" {
		said = "it printed no summary and no error"
	}
	return fmt.Sprintf("CANNOT RUN - cargo mutants exited %d — a broken run, not a survivor report (a baseline that did not build, a diff that did not match the tree, a crash): %s\n\nthe tail of its output:\n```\n%s\n```",
		run.Status, said, boundedTail(run.Log, 20, 4000))
}

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

// lineCount is `grep -c .`: the lines that hold at least one character.
func lineCount(s string) int {
	n := 0
	for _, ln := range strings.Split(s, "\n") {
		if ln != "" {
			n++
		}
	}
	return n
}

func withNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// firstErrorLine is the first line of a run's output that says "error" in any
// case, cut to 160 characters: the cause, not its fallout.
func firstErrorLine(log string) string {
	for _, ln := range strings.Split(log, "\n") {
		if strings.Contains(strings.ToLower(ln), "error") {
			r := []rune(ln)
			return string(r[:min(len(r), 160)])
		}
	}
	return ""
}
