package fleetconsumedeventsemitted

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

func TestConsumedEventsEmitted(t *testing.T) {
	consumer := map[string]string{"c.py": "if ev[\"event_type\"] == \"gone_type\":\n    pass\n"}
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		{"a tree that consumes no literal event_type has no question", map[string]string{"c.py": "x = 1"}, 0, pass, []string{"consumes no literal event_type"}},
		{"a type this tree emits itself needs no door",
			map[string]string{"c.py": "if ev[\"event_type\"] == \"t\":\n    pass\nemit({\"event_type\": \"t\"})\n"}, 0, pass, []string{"all emitted by this tree"}},
		{"a fleet no door answers for is a 2, not a guess", consumer, 2, cannot, []string{"CANNOT RUN"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := treeIn(t, tc.files)
			in.Door = deadDoor(t)
			expect(t, run(t, in), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a source file that will not read is a 2", func(t *testing.T) {
		in := treeIn(t, map[string]string{"a": "x"})
		in.Files = append(in.Files, "gone.py")
		expect(t, run(t, in), stateOf(2), cannot, "gone.py (", "would not read")
	})
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		in := missingRoot(t)
		in.FilesErr = errBoom
		expect(t, run(t, in), stateOf(2), cannot, "the tree would not enumerate")
	})
}

// scanDoor serves the three door reads a fleet scan makes.
func scanDoor(t *testing.T, repos map[string]map[string]string) checks.Door {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		repo := r.URL.Query().Get("repo")
		switch r.URL.Path {
		case "/custody/repos":
			names := []string{}
			for k := range repos {
				names = append(names, k)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"repos": names})
		case "/tree":
			entries := []map[string]any{}
			for p, b := range repos[repo] {
				entries = append(entries, map[string]any{"path": p, "mode": "0100644", "size": len(b)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"entries": entries})
		case "/archive":
			_, _ = io.WriteString(w, repos[repo][r.URL.Query().Get("path")])
		}
	}))
	t.Cleanup(srv.Close)
	return checks.Door{Base: srv.URL, Client: srv.Client()}
}

func TestConsumedEventsEmittedScansTheFleetForATypeItDoesNotEmit(t *testing.T) {
	in := treeIn(t, map[string]string{"hook.py": "if ev[\"event_type\"] == \"theirs\":\n    pass\n"})
	in.Door = scanDoor(t, map[string]map[string]string{"rob/producer": {"emit.py": "emit({\"event_type\": \"theirs\"})\n"}})
	expect(t, run(t, in), stateOf(0), pass, "every one has an emitter (1 outside this tree)")

	in = treeIn(t, map[string]string{"hook.py": "if ev[\"event_type\"] == \"orphan\":\n    pass\n"})
	in.Door = scanDoor(t, map[string]map[string]string{"rob/producer": {"emit.py": "emit({\"event_type\": \"something_else\"})\n"}})
	expect(t, run(t, in), stateOf(1), findings, "'orphan' is consumed (hook.py)", "no source in the fleet emits it")
}

func TestConsumedEventsEmittedCannotRunWhenTheFleetIsUnlisted(t *testing.T) {
	in := treeIn(t, map[string]string{"hook.py": "if ev[\"event_type\"] == \"theirs\":\n    pass\n"})
	in.Door = doorOf(t, nil)
	expect(t, run(t, in), stateOf(2), cannot, "CANNOT RUN", "partial scan is a guess")
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
	if Def.Desc != "Every event_type this repo consumes has at least one live emitter in the fleet, so a retired producer cannot leave a reader silent." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if !slices.Equal(Def.Needs, nil) {
		t.Errorf("needs %v", Def.Needs)
	}
}
