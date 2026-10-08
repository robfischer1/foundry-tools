package atoms

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// treeIn writes files to a directory and answers the Input an atom is handed
// over it, WITHOUT git: the population is the file list, as Collect would
// build it for a tree with nothing ignored. Atoms that ask the root, a file or a
// glob read the disk; atoms that ask the lists read these.
func treeIn(t *testing.T, files map[string]string) Input {
	t.Helper()
	dir := t.TempDir()
	var names []string
	for name, body := range files {
		put(t, dir, name, body)
		names = append(names, name)
	}
	sort.Strings(names)
	return Input{
		Root: dir, Files: checks.GatePopulation(names), Committable: names, Tracked: names,
		Now: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
	}
}

// runAtom runs a built-in atom by id with its catalogue row, as the runner does.
func runAtom(t *testing.T, id string, in Input) checks.Verdict {
	t.Helper()
	for _, a := range Builtin() {
		if a.ID == id {
			return a.Run(context.Background(), checks.AtomByID(id), in)
		}
	}
	t.Fatalf("%s is not a built-in atom", id)
	return checks.Verdict{}
}

// expect holds a verdict to its state, its result word (an ABSENT pass reads
// "absent") and the words it must carry. A pass's Reason is only "<id>: PASS" —
// what the atom said rides in its Logs — so the words are looked for in both.
func expect(t *testing.T, v checks.Verdict, state checks.State, result string, needles ...string) {
	t.Helper()
	if v.State != int(state) || v.Result != result {
		t.Errorf("%s: state %d (%s), want %d (%s)\n%s", v.Atom, v.State, v.Result, state, result, v.Reason)
	}
	said := v.Reason + "\n" + strings.Join(v.Logs, "\n")
	for _, n := range needles {
		if !strings.Contains(said, n) {
			t.Errorf("%s: reason and logs lack %q:\n%s", v.Atom, n, said)
		}
	}
}

var (
	pass     = checks.StatePass.String()
	findings = checks.StateFindings.String()
	cannot   = checks.StateCannotRun.String()
)

const absent = "absent"

// doorOf answers for the git door: "<repo> <path>" to the body it serves; any
// other file is a 404, which is what the door says for a path it does not hold.
func doorOf(t *testing.T, answers map[string]string) checks.Door {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := answers[r.URL.Query().Get("repo")+" "+r.URL.Query().Get("path")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return checks.Door{Base: srv.URL}
}

// deadDoor is a door nothing answers at: connection refused.
func deadDoor(t *testing.T) checks.Door {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	return checks.Door{Base: base}
}

// missingRoot is an Input over a directory that is not there: the atoms that
// open with the root's listing settle 2.
func missingRoot(t *testing.T) Input {
	t.Helper()
	return Input{Root: t.TempDir() + "/not-there", Now: time.Now()}
}

// stateOf is a table's plain int as the catalogue's State.
func stateOf(n int) checks.State { return checks.State(n) }

var errBoom = errors.New("boom")
