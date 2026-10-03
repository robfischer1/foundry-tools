package main

import (
	"errors"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/dagger"
)

// A FAILED EXEC'S EVIDENCE IS ITS COMMAND, THEN THE TAIL OF WHAT IT PRINTED —
// stderr first, because that is where a failing tool says why.
func TestExecEvidenceNamesTheCommandAndWhatItSaid(t *testing.T) {
	got := execEvidence([]string{"go", "mod", "download"}, "some stdout\n", "go: example.com/x@v1.2.3: 404 Not Found\n")
	want := "failed: go mod download\ngo: example.com/x@v1.2.3: 404 Not Found\nsome stdout"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

// NOTHING ATTACHED IS NOTHING SAID — not a "failed:" line with no command.
func TestExecEvidenceIsEmptyWhenTheEngineAttachedNothing(t *testing.T) {
	if got := execEvidence(nil, " \n", ""); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
	if got := execEvidence(nil, "", "boom"); got != "boom" {
		t.Fatalf("a stream with no command is still evidence: %q", got)
	}
}

// ONLY THE TAIL: a tool's failure is at the end of its output.
func TestExecEvidenceKeepsOnlyTheTail(t *testing.T) {
	var lines []string
	for i := range 50 {
		lines = append(lines, "line"+string(rune('A'+i%26)))
	}
	lines[49] = "the actual error"
	got := execEvidence(nil, "", strings.Join(lines, "\n"))
	if n := strings.Count(got, "\n") + 1; n != execEvidenceLines {
		t.Fatalf("want %d lines, got %d: %q", execEvidenceLines, n, got)
	}
	if !strings.HasSuffix(got, "the actual error") {
		t.Fatalf("the last line must survive: %q", got)
	}
}

// AN ERROR THAT IS NOT AN EXEC FAILURE IS ANSWERED UNCHANGED, the same value.
func TestANonExecErrorIsUnchanged(t *testing.T) {
	err := errors.New("engine went away")
	if got := withExecEvidence(err); got != err {
		t.Fatalf("want the same error back, got %v", got)
	}
	if withExecEvidence(nil) != nil {
		t.Fatal("no error is no error")
	}
}

// THROUGH THE ENGINE: an exec in front of the atom's own fails, and the
// atom's could-not-run says what that exec printed rather than only
// "exit code: N". The paper engine attaches an exec error's streams for the
// signal range (engine_fake_test.go's execError), which is the same
// ExecError a code-1 setup failure arrives as on the cluster.
func TestAnAtomThatNeverRanSaysWhatTheFailedExecPrinted(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"go","mod","download"`, 137)
	engine.stderr(`"go","mod","download"`, "go: reading example.com/x: 404 Not Found")
	v := registry["go:test"](t.Context(), newRun(dag.Directory(), "", ""))
	if v.State != 2 || !strings.Contains(v.Reason, "never ran") || !strings.Contains(v.Reason, "404 Not Found") {
		t.Fatalf("the could-not-run must carry the failed exec's stderr: %+v", v)
	}
}

// THE EXEC ERROR SURVIVES THE WRAP: the rust mutation atom reads a killed
// run's streams off it with errors.As, so a signal-range kill must still be
// graded on what it printed (state 1 or 0 from the outcomes, never "never ran").
func TestAKilledExecIsStillReadableThroughTheWrap(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"go","vet"`, 137)
	engine.stderr(`"go","vet"`, "killed")
	_, _, err := outputBoth(t.Context(), dag.Container().From("x").WithExec([]string{"go", "vet"}, anyExit))
	if err == nil || !strings.Contains(err.Error(), "killed") || !strings.Contains(err.Error(), "exit code: 137") {
		t.Fatalf("the wrapped error must keep the engine's message and add the streams: %v", err)
	}
	var ex *dagger.ExecError
	if !errors.As(err, &ex) || ex.ExitCode != 137 || ex.Stderr != "killed" {
		t.Fatalf("errors.As must still find the ExecError through the wrap: %#v", ex)
	}
}

// AN EXEC FAILURE THAT CARRIED NOTHING IS ANSWERED AS THE ENGINE SAID IT — no
// trailing blank line where the evidence would have gone.
func TestAnExecFailureWithNoStreamsIsUnchanged(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"go","vet"`, 137)
	_, _, err := output(t.Context(), dag.Container().From("x").WithExec([]string{"go", "vet"}, anyExit))
	if err == nil || err.Error() != "exit code: 137" {
		t.Fatalf("want the engine's message alone, got %q", err)
	}
}
