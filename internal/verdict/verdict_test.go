package verdict

import (
	"bytes"
	"os"
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
		code := Run(c.args, &errOut)
		if code != c.code || !strings.Contains(errOut.String(), c.stderr) {
			t.Errorf("%v: code %d stderr %q, want %d containing %q", c.args, code, errOut.String(), c.code, c.stderr)
		}
	}
}

// A verdict with no reason prints nothing at all.
func TestAVerdictWithNoReasonPrintsNothing(t *testing.T) {
	var errOut bytes.Buffer
	if code := Run([]string{"1"}, &errOut); code != 1 || errOut.Len() != 0 {
		t.Fatalf("code %d stderr %q", code, errOut.String())
	}
}

// A reason given as @file is read from the file and printed once; the code is
// the verdict's. A file that will not read is a could-not-run.
func TestAReasonCanComeFromAFile(t *testing.T) {
	old := readFile
	defer func() { readFile = old }()
	readFile = func(path string) ([]byte, error) {
		if path != "/reason" {
			return nil, os.ErrNotExist
		}
		return []byte("FAIL: TestX\nline two\n\n"), nil
	}
	var out bytes.Buffer
	if code := Run([]string{"1", "@/reason"}, &out); code != 1 || out.String() != "FAIL: TestX\nline two\n" {
		t.Errorf("code %d, stderr %q", code, out.String())
	}
	out.Reset()
	if code := Run([]string{"1", "@/missing"}, &out); code != 2 || !strings.Contains(out.String(), "cannot read the reason") {
		t.Errorf("an unreadable file: code %d, stderr %q", code, out.String())
	}
	// An @ in a longer reason is text, not a file.
	out.Reset()
	if code := Run([]string{"1", "@/reason", "and more"}, &out); code != 1 || out.String() != "@/reason and more\n" {
		t.Errorf("a multi-word reason is literal: code %d, stderr %q", code, out.String())
	}
	// And a plain @-less reason is untouched.
	out.Reset()
	if code := Run([]string{"0", "user@host"}, &out); code != 0 || out.String() != "user@host\n" {
		t.Errorf("code %d, stderr %q", code, out.String())
	}
}

// A reason that itself starts with '@' round-trips: ReasonArg encodes it, Run
// prints it back. Every reason, with or without a leading '@', comes out as
// it went in, and nothing is read as a file.
func TestAReasonStartingWithAtRoundTrips(t *testing.T) {
	old := readFile
	defer func() { readFile = old }()
	readFile = func(path string) ([]byte, error) { return nil, os.ErrNotExist }
	for _, reason := range []string{
		"@alice broke the build", "@/etc/passwd", "@", "@@", "@@x", "plain", "user@host", "two\nlines",
	} {
		var out bytes.Buffer
		if code := Run([]string{"1", ReasonArg(reason)}, &out); code != 1 || out.String() != reason+"\n" {
			t.Errorf("%q: code %d, stderr %q", reason, code, out.String())
		}
	}
}

// The escape is `@@`: it is the only new meaning, and the existing forms are
// as they were. `@@x` was never a readable file (the path `@x`), so no working
// caller changes.
func TestTheAtEscapeKeepsTheExistingForms(t *testing.T) {
	old := readFile
	defer func() { readFile = old }()
	readFile = func(path string) ([]byte, error) { return []byte("from file\n"), nil }
	var out bytes.Buffer
	if code := Run([]string{"1", "@/reason"}, &out); code != 1 || out.String() != "from file\n" {
		t.Errorf("the file form: code %d, stderr %q", code, out.String())
	}
	out.Reset()
	if code := Run([]string{"1", "@@/reason"}, &out); code != 1 || out.String() != "@/reason\n" {
		t.Errorf("the escaped form: code %d, stderr %q", code, out.String())
	}
	out.Reset()
	if code := Run([]string{"1", "@/reason", "more"}, &out); code != 1 || out.String() != "@/reason more\n" {
		t.Errorf("a multi-argument reason is literal: code %d, stderr %q", code, out.String())
	}
	if got := ReasonArg("plain"); got != "plain" {
		t.Errorf("a reason with no leading @ is its own argument, got %q", got)
	}
}
