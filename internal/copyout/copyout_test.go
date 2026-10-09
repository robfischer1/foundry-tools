package copyout

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSplitsPairsFromTheCommand(t *testing.T) {
	pairs, cmd, err := Parse([]string{"/c/a=/out/a", "/c/b=/out/x/b", "--", "cargo", "build", "--", "z"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 2 || pairs[0] != (Pair{"/c/a", "/out/a"}) || pairs[1] != (Pair{"/c/b", "/out/x/b"}) {
		t.Errorf("pairs %v", pairs)
	}
	// Only the FIRST "--" separates; the command keeps its own.
	if strings.Join(cmd, " ") != "cargo build -- z" {
		t.Errorf("command %q", cmd)
	}
	// No pair at all is a build with nothing to carry, and still a build.
	if pairs, cmd, err := Parse([]string{"--", "true"}); err != nil || len(pairs) != 0 || len(cmd) != 1 {
		t.Errorf("a bare build: %v %v %v", pairs, cmd, err)
	}
}

func TestParseRefusesWhatIsNotItsGrammar(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"a=b"},
		{"a=b", "--"},
		{"ab", "--", "true"},
		{"=b", "--", "true"},
		{"a=", "--", "true"},
	} {
		if _, _, err := Parse(args); err == nil {
			t.Errorf("%q parsed", args)
		}
	}
}

func TestMainIsAUsageErrorOnBadArguments(t *testing.T) {
	var stderr bytes.Buffer
	if code := Main([]string{"x"}, &bytes.Buffer{}, &stderr); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage: copyout") {
		t.Errorf("no usage line: %q", stderr.String())
	}
}

// A built output is copied out with its mode, into a directory made for it.
func TestABuildThatPassesCarriesItsOutputsOut(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "cache", "bin"), filepath.Join(dir, "out", "deep", "bin")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	// The "build" writes the output; it did not exist before.
	build := []string{"sh", "-c", "printf built > " + src + " && chmod 0755 " + src}
	var stdout bytes.Buffer
	if code := Main(append([]string{src + "=" + dst, "--"}, build...), &stdout, &bytes.Buffer{}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "built" {
		t.Fatalf("dst %q, %v", got, err)
	}
	if info, _ := os.Stat(dst); info.Mode().Perm() != 0o755 {
		t.Errorf("mode %v, want 0755", info.Mode().Perm())
	}
	// The source stays in the cache: the next build's dependency artifacts are
	// what the volume is for.
	if _, err := os.Stat(src); err != nil {
		t.Errorf("the source left the cache: %v", err)
	}
}

// THE STALE CASE. A file a previous tree left in the volume, which this build
// does not produce, is cleared before the build and so never carried.
func TestAnOutputThisBuildDidNotProduceIsNotCarried(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "bin"), filepath.Join(dir, "out", "bin")
	if err := os.WriteFile(src, []byte("an older tree's"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code := Run([]Pair{{src, dst}}, []string{"true"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a stale output was carried: %v", err)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the stale source was not cleared: %v", err)
	}
}

// A failed build is answered with its own code, and nothing is copied.
func TestAFailedBuildAnswersItsOwnCodeAndCopiesNothing(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "bin"), filepath.Join(dir, "out", "bin")
	build := []string{"sh", "-c", "printf half > " + src + "; exit 101"}
	if code := Run([]Pair{{src, dst}}, build, &bytes.Buffer{}, &bytes.Buffer{}); code != 101 {
		t.Errorf("exit %d, want 101", code)
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a failed build's output was carried: %v", err)
	}
}

func TestASignalIsAnsweredAsAShellWould(t *testing.T) {
	if code := Run(nil, []string{"sh", "-c", "kill -9 $$"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 137 {
		t.Errorf("exit %d, want 137", code)
	}
}

func TestACommandThatDoesNotStartIs127(t *testing.T) {
	var stderr bytes.Buffer
	if code := Run(nil, []string{"/nonexistent/cargo"}, &bytes.Buffer{}, &stderr); code != 127 {
		t.Errorf("exit %d, want 127", code)
	}
	if !strings.Contains(stderr.String(), "could not start /nonexistent/cargo") {
		t.Errorf("stderr %q", stderr.String())
	}
}

// The build's output reaches the caller's writers: the verdict reads it.
func TestTheBuildsStreamsPassThrough(t *testing.T) {
	var stdout, stderr bytes.Buffer
	Run(nil, []string{"sh", "-c", "echo out; echo err >&2"}, &stdout, &stderr)
	if stdout.String() != "out\n" || stderr.String() != "err\n" {
		t.Errorf("stdout %q stderr %q", stdout.String(), stderr.String())
	}
}

// A wait that fails for a reason other than the command's exit is 1.
func TestAWaitThatFailsOtherwiseIsOne(t *testing.T) {
	if code := Run(nil, []string{"echo", "x"}, failingWriter{}, &bytes.Buffer{}); code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

// An output that will not copy is CopyFailed, never the build's verdict.
func TestAnOutputThatWillNotCopyIsCopyFailed(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "bin")
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	build := []string{"sh", "-c", "printf x > " + src}
	if code := Run([]Pair{{src, filepath.Join(blocker, "under", "bin")}}, build, &bytes.Buffer{}, &stderr); code != CopyFailed {
		t.Errorf("exit %d, want %d", code, CopyFailed)
	}
	if !strings.Contains(stderr.String(), "could not copy") {
		t.Errorf("stderr %q", stderr.String())
	}
	// And a destination that is a directory cannot be opened as the file.
	if code := Run([]Pair{{src, dir}}, build, &bytes.Buffer{}, &bytes.Buffer{}); code != CopyFailed {
		t.Errorf("exit %d, want %d", code, CopyFailed)
	}
}

// A source that cannot be cleared is refused before the build runs: carrying it
// could carry a stale one.
func TestASourceThatWillNotClearStopsBeforeTheBuild(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "bin")
	if err := os.MkdirAll(filepath.Join(src, "full"), 0o755); err != nil {
		t.Fatal(err)
	}
	ran := filepath.Join(dir, "ran")
	var stderr bytes.Buffer
	if code := Run([]Pair{{src, filepath.Join(dir, "out")}}, []string{"touch", ran}, &bytes.Buffer{}, &stderr); code != CopyFailed {
		t.Errorf("exit %d, want %d", code, CopyFailed)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("the build ran over a source that would not clear")
	}
	if !strings.Contains(stderr.String(), "could not clear") {
		t.Errorf("stderr %q", stderr.String())
	}
}

// A source that is a directory opens but does not read as a file: CopyFailed.
func TestASourceThatIsNotAFileIsCopyFailed(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "bin")
	build := []string{"mkdir", src}
	if code := Run([]Pair{{src, filepath.Join(dir, "out", "bin")}}, build, &bytes.Buffer{}, &bytes.Buffer{}); code != CopyFailed {
		t.Errorf("exit %d, want %d", code, CopyFailed)
	}
}
