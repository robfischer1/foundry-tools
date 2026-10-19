package fleetnodekindsdeclared

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

func TestNodeKindsDeclared(t *testing.T) {
	const schema = "INSERT INTO {schema}.node_kinds (kind, canonical_form, note)\nSELECT k, k, 'bootstrap' FROM unnest(ARRAY[\n    'Memory'\n]) k\n"
	capture := func(kind string) map[string]string {
		return map[string]string{"internal/x/x.go": "package x\nfunc f() { _ = CreateNode(\"" + kind + "\", \"l\") }\n"}
	}
	door := map[string]string{"rob/chaos bigintschema/schema.go": schema}
	for _, tc := range []struct {
		name    string
		files   map[string]string
		dead    bool
		state   int
		result  string
		needles []string
	}{
		{"a tree that captures nothing never asks the door", map[string]string{"internal/x/x.go": "package x\n"}, true, 0, pass, []string{"no node kind is captured"}},
		{"a declared kind passes", capture("Memory"), false, 0, pass, []string{"1 captured kind(s) are all declared"}},
		{"an undeclared kind is a finding", capture("ChronicleChapterX"), false, 1, findings, []string{"'ChronicleChapterX' is captured"}},
		{"a vocabulary that was not read is a 2, never a finding", capture("ChronicleChapterX"), true, 2, cannot,
			[]string{"A vocabulary that was not read declares nothing"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := treeIn(t, tc.files)
			in.Door = doorOf(t, door)
			if tc.dead {
				in.Door = deadDoor(t)
			}
			expect(t, run(t, in), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a pass names the non-Go files it did not read, a finding does not", func(t *testing.T) {
		py := map[string]string{"client/chaos.py": "graph_capture(kind)\n"}
		in := treeIn(t, py)
		in.Door = deadDoor(t)
		expect(t, run(t, in), stateOf(0), pass, "NOT JUDGED: this check reads Go only, and 1 non-Go file(s) look like captures (client/chaos.py)")
		files := capture("ChronicleChapterX")
		files["client/chaos.py"] = py["client/chaos.py"]
		in = treeIn(t, files)
		in.Door = doorOf(t, door)
		if v := run(t, in); v.State != 1 || strings.Contains(v.Reason+strings.Join(v.Logs, "\n"), "NOT JUDGED") {
			t.Errorf("a finding carries no note: state %d\n%s", v.State, v.Reason)
		}
	})
	t.Run("a source file that will not read is a 2", func(t *testing.T) {
		in := treeIn(t, map[string]string{"a": "x"})
		in.Files = append(in.Files, "gone.go")
		expect(t, run(t, in), stateOf(2), cannot, "gone.go (", "would not read")
	})
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		in := missingRoot(t)
		in.FilesErr = errBoom
		expect(t, run(t, in), stateOf(2), cannot, "the tree would not enumerate")
	})
}

func schemaDoor(t *testing.T) checks.Door {
	return doorOf(t, map[string]string{checks.NodeKindsRepo + " " + checks.NodeKindsPath: "INSERT INTO {schema}.node_kinds (kind, canonical_form, note)\n" +
		"SELECT k, k, 'bootstrap' FROM unnest(ARRAY[\n    'Memory'\n]) k\nON CONFLICT (kind) DO NOTHING;\n"})
}

func TestNodeKindsDeclaredLadderOfTheDoor(t *testing.T) {
	capture := func(kind string) map[string]string {
		return map[string]string{"main.go": "package main\nfunc f() { _ = CreateNode(\"" + kind + "\", \"l\") }\n"}
	}
	in := treeIn(t, capture("Mystery"))
	in.Door = schemaDoor(t)
	expect(t, run(t, in), stateOf(1), findings, "'Mystery' is captured (main.go:2)", "not in node_kinds")
	// a door that answers 404 for the vocabulary is not a vocabulary
	in = treeIn(t, capture("Memory"))
	in.Door = doorOf(t, nil)
	expect(t, run(t, in), stateOf(2), cannot, "CANNOT RUN", "HTTP 404", "not a finding about this tree")
}

func TestNodeKindsDeclaredIgnoresTestFiles(t *testing.T) {
	in := treeIn(t, map[string]string{"a_test.go": "package a\nfunc f() { _ = CreateNode(\"OnlyInATest\", \"l\") }\n"})
	in.Door = deadDoor(t) // a test's capture is not the star's: the door is not asked
	expect(t, run(t, in), stateOf(0), pass, "no node kind is captured")
}

// The note belongs to a PASS only: a finding already says what it found.
func TestNodeKindsDeclaredNamesUnreadSourcesOnlyOnAPass(t *testing.T) {
	client := "def go(self):\n    return self.create_node(kind)\n"
	in := treeIn(t, map[string]string{"main.go": "package main\nfunc f() { _ = CreateNode(\"Undeclared\", \"l\") }\n", "src/client/_client.py": client})
	in.Door = schemaDoor(t)
	if v := run(t, in); v.State != 1 || strings.Contains(v.Reason+strings.Join(v.Logs, "\n"), "NOT JUDGED") {
		t.Errorf("a finding must not carry the pass's note: %d %s", v.State, v.Reason)
	}
	in = treeIn(t, map[string]string{"main.go": "package main\nfunc f() { _ = CreateNode(\"Memory\", \"l\") }\n", "src/client/_client.py": client})
	in.Door = schemaDoor(t)
	expect(t, run(t, in), stateOf(0), pass, "all declared", "NOT JUDGED", "src/client/_client.py")
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
	if Def.Desc != "Every node kind this repo's Go code captures is declared in chaos's node_kinds, so the foreign key never refuses it at runtime." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if !slices.Equal(Def.Needs, nil) {
		t.Errorf("needs %v", Def.Needs)
	}
}
