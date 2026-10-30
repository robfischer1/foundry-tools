package checks

import (
	"fmt"
	"regexp"
	"strconv"
)

// cargoTestResult is cargo test's per-binary summary line:
// "test result: ok. 223 passed; 0 failed; 2 ignored; 0 measured; 14 filtered out; finished in 0.45s".
// The "FAILED." form carries the same counts.
var cargoTestResult = regexp.MustCompile(`(?m)^\s*test result: \w+\. (\d+) passed; (\d+) failed; (\d+) ignored`)

// CargoTestCounts is what a cargo test run reported, summed over its test
// binaries (one "test result:" line each). Results is how many lines it read:
// 0 means the output carried none, which is "counts unknown", not "zero tests".
type CargoTestCounts struct {
	Passed, Failed, Ignored, Results int
}

// ParseCargoTestCounts reads the "test result:" lines of a cargo test run's
// output. Reporting only: no verdict is read from it.
func ParseCargoTestCounts(out string) CargoTestCounts {
	var c CargoTestCounts
	for _, m := range cargoTestResult.FindAllStringSubmatch(out, -1) {
		p, _ := strconv.Atoi(m[1])
		f, _ := strconv.Atoi(m[2])
		i, _ := strconv.Atoi(m[3])
		c.Passed, c.Failed, c.Ignored, c.Results = c.Passed+p, c.Failed+f, c.Ignored+i, c.Results+1
	}
	return c
}

// Line is the counts as a log line, naming a run that read no counts or ran no
// test rather than leaving the reader to infer it.
func (c CargoTestCounts) Line() string {
	switch {
	case c.Results == 0:
		return "native tests: cargo printed no \"test result:\" line, so the count is unknown"
	case c.Passed+c.Failed == 0:
		return fmt.Sprintf("native tests: 0 passed; 0 failed; %d ignored across %d test results - NO NATIVE TEST RAN", c.Ignored, c.Results)
	}
	return fmt.Sprintf("native tests: %d passed; %d failed; %d ignored across %d test results", c.Passed, c.Failed, c.Ignored, c.Results)
}

// Finding is the counts as the record's finding for a pass: "holds" when tests
// ran, "inert" when none did, and none when the count is unknown (a finding
// of unknown would outrank the pass in the record's lattice).
func (c CargoTestCounts) Finding(probe string) []Finding {
	if c.Results == 0 {
		return nil
	}
	v := VerdictHolds
	cause := "tests-passed"
	if c.Passed+c.Failed == 0 {
		v, cause = VerdictInert, "no-tests-ran"
	}
	return []Finding{{Verdict: v, Subject: "native tests", Cause: cause,
		Detail: fmt.Sprintf("%d passed; %d failed; %d ignored across %d test results", c.Passed, c.Failed, c.Ignored, c.Results), Probe: probe}}
}
