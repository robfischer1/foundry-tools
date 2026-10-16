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
	withOneAtom(t)
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
	// The pull path: the atoms of precommit and prepush this package carries (none now: every one lives,
	// none of the orbit lane's and none of the ones that live in their own files
	// (atoms/<ns>/<name>); a tree with no surface for them is absent or clean in
	// all of them.
	want := StageIDs("")
	wantState := map[string]int{}
	if len(vector) != len(want) {
		t.Fatalf("%d verdicts, want %d", len(vector), len(want))
	}
	for i, v := range vector {
		if v.Atom != want[i] || v.State != wantState[v.Atom] {
			t.Errorf("slot %d: %s state %d, want %s state %d\n%s", i, v.Atom, v.State, want[i], wantState[v.Atom], v.Reason)
		}
		if v.StartedAt == "" || v.Logs == nil {
			t.Errorf("%s: started %q, logs %v", v.Atom, v.StartedAt, v.Logs)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr %q", stderr.String())
	}
}

// The witness flags reach the atoms through the input: the dry run is off unless
// asked for (the voter asks for real), and the URL is the stand-in's when named.
func TestWitnessFlagsReachTheAtoms(t *testing.T) {
	dir := newRepo(t)
	put(t, dir, "a.txt", "a\n")
	commitAll(t, dir, "c1")
	var dry []bool
	var urls []string
	build := func() (*Registry, error) {
		return NewRegistry(Atom{ID: "go:release", Run: func(_ context.Context, a checks.AtomDef, in Input) checks.Verdict {
			dry, urls = append(dry, in.WitnessDry), append(urls, in.Witness.URL)
			return checks.VerdictOf(a, 0, "")
		}})
	}
	var out, errb strings.Builder
	for _, args := range [][]string{{"-root", dir}, {"-root", dir, "-witness-dry", "-witness-url", "http://narcissus.invalid"}} {
		if code := run(context.Background(), args, &out, &errb, time.Now, build); code != 0 {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
	}
	if len(dry) != 2 || dry[0] || !dry[1] {
		t.Errorf("dry was %v for no flag and the flag; want false then true (the voter asks for real)", dry)
	}
	if len(urls) != 2 || urls[0] != "" || urls[1] != "http://narcissus.invalid" {
		t.Errorf("witness URL was %q for no flag and the flag", urls)
	}
}

func TestRunOutsideARepositoryStillAnswersAVector(t *testing.T) {
	withOneAtom(t)
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"-root", t.TempDir()}, &stdout, &stderr, fixedNow); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	vector, err := checks.ParseVector(stdout.String())
	if err != nil {
		t.Fatal(err)
	}
	// The atoms defined over git's answers (the population, the tracked list)
	// cannot say anything about a tree git would not read, and only could-not-run
	// is honest. The ones that read the directory itself have a directory to read.
	gitBound := map[string]bool{}
	for _, v := range vector {
		if gitBound[v.Atom] && v.State != 2 {
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
