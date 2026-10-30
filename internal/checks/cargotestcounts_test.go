package checks

import (
	"strings"
	"testing"
)

// Real cargo test output: a unit-test binary, an empty one, and a doc-test
// block, as `cargo test --workspace --lib` and plain `cargo test` print them.
const cargoTestSample = `   Compiling stellar-core v0.1.0 (/src)
    Finished ` + "`test`" + ` profile [unoptimized + debuginfo] target(s) in 41.20s
     Running unittests src/lib.rs (target/debug/deps/stellar_core-1a2b3c)

running 223 tests
test wit::identity::round_trip ... ok
test wit::identity::slow ... ignored

test result: ok. 197 passed; 0 failed; 26 ignored; 0 measured; 14 filtered out; finished in 0.45s

     Running unittests src/main.rs (target/debug/deps/tool-4d5e6f)

running 0 tests

test result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s
`

func TestParseCargoTestCounts(t *testing.T) {
	for name, c := range map[string]struct {
		out  string
		want CargoTestCounts
		line string
	}{
		"two binaries": {cargoTestSample, CargoTestCounts{197, 0, 26, 2},
			"native tests: 197 passed; 0 failed; 26 ignored across 2 test results"},
		"a failed binary": {"test result: FAILED. 3 passed; 2 failed; 1 ignored; 0 measured; 0 filtered out; finished in 0.01s\n", CargoTestCounts{3, 2, 1, 1},
			"native tests: 3 passed; 2 failed; 1 ignored across 1 test results"},
		"zero tests ran": {"running 0 tests\n\ntest result: ok. 0 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n", CargoTestCounts{0, 0, 0, 1},
			"NO NATIVE TEST RAN"},
		"no result line":                     {"", CargoTestCounts{}, "the count is unknown"},
		"a log line that merely mentions it": {"note: test result: ok. 9 passed; 0 failed; 0 ignored in prose\n", CargoTestCounts{}, "the count is unknown"},
	} {
		t.Run(name, func(t *testing.T) {
			got := ParseCargoTestCounts(c.out)
			if got != c.want {
				t.Errorf("counts %+v, want %+v", got, c.want)
			}
			if l := got.Line(); !strings.Contains(l, c.line) {
				t.Errorf("line %q lacks %q", l, c.line)
			}
		})
	}
}

func TestCargoTestCountsFinding(t *testing.T) {
	if f := (CargoTestCounts{197, 0, 26, 2}).Finding("p"); len(f) != 1 || f[0].Verdict != VerdictHolds || f[0].Probe != "p" {
		t.Errorf("tests ran: %+v", f)
	}
	if f := (CargoTestCounts{0, 0, 0, 1}).Finding("p"); len(f) != 1 || f[0].Verdict != VerdictInert || f[0].Cause != "no-tests-ran" {
		t.Errorf("no test ran: %+v", f)
	}
	if f := (CargoTestCounts{}).Finding("p"); f != nil {
		t.Errorf("unknown count must record nothing: %+v", f)
	}
}
