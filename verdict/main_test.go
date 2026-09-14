package main

import (
	"bytes"
	"strings"
	"testing"
)

// The three verdicts pass through with their reason; anything else the door
// could not read is a could-not-run.
func TestVerdictAnswersTheCodeAndPrintsTheReason(t *testing.T) {
	for _, c := range []struct {
		args   []string
		code   int
		stderr string
	}{
		{[]string{"0"}, 0, ""},
		{[]string{"0", "clean:", "published"}, 0, "clean: published\n"},
		{[]string{"1", "findings", "in", "sign"}, 1, "findings in sign\n"},
		{[]string{"2", "could not run"}, 2, "could not run\n"},
		{[]string{"3", "nope"}, 2, `"3" is not a verdict`},
		{[]string{"-1"}, 2, "is not a verdict"},
		{[]string{"one"}, 2, "is not a verdict"},
		{nil, 2, "no code"},
	} {
		var errOut bytes.Buffer
		code := run(c.args, &errOut)
		if code != c.code || !strings.Contains(errOut.String(), c.stderr) {
			t.Errorf("%v: code %d stderr %q, want %d containing %q", c.args, code, errOut.String(), c.code, c.stderr)
		}
	}
}
