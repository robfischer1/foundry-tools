package diescanaryvisibility

import (
	"context"
	"os"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
)

// TestUT_485_TheDeclarationIsTheRowItReplaced: the Def carries the identity the
// catalogue row did, and validates.
func TestUT_485_TheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrepush || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "none" || Def.Surface != "any:**" || strings.Join(Def.Needs, ",") != "tool:opa" {
		t.Errorf("%+v", Def)
	}
}

// chaosData is a bundle whose first curated chaos verb is "graph_x".
const chaosData = `{"authz_audience":{"star_only":{"chaos":["graph_x","graph_y"]}}}`

// allowedFor answers a probe with the verbs its principal may see, reading the
// input file opa is pointed at, so the test holds that the file carries the
// principal and the canary.
func allowedFor(session, star string) func(*testing.T) func(atoms.Cmd) (string, int) {
	return func(t *testing.T) func(atoms.Cmd) (string, int) {
		return func(c atoms.Cmd) (string, int) {
			body, err := os.ReadFile(c.Args[4])
			if err != nil {
				t.Errorf("the probe input %q is not readable while opa runs: %v", c.Args[4], err)
			}
			if !strings.Contains(string(body), `"graph_x"`) {
				t.Errorf("the probe does not ask about the canary: %s", body)
			}
			who := session
			if strings.Contains(string(body), `"type":"star"`) {
				who = star
			}
			return `{"result":[{"expressions":[{"value":` + who + `}]}]}`, 0
		}
	}
}

// fixed is a probe that answers the same whoever asks.
func fixed(out string, code int) func(*testing.T) func(atoms.Cmd) (string, int) {
	return func(*testing.T) func(atoms.Cmd) (string, int) { return atomtest.Const0(out, code) }
}

func TestAssertsBothDirections(t *testing.T) {
	pinned := atomtest.PinnedOpa()
	run := func(t *testing.T, data string, eval func(*testing.T) func(atoms.Cmd) (string, int)) (checks.Verdict, *atomtest.Fake) {
		var built string
		in, f := atomtest.ToolTree(t, atomtest.DieTree(nil), nil)
		f.Answer = func(c atoms.Cmd) (string, int) {
			switch {
			case c.Name == "tar":
				return data, 0
			case c.Name == "opa" && c.Args[0] == "build":
				built = c.Args[4]
			case c.Name == "opa" && c.Args[0] == "eval":
				if c.Args[1] != "-b" || c.Args[2] != built {
					t.Errorf("the bundle built at %q was interrogated as %q", built, atomtest.Flagged(c))
				}
				return eval(t)(c)
			}
			return atomtest.OpaScript{Version: pinned}.Answer(c)
		}
		return check(context.Background(), in), f
	}
	for _, tc := range []struct {
		name    string
		data    string
		eval    func(*testing.T) func(atoms.Cmd) (string, int)
		state   checks.State
		result  string
		needles []string
	}{
		{"hidden from a session, visible to a star", chaosData, allowedFor(`["search"]`, `["search","graph_x"]`), checks.StatePass, atomtest.Pass,
			[]string{"the built bundle still hides what it should", "canary: graph_x"}},
		{"visible to a session is the fail-open", chaosData, allowedFor(`["search","graph_x"]`, `["search","graph_x"]`), checks.StateFindings, atomtest.Findings, []string{"VISIBLE to a session principal"}},
		{"invisible to a star narrowed both audiences", chaosData, allowedFor(`["search"]`, `["search"]`), checks.StateFindings, atomtest.Findings, []string{"INVISIBLE to a star principal"}},
		{"a bundle with no chaos row did not keep its roster", `{"authz_audience":{"star_only":{}}}`, fixed("", 0), checks.StateFindings, atomtest.Findings, []string{"chaos has no star_only row"}},
		{"a probe that did not complete", chaosData, fixed("rego_error", 2), checks.StateCannotRun, atomtest.Cannot,
			[]string{"the session-principal probe against the built bundle did not evaluate", "rego_error"}},
		{"a probe that would not start", chaosData, fixed("opa: gone", -1), checks.StateCannotRun, atomtest.Cannot, []string{"opa: gone"}},
		{"a probe that answers a value that is not a list", chaosData, fixed(`{"result":[{"expressions":[{"value":"x"}]}]}`, 0), checks.StateCannotRun, atomtest.Cannot, []string{"not a list of verbs"}},
		{"a probe that answers nothing", chaosData, fixed("{}", 0), checks.StateCannotRun, atomtest.Cannot, []string{"no result[0]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, _ := run(t, tc.data, tc.eval)
			atomtest.Expect(t, v, tc.state, tc.result, tc.needles...)
		})
	}
	t.Run("the star probe is not asked when the session probe failed", func(t *testing.T) {
		_, f := run(t, chaosData, fixed("rego_error", 2))
		evals := 0
		for _, c := range f.Calls {
			if c.Name == "opa" && c.Args[0] == "eval" {
				evals++
			}
		}
		if evals != 1 {
			t.Errorf("%d probes ran after the first failed", evals)
		}
	})
	t.Run("the bundle and the probe inputs are removed", func(t *testing.T) {
		var paths []string
		in, f := atomtest.ToolTree(t, atomtest.DieTree(nil), nil)
		f.Answer = func(c atoms.Cmd) (string, int) {
			if c.Name == "opa" && c.Args[0] == "build" {
				paths = append(paths, c.Args[4])
				if err := os.WriteFile(c.Args[4], []byte("bundle"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if c.Name == "opa" && c.Args[0] == "eval" {
				paths = append(paths, c.Args[4])
				return allowedFor(`["search"]`, `["search","graph_x"]`)(t)(c)
			}
			if c.Name == "tar" {
				return chaosData, 0
			}
			return atomtest.OpaScript{Version: pinned}.Answer(c)
		}
		atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass)
		if len(paths) != 3 {
			t.Fatalf("expected the bundle and two probe inputs, got %v", paths)
		}
		for _, p := range paths {
			if _, err := os.Stat(p); err == nil {
				t.Errorf("%s was left behind", p)
			}
		}
	})
	t.Run("two canary runs at once never share a bundle", func(t *testing.T) {
		seen := map[string]bool{}
		for range 2 {
			in, f := atomtest.ToolTree(t, atomtest.DieTree(nil), nil)
			f.Answer = func(c atoms.Cmd) (string, int) {
				if c.Name == "opa" && c.Args[0] == "build" {
					if seen[c.Args[4]] {
						t.Errorf("the bundle path %s was used twice", c.Args[4])
					}
					seen[c.Args[4]] = true
				}
				return atomtest.OpaScript{Version: pinned}.Answer(c)
			}
			check(context.Background(), in)
		}
	})
	t.Run("a probe input that cannot be written is a could-not-run", func(t *testing.T) {
		// A scratch directory that sits under a file can never be made.
		blocker := t.TempDir() + "/file"
		if err := os.WriteFile(blocker, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TMPDIR", blocker+"/sub")
		in, _ := atomtest.ToolTree(t, atomtest.DieTree(nil), func(c atoms.Cmd) (string, int) {
			if c.Name == "tar" {
				return chaosData, 0
			}
			return atomtest.OpaScript{Version: pinned}.Answer(c)
		})
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the session-principal probe against the built bundle did not evaluate", "not a directory")
	})
	for name, c := range map[string]struct {
		session string
		why     string
	}{
		"a session that sees only the canary":          {`["graph_x"]`, "VISIBLE to a session principal"},
		"a session that sees nothing, not even search": {`[]`, "VISIBLE to a session principal"},
		"a session that sees search and more":          {`["search","graph_y"]`, "VISIBLE to a session principal"},
	} {
		t.Run(name+" is not a curated roster", func(t *testing.T) {
			v, _ := run(t, chaosData, allowedFor(c.session, `["search","graph_x"]`))
			atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, c.why)
		})
	}
	t.Run("a star probe that did not complete is named", func(t *testing.T) {
		v, _ := run(t, chaosData, func(t *testing.T) func(atoms.Cmd) (string, int) {
			ok := allowedFor(`["search"]`, `["search","graph_x"]`)(t)
			return func(c atoms.Cmd) (string, int) {
				if body, _ := os.ReadFile(c.Args[4]); strings.Contains(string(body), `"type":"star"`) {
					return "rego_error", 2
				}
				return ok(c)
			}
		})
		atomtest.Expect(t, v, checks.StateCannotRun, atomtest.Cannot, "the star-principal probe against the built bundle did not evaluate", "rego_error")
		if strings.Contains(v.Reason, "session-principal") {
			t.Errorf("the session probe was blamed: %s", v.Reason)
		}
	})
	t.Run("an opa that is not the pinned one is a 2 and no bundle is built", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, atomtest.DieTree(nil), atomtest.OpaScript{Version: "Version: 0.1.0"}.Answer)
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "does not answer", "A policy suite that never ran")
		if len(f.Calls) != 1 {
			t.Errorf("a bundle was built with the wrong opa: %v", f.Ran())
		}
	})
	t.Run("a build that did not produce a bundle is a 2 and nothing is probed", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, atomtest.DieTree(nil),
			atomtest.OpaScript{Version: pinned, On: map[string]func(atoms.Cmd) (string, int){"build": atomtest.Const0("rego_error", 1)}}.Answer)
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "opa build did not produce a bundle", "opa build exited 1")
		for _, c := range f.Calls {
			if c.Name == "opa" && c.Args[0] == "eval" {
				t.Errorf("a bundle that was not built was interrogated")
			}
		}
	})
	t.Run("a bundle with no data.json is a finding and nothing is probed", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, atomtest.DieTree(nil), func(c atoms.Cmd) (string, int) {
			if c.Name == "tar" {
				return "tar: /data.json: Not found in archive", 2
			}
			return atomtest.OpaScript{Version: pinned}.Answer(c)
		})
		atomtest.Expect(t, check(context.Background(), in), checks.StateFindings, atomtest.Findings, "carries NO data.json member at all")
		for _, c := range f.Calls {
			if c.Name == "opa" && c.Args[0] == "eval" {
				t.Errorf("a bundle with no data was interrogated")
			}
		}
	})
	t.Run("off the die's source it is absent and opa is not touched", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, map[string]string{"main.go": "x"}, nil)
		atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Absent, "this tree is not the policy die's source")
		if len(f.Calls) != 0 {
			t.Errorf("an absent atom touched %v", f.Ran())
		}
	})
}
