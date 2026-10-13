package devlane

import (
	"fmt"
	"regexp"
	"strings"
)

// A passing run must not look like a failing one. cerberus's suite exercises
// failure paths on purpose and logs them at ERROR, so a green `cargo test`
// filled the terminal with errors that were the point. TestReport answers a
// pass with the summary lines alone; a failure keeps every byte of the tool's
// output (the tail Reason keeps) and ends with the same summary.
//
// Pure over the captured streams and the exit code: no engine, no clock.

var (
	// cargo: "     Running unittests src/lib.rs (target/debug/deps/x-1a2b)" and
	// "   Doc-tests x" head each test binary's block.
	rustBinary = regexp.MustCompile(`^\s*(Running|Doc-tests)\s`)
	// "test result: ok. 3 passed; 0 failed; 1 ignored; 0 measured; 0 filtered out; finished in 0.01s"
	rustResult = regexp.MustCompile(`^test result: \S+ (\d+) passed; (\d+) failed; (\d+) ignored`)
	// go: "ok  \tpkg\t0.5s", "ok  \tpkg\t(cached)", "?   \tpkg\t[no test files]",
	// "FAIL\tpkg\t0.5s". A bare "FAIL" line has no package and is not matched.
	goPackage = regexp.MustCompile(`^(ok|FAIL|\?)\s+\S+\s`)
)

// Summary is the test run's digest: the lines that say what ran and how it
// ended. "" when the output holds none (a build that never reached the tests).
func Summary(lang, stdout, stderr string) string {
	var lines, heads, results []string
	var passed, failed, ignored, binaries int
	for _, l := range strings.Split(stdout+"\n"+stderr, "\n") {
		l = strings.TrimRight(l, "\r")
		switch lang {
		case Go:
			if goPackage.MatchString(l) {
				lines = append(lines, l)
			}
		case Rust:
			if rustBinary.MatchString(l) {
				heads = append(heads, l)
			} else if m := rustResult.FindStringSubmatch(l); m != nil {
				results = append(results, l)
				binaries++
				passed += atoi(m[1])
				failed += atoi(m[2])
				ignored += atoi(m[3])
			}
		}
	}
	// cargo prints the heads on stderr and the results on stdout, so the two
	// streams arrive apart. One result follows each head: pair them back up when
	// the counts agree, else list heads then results rather than guess.
	if len(heads) == len(results) {
		for i := range heads {
			lines = append(lines, heads[i], results[i])
		}
	} else {
		lines = append(lines, heads...)
		lines = append(lines, results...)
	}
	if len(lines) == 0 {
		return ""
	}
	if lang == Rust && binaries > 0 {
		lines = append(lines, fmt.Sprintf("total: %d passed; %d failed; %d ignored across %d test results", passed, failed, ignored, binaries))
	}
	return strings.Join(lines, "\n") + "\n"
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// TestReport is what the test verb answers. verbose, or output with nothing to
// summarise, returns the whole output as before. A pass returns the summary. A
// failure returns Reason (the full output, tailed per stream) and then the
// summary, so the verdict is the last thing read.
func TestReport(lang, stdout, stderr string, code int, verbose bool) string {
	sum := Summary(lang, stdout, stderr)
	if code != 0 {
		r := Reason(stdout, stderr)
		if sum == "" {
			return r
		}
		if r != "" && !strings.HasSuffix(r, "\n") {
			r += "\n"
		}
		return r + "\n" + sum
	}
	if verbose || sum == "" {
		return stdout + stderr
	}
	return sum
}
