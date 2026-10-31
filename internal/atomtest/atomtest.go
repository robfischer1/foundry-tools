// Package atomtest is the test support the atoms that live in their own files
// (atoms/<ns>/<name>) share: a tree on disk, a fake for the programs an atom
// execs, a fake git door, and the assertion every atom's test ends in. It is
// ordinary code and not a _test.go file because each atom is a package of its
// own and a helper two or more of them use is written once.
//
// Every helper takes TB and not *testing.T, and every condition in them goes
// through Assert, so atomtest_test.go can hold each helper to what it reports
// with a recording TB.
package atomtest

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// TB is the part of *testing.T the helpers use.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
	Fatal(args ...any)
	TempDir() string
	Cleanup(func())
}

// The result words a verdict carries, and the one an ABSENT pass reads.
var (
	Pass     = checks.StatePass.String()
	Findings = checks.StateFindings.String()
	Cannot   = checks.StateCannotRun.String()
)

// Absent is the result word of a pass that says nothing was checked.
const Absent = "absent"

// Assert reports a failure when ok is false.
func Assert(t TB, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Errorf(format, args...)
	}
}

// must fails the test on an error.
func must(t TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Put writes a file, making its directories.
func Put(t TB, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, []byte(body), 0o644))
}

// TreeIn writes files to a directory and answers the input an atom is handed
// over it, WITHOUT git: the population is the file list, as the collector would
// build it for a tree with nothing ignored.
func TreeIn(t TB, files map[string]string) atomkit.In {
	t.Helper()
	dir := t.TempDir()
	var names []string
	for name, body := range files {
		Put(t, dir, name, body)
		names = append(names, name)
	}
	sort.Strings(names)
	return atomkit.In{
		Root: dir, Files: checks.GatePopulation(names), Committable: names, Tracked: names,
		Now: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
	}
}

// MissingRoot is an input over a directory that is not there: the atoms that
// open with the root's listing settle 2.
func MissingRoot(t TB) atomkit.In {
	t.Helper()
	return atomkit.In{Root: t.TempDir() + "/not-there", Now: time.Now()}
}

// Expect holds a verdict to its state, its result word (an ABSENT pass reads
// "absent") and the words it must carry. A pass's Reason is only "<id>: PASS"
// and what the atom said rides in its Logs, so the words are looked for in both.
func Expect(t TB, v checks.Verdict, state checks.State, result string, needles ...string) {
	t.Helper()
	Assert(t, v.State == int(state) && v.Result == result, "%s: state %d (%s), want %d (%s)\n%s", v.Atom, v.State, v.Result, state, result, v.Reason)
	said := v.Reason + "\n" + strings.Join(v.Logs, "\n")
	for _, n := range needles {
		Assert(t, strings.Contains(said, n), "%s: reason and logs lack %q:\n%s", v.Atom, n, said)
	}
}

// ErrBoom is the error a seam that fails answers.
var ErrBoom = errors.New("boom")

// FailingFS is the disk, except that listing the named directory fails.
type FailingFS struct {
	fs.FS
	Dir string
}

// ReadDir lists name, or fails for the named directory.
func (f FailingFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == f.Dir {
		return nil, ErrBoom
	}
	return fs.ReadDir(f.FS, name)
}

// DoorOf answers for the git door: "<repo> <path>" to the body it serves; any
// other file is a 404, which is what the door says for a path it does not hold.
func DoorOf(t TB, answers map[string]string) checks.Door {
	t.Helper()
	return DoorBreaking(t, answers, "")
}

// DoorBreaking answers like DoorOf, except that the connection for one file is
// dropped mid-request: the door as a network that failed for that read alone.
func DoorBreaking(t TB, answers map[string]string, breakOn string) checks.Door {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("repo") + " " + r.URL.Query().Get("path")
		if breakOn != "" && key == breakOn {
			// the test server's writer always hijacks
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
			return
		}
		body, ok := answers[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return checks.Door{Base: srv.URL, NoRetry: true}
}

// DeadDoor is a door nothing answers at: connection refused.
func DeadDoor(t TB) checks.Door {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	return checks.Door{Base: base, NoRetry: true}
}

// BrokenLink makes a file a listing shows and nothing can read: a symlink to
// nowhere.
func BrokenLink(t TB, root, rel string) {
	t.Helper()
	p := filepath.Join(root, rel)
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.Symlink(filepath.Join(root, "nowhere"), p))
}

// Ledger is a retired-verbs.toml with one retired verb.
const Ledger = "[retired.old_verb]\nstar = \"x\"\nsuccessor = \"new_verb\"\nretired_by = \"y\"\n"

// StocksTree is the smallest tree the law lanes call foundry-stocks, with extra
// files laid over it.
func StocksTree(extra map[string]string) map[string]string {
	files := map[string]string{
		"kits.toml":                  "[kits.root]\nblocks = [\"forge\"]\n",
		"retired-verbs.toml":         Ledger,
		"governance/blocks/forge.md": "CI logs through `repo_ci_logs`.\n",
		// the registry law:lint grades beside the kits
		"dies.toml":      "[dies.\"tool/x\"]\nsource = \"https://git.example.test/rob/x.git@main\"\npath = \".\"\n",
		"consumers.toml": "[consumers.c]\npath = \"../c\"\nprovider = \"claude\"\n",
		"matrix.toml":    "[skills]\nclaude = \"native\"\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	return files
}
