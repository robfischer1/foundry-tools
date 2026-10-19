package fleetrenovatelockstep

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// put writes a file, making its directories.
func put(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// treeIn writes files to a directory and answers the In an atom is handed over
// it, WITHOUT git: the population is the file list, as Collect would build it
// for a tree with nothing ignored.
func treeIn(t *testing.T, files map[string]string) atomkit.In {
	t.Helper()
	dir := t.TempDir()
	var names []string
	for name, body := range files {
		put(t, dir, name, body)
		names = append(names, name)
	}
	sort.Strings(names)
	return atomkit.In{
		Root: dir, Files: checks.GatePopulation(names), Committable: names, Tracked: names,
		Now: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
	}
}

// run runs the atom over an input.
func run(t *testing.T, in atomkit.In) checks.Verdict {
	t.Helper()
	return check(context.Background(), in)
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

var pass = checks.StatePass.String()

var findings = checks.StateFindings.String()

var cannot = checks.StateCannotRun.String()

const absent = "absent"

// stateOf is a table's plain int as the catalogue's State.
func stateOf(n int) checks.State { return checks.State(n) }

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
	return checks.Door{Base: base, NoRetry: true}
}

// missingRoot is an input over a directory that is not there: the atoms that
// open with the root's listing settle 2.
func missingRoot(t *testing.T) atomkit.In {
	t.Helper()
	return atomkit.In{Root: t.TempDir() + "/not-there", Now: time.Now()}
}

var errBoom = errors.New("boom")

func TestRenovateLockstep(t *testing.T) {
	ref := "ghcr.io/renovatebot/renovate:43.261.4@sha256:" + strings.Repeat("a", 64)
	other := "ghcr.io/renovatebot/renovate:43.262.0@sha256:" + strings.Repeat("b", 64)
	flux := map[string]string{"foundry/flux renovate/renovate.yaml": "image: " + ref + "\n"}
	pin := func(r string) map[string]string {
		return map[string]string{"internal/checks/images.go": "const (\n\tImageRenovate = \"" + r + "\"\n)\n"}
	}
	for _, tc := range []struct {
		name    string
		files   map[string]string
		dead    bool
		state   int
		result  string
		needles []string
	}{
		{"a tree that pins no renovate is absent", map[string]string{"a": "x"}, false, 0, absent, []string{"nothing to hold in lockstep"}},
		{"a pin equal to flux main agrees", pin(ref), false, 0, pass, []string{"1 pin(s) agree"}},
		{"a pin that differs from flux main is out of lockstep", pin(other), false, 1, findings, []string{"land flux first"}},
		{"flux that could not be read is a 2", pin(ref), true, 2, cannot, []string{"the door is unreachable"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := treeIn(t, tc.files)
			in.Door = doorOf(t, flux)
			if tc.dead {
				in.Door = deadDoor(t)
			}
			expect(t, run(t, in), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a pin file that will not read is a 2", func(t *testing.T) {
		in := treeIn(t, map[string]string{"a": "x"})
		in.Files = append(in.Files, "renovate/renovate.yaml") // listed, and not on disk
		expect(t, run(t, in), stateOf(2), cannot, "renovate/renovate.yaml would not read")
	})
}

func TestRenovateLockstepRefusesATreeThatWouldNotEnumerate(t *testing.T) {
	in := missingRoot(t)
	in.FilesErr = errBoom
	expect(t, run(t, in), stateOf(2), cannot, "the tree would not enumerate")
}

// A tree that IS flux is judged on itself; the door is not asked.
func TestRenovateLockstepJudgesFluxOnItsOwnTree(t *testing.T) {
	ref := "ghcr.io/renovatebot/renovate:43.261.4@sha256:" + strings.Repeat("c", 64)
	in := treeIn(t, map[string]string{"renovate/renovate.yaml": "image: " + ref + "\nimage: " + ref + "\n"})
	in.Door = deadDoor(t)
	expect(t, run(t, in), stateOf(0), pass, "2 pin(s) agree with the Renovate "+ref)
}

// ImageRenovate is held to flux main's ref, read from the door by name.
func TestRenovateLockstepHoldsTheImageToFluxMain(t *testing.T) {
	pinned := "ghcr.io/renovatebot/renovate:43.261.4@sha256:" + strings.Repeat("a", 64)
	deployed := "ghcr.io/renovatebot/renovate:43.262.0@sha256:" + strings.Repeat("b", 64)
	in := treeIn(t, map[string]string{"internal/checks/images.go": "const (\n\tImageRenovate = \"" + pinned + "\"\n)\n"})
	in.Door = doorOf(t, map[string]string{"foundry/flux renovate/renovate.yaml": "image: " + deployed + "\n"})
	expect(t, run(t, in), stateOf(1), findings, "ImageRenovate "+pinned+", but Renovate runs "+deployed+" (foundry/flux main renovate/renovate.yaml)")
}

// The declaration is the catalogue row it replaced: the same stage, group, text and
// fork lane, and the needs the split table gives it.
func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrepush || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "none" || Def.NotOnForks != "" || Def.SubsumedBy != "" || Def.NeedsManifest {
		t.Errorf("%+v", Def)
	}
	if Def.Desc != "The Renovate image this repo pins (ImageRenovate) equals the one foundry/flux deploys, and flux's two containers agree." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if !slices.Equal(Def.Needs, nil) {
		t.Errorf("needs %v", Def.Needs)
	}
}
