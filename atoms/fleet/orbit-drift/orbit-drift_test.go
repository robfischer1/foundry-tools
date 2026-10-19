package fleetorbitdrift

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

const contractBody = "verbs = [\"a\"]\n"

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func orbitToml(contract, digest string) string {
	return "[[consumes]]\nfrom = \"chaos\"\ncontract = \"" + contract + "\"\ndigest = \"" + digest + "\"\n"
}

func TestOrbitDrift(t *testing.T) {
	door := map[string]string{"foundry/foundry-dies orbits/chaos-x.toml": contractBody}
	for _, tc := range []struct {
		name    string
		files   map[string]string
		dead    bool
		state   int
		result  string
		needles []string
	}{
		{"no orbit.toml declares no seams", map[string]string{"a": "x"}, false, 0, absent, []string{"no orbit.toml in this tree"}},
		{"a digest that matches agrees", map[string]string{"orbit.toml": orbitToml("chaos-x", digestOf(contractBody))}, false, 0, pass,
			[]string{"1 seam(s) agree"}},
		{"a digest that moved is drift", map[string]string{"orbit.toml": orbitToml("chaos-x", "sha256:00")}, false, 1, findings,
			[]string{"hashes to " + digestOf(contractBody)}},
		{"an edge that pins nothing is a finding, not a pass", map[string]string{"orbit.toml": orbitToml("chaos-x", "")}, false, 1, findings,
			[]string{"carries no digest"}},
		{"a door nothing answers at is a 2", map[string]string{"orbit.toml": orbitToml("chaos-x", "sha256:00")}, true, 2, cannot,
			[]string{"the door is unreachable"}},
		{"an orbit.toml that will not parse is a 2", map[string]string{"orbit.toml": "= not toml"}, false, 2, cannot, []string{"did not parse"}},
		{"an orbit.toml that will not read is a 2", map[string]string{"orbit.toml/x": "x"}, false, 2, cannot, []string{"orbit.toml did not parse"}},
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
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		expect(t, run(t, missingRoot(t)), stateOf(2), cannot, "the tree would not enumerate")
	})
}

// edges declares one consumed edge per (peer, contract, digest) triple.
func edges(es ...[3]string) string {
	var b strings.Builder
	for _, e := range es {
		b.WriteString("[[consumes]]\nfrom = \"" + e[0] + "\"\n")
		if e[1] != "" {
			b.WriteString("contract = \"" + e[1] + "\"\n")
		}
		if e[2] != "" {
			b.WriteString("digest = \"" + e[2] + "\"\n")
		}
	}
	return b.String()
}

func TestOrbitDriftLadderMatchesTheScriptItReplaced(t *testing.T) {
	good := digestOf(contractBody)
	has := map[string]string{"foundry/foundry-dies orbits/c.toml": contractBody}
	for _, tc := range []struct {
		name    string
		orbit   string
		door    map[string]string
		state   int
		needles []string
	}{
		{"empty", "# no seams\n", nil, 0, []string{"declares no seams"}},
		{"unparseable", "this is = = not toml", nil, 2, []string{"orbit.toml did not parse"}},
		{"no contract named", edges([3]string{"hades", "", ""}), nil, 1,
			[]string{"orbit-drift: consumes hades names no contract", "compared nothing"}},
		{"agrees", edges([3]string{"hades", "c", good}), has, 0, []string{"1 seam(s) agree"}},
		{"digest moved", edges([3]string{"hades", "c", "sha256:old"}), has, 1,
			[]string{"hades: contract 'c' hashes to " + good + ", orbit.toml records sha256:old", "Re-lay orbit.toml from the die"}},
		{"no digest", edges([3]string{"hades", "c", ""}), has, 1, []string{"contract 'c' carries no digest", "pins nothing"}},
		{"contract absent from the die", edges([3]string{"hades", "gone", good}), nil, 1,
			[]string{"names contract 'gone', which is not in foundry-dies/orbits"}},
		{"html is not a contract", edges([3]string{"hades", "c", good}),
			map[string]string{"foundry/foundry-dies orbits/c.toml": "<html>login</html>"}, 2,
			[]string{"did not answer a TOML document", "not a contract that disagrees"}},
		{"not utf-8", edges([3]string{"hades", "c", good}),
			map[string]string{"foundry/foundry-dies orbits/c.toml": "verbs = \"\xff\"\n"}, 2, []string{"did not answer a TOML document", "UTF-8"}},
		{"toml without verbs", edges([3]string{"hades", "c", good}),
			map[string]string{"foundry/foundry-dies orbits/c.toml": "x = 1\n"}, 2, []string{"carries no verbs key"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := treeIn(t, map[string]string{"orbit.toml": tc.orbit})
			in.Door = doorOf(t, tc.door)
			v := run(t, in)
			if v.State != tc.state {
				t.Fatalf("state %d, want %d:\n%s", v.State, tc.state, v.Reason)
			}
			said := v.Reason + "\n" + strings.Join(v.Logs, "\n")
			for _, n := range tc.needles {
				if !strings.Contains(said, n) {
					t.Errorf("lacks %q:\n%s", n, said)
				}
			}
		})
	}
}

// Edges are read from both directions, and from both TOML spellings.
func TestOrbitDriftReadsEveryEdgeInBothDirectionsAndSpellings(t *testing.T) {
	in := treeIn(t, map[string]string{
		"orbit.toml": "consumes = [{ from = \"a\", contract = \"one\", digest = \"" + digestOf(contractBody) + "\" }]\n" +
			"[[produces]]\nto = \"b\"\ncontract = \"two\"\ndigest = \"" + digestOf(contractBody) + "\"\n" +
			"[[produces]]\ncontract = \"three\"\n",
	})
	in.Door = doorOf(t, map[string]string{
		"foundry/foundry-dies orbits/one.toml": contractBody, "foundry/foundry-dies orbits/two.toml": contractBody, "foundry/foundry-dies orbits/three.toml": contractBody,
	})
	expect(t, run(t, in), stateOf(1), findings, "?: contract 'three' carries no digest")
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
	if Def.Desc != "This repo's declared seams agree with the canonical contracts in foundry-dies/orbits." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if !slices.Equal(Def.Needs, nil) {
		t.Errorf("needs %v", Def.Needs)
	}
}
