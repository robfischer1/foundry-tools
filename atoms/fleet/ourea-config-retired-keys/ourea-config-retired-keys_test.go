package fleetoureaconfigretiredkeys

import (
	"context"
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
	return checks.Door{Base: base}
}

// missingRoot is an input over a directory that is not there: the atoms that
// open with the root's listing settle 2.
func missingRoot(t *testing.T) atomkit.In {
	t.Helper()
	return atomkit.In{Root: t.TempDir() + "/not-there", Now: time.Now()}
}

const ourea = "ourea internal/config/retired-keys.json"

func oureaMap(keys string) map[string]string {
	return map[string]string{"prime/ourea-config.yaml": "kind: ConfigMap\ndata:\n  config.toml: |\n" + keys}
}

func TestOureaConfigRetiredKeys(t *testing.T) {
	list := map[string]string{"rob/" + ourea: `{"old_key": "went in #12"}`}
	for _, tc := range []struct {
		name    string
		files   map[string]string
		door    map[string]string
		dead    bool
		state   int
		result  string
		needles []string
	}{
		{"not the flux tree is absent", map[string]string{"main.go": "x"}, list, false, 0, absent, []string{"carries no prime/"}},
		{"prime without the ConfigMap means it moved, which is not absence", map[string]string{"prime/other.yaml": "x"}, list, false, 2, cannot,
			[]string{"is not in it: the door's ConfigMap moved"}},
		{"a ConfigMap that names no retired key passes", oureaMap("    live_key = 1\n"), list, false, 0, pass,
			[]string{"config.toml names none of ourea's 1 retired keys"}},
		{"a retired key in a comment is not a key", oureaMap("    # old_key = 1 was removed\n    live = 1\n"), list, false, 0, pass, nil},
		{"a retired key is a finding", oureaMap("    old_key = 2\n    live_key = 1\n"), list, false, 1, findings,
			[]string{"carries 1 key(s) the door no longer reads", "old_key", "went in #12"}},
		{"a ConfigMap with no door config will not parse", map[string]string{"prime/ourea-config.yaml": "kind: ConfigMap\ndata: {}\n"}, list, false, 2, cannot,
			[]string{"would not parse"}},
		{"a door that does not answer leaves the check without its list", oureaMap("    live_key = 1\n"), nil, true, 2, cannot,
			[]string{"nothing says which keys are retired"}},
		{"a door that answers 404 is not a list", oureaMap("    live_key = 1\n"), map[string]string{}, false, 2, cannot,
			[]string{"HTTP 404"}},
		{"an empty list grades against nothing", oureaMap("    live_key = 1\n"), map[string]string{"rob/" + ourea: `{}`}, false, 2, cannot,
			[]string{"decoded empty"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := treeIn(t, tc.files)
			in.Door = doorOf(t, tc.door)
			if tc.dead {
				in.Door = deadDoor(t)
			}
			v := run(t, in)
			expect(t, v, stateOf(tc.state), tc.result, tc.needles...)
			if tc.dead && strings.Contains(v.Reason, "HTTP 0") {
				t.Errorf("a door nothing answered at is an error, not a status of 0:\n%s", v.Reason)
			}
		})
	}
	t.Run("a prime that will not list", func(t *testing.T) {
		in := treeIn(t, map[string]string{"main.go": "x"})
		// prime is a file, not a directory: it is in the root listing and cannot list.
		put(t, in.Root, "prime", "not a directory")
		expect(t, run(t, in), stateOf(2), cannot, "prime/ would not enumerate")
	})
	t.Run("a ConfigMap that will not read", func(t *testing.T) {
		in := treeIn(t, map[string]string{"prime/ourea-config.yaml/x": "x"})
		expect(t, run(t, in), stateOf(2), cannot, "would not read")
	})
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		expect(t, run(t, missingRoot(t)), stateOf(2), cannot, "the tree would not enumerate")
	})
}

const retiredList = `{
  "gate_module_pin": "the door resolves gate_module_repo's main at dispatch; set gate_module / gate_module_repo instead and delete this key",
  "record_volume_size": "there is no record volume to size - delete this key",
  "pin_hook_url": "the Forgejo ` + "`issues`" + ` webhook is gone - delete this key"
}`

const listPath = "rob/" + ourea

const cleanConfig = `forgejo_url = "http://forgejo:30142"
gate_job_namespace = "prime"
`

// configMap wraps a TOML body the way flux's file does: a ConfigMap whose
// data["config.toml"] is a block scalar.
func configMap(body string) string {
	var b strings.Builder
	b.WriteString("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: ourea-config\n  namespace: prime\ndata:\n  config.toml: |\n")
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		b.WriteString("    " + line + "\n")
	}
	return b.String()
}

// fluxTree is the fleet's flux checkout as the atom sees it, with the door
// serving ourea's published list.
func fluxIn(t *testing.T, toml string) atomkit.In {
	in := treeIn(t, map[string]string{"prime/ourea-config.yaml": configMap(toml)})
	in.Door = doorOf(t, map[string]string{listPath: retiredList})
	return in
}

// THE FALSE RED THIS ATOM WAS BUILT AROUND. flux's real ConfigMap is 28 set
// keys, none retired, and 7 of the 11 retired names appear in its comments
// anyway, each inside prose explaining why that key went. A text scan reds all
// of them.
func TestARetiredKeyNamedOnlyInComments(t *testing.T) {
	in := fluxIn(t, `# There is no pin to declare here - gate_module_pin was retired with the
# declared-pin apparatus, and a copy of that key is a WARN at boot.
forgejo_url = "http://forgejo:30142"
# record_volume_class, record_volume_size and record_collect_image went with
# the per-run volume.
gate_job_namespace = "prime"
#pin_hook_url = "http://casper:9000/pin"
`)
	expect(t, run(t, in), stateOf(0), pass)
}

// A key of the same name under a table is a DIFFERENT key, and ourea's retired
// ones are all top-level.
func TestARetiredNameInsideATableIsNotTheRetiredKey(t *testing.T) {
	expect(t, run(t, fluxIn(t, "forgejo_url = \"http://forgejo:30142\"\n\n[hooks]\npin_hook_url = \"http://casper:9000/pin\"\n")), stateOf(0), pass)
}

// The finding carries ourea's OWN sentence about the key, and says the line is
// inert: an author who reads "retired" as "broken" reaches for a rollback.
func TestTheFindingCarriesOureasReasonAndTheFix(t *testing.T) {
	out := run(t, fluxIn(t, cleanConfig+"gate_module_pin = \"foundry-tools@f0566e9b\"\n")).Reason
	for _, want := range []string{
		"gate_module_pin",                    // the key
		"set gate_module / gate_module_repo", // ourea's own guidance, carried verbatim
		"INERT",                              // what it is not
		"FIX: delete the named key",          // what to do
		"internal/config/retired-keys.json",  // where the list came from
	} {
		if !strings.Contains(out, want) {
			t.Errorf("finding does not mention %q:\n%s", want, out)
		}
	}
}

// Several keys are reported together and in a stable order.
func TestSeveralRetiredKeysAreReportedTogetherAndSorted(t *testing.T) {
	out := run(t, fluxIn(t, cleanConfig+"record_volume_size = \"1Gi\"\npin_hook_url = \"http://casper:9000/pin\"\ngate_module_pin = \"x\"\n")).Reason
	if !strings.Contains(out, "3 key(s)") {
		t.Errorf("the count is wrong:\n%s", out)
	}
	i, j, k := strings.Index(out, "gate_module_pin"), strings.Index(out, "pin_hook_url"), strings.Index(out, "record_volume_size")
	if !(i < j && j < k) {
		t.Errorf("the keys are not sorted (%d, %d, %d):\n%s", i, j, k, out)
	}
}

// A ConfigMap that is not the shape this atom reads is a check that did not
// run, never a ConfigMap with no retired keys in it.
func TestOureaConfigRefusesAFileItCannotParse(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"not yaml", "\tthis: [is\n  not: yaml\n", "not YAML"},
		{"no config.toml", "apiVersion: v1\nkind: ConfigMap\ndata:\n  other.toml: |\n    x = 1\n", `no data["config.toml"]`},
		{"not toml", configMap("this is not = = toml\n"), "not TOML"},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := treeIn(t, map[string]string{"prime/ourea-config.yaml": c.body})
			in.Door = doorOf(t, map[string]string{listPath: retiredList})
			expect(t, run(t, in), stateOf(2), cannot, "CANNOT RUN", "would not parse", c.want)
		})
	}
}

// THE LIST IS THE CHECK. A list the atom cannot use is a check that did not run.
func TestOureaConfigRefusesAListItCannotUse(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"not an object of strings", `["gate_module_pin"]`, "did not decode"},
		{"empty", `{}`, "grading a ConfigMap against no keys is not a check"},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := treeIn(t, map[string]string{"prime/ourea-config.yaml": configMap(cleanConfig)})
			in.Door = doorOf(t, map[string]string{listPath: c.body})
			expect(t, run(t, in), stateOf(2), cannot, "CANNOT RUN", c.want)
		})
	}
}

// The declaration is the catalogue row it replaced: the same stage, group, text and
// fork lane, and the needs the split table gives it.
func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "none" || Def.NotOnForks != "" || Def.SubsumedBy != "" || Def.NeedsManifest {
		t.Errorf("%+v", Def)
	}
	if Def.Desc != "The ourea ConfigMap names no key the door has stopped reading." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if !slices.Equal(Def.Needs, nil) {
		t.Errorf("needs %v", Def.Needs)
	}
}
