package atoms

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/checks"
)

func fixedNow() time.Time { return time.Date(2026, 10, 8, 3, 0, 0, 0, time.UTC) }

func TestRunPrintsTheVectorAndExitsZero(t *testing.T) {
	dir := newRepo(t)
	put(t, dir, "ok.yml", "a: 1\n")
	put(t, dir, "a.py", "x = 1\n")
	put(t, dir, "bad.txt", "<<<<<<< HEAD\n")
	commitAll(t, dir, "root")

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"-root", dir}, &stdout, &stderr, fixedNow)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q: a finding is a result, not a failure", code, stderr.String())
	}
	vector, err := checks.ParseVector(stdout.String())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"fleet:check-yaml", "fleet:check-added-large-files", "fleet:check-merge-conflict", "fleet:stop-justifications"}
	wantState := []int{0, 0, 1, 0}
	if len(vector) != len(want) {
		t.Fatalf("%d verdicts, want %d", len(vector), len(want))
	}
	for i, v := range vector {
		if v.Atom != want[i] || v.State != wantState[i] {
			t.Errorf("slot %d: %s state %d, want %s state %d\n%s", i, v.Atom, v.State, want[i], wantState[i], v.Reason)
		}
		if v.StartedAt == "" || v.Logs == nil {
			t.Errorf("%s: started %q, logs %v", v.Atom, v.StartedAt, v.Logs)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr %q", stderr.String())
	}
}

func TestRunOutsideARepositoryStillAnswersAVector(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"-root", t.TempDir()}, &stdout, &stderr, fixedNow); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	vector, err := checks.ParseVector(stdout.String())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vector {
		if v.State != 2 {
			t.Errorf("%s answered %d about a tree git would not read; only could-not-run is honest", v.Atom, v.State)
		}
	}
}

func TestRunFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"an unknown flag", []string{"-bogus"}},
		{"a malformed duration", []string{"-timeout", "soon"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run(context.Background(), tc.args, &stdout, &stderr, fixedNow); code != 2 {
				t.Errorf("exit %d, want 2", code)
			}
			if stdout.Len() != 0 || stderr.Len() == 0 {
				t.Errorf("a refused invocation prints no vector (%q) and says why (%q)", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunRefusesAnInvalidRegistry(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), nil, &stdout, &stderr, fixedNow, func() (*Registry, error) { return nil, errors.New("duplicate") })
	if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "the registry is invalid: duplicate") {
		t.Errorf("exit %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
}
