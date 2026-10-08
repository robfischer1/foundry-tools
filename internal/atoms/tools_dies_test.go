package atoms

import (
	"os"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// opaScript answers for opa, tar and git. version is what `opa version` says;
// on maps the opa subcommand (test, eval, build) to its answer.
type opaScript struct {
	version string
	vcode   int
	on      map[string]func(Cmd) (string, int)
}

func (s opaScript) answer(c Cmd) (string, int) {
	switch {
	case c.Name == "git":
		return "abc123", 0
	case c.Name == "tar":
		return goodData, 0
	case c.Args[0] == "version":
		return s.version, s.vcode
	}
	if h, ok := s.on[c.Args[0]]; ok {
		return h(c)
	}
	return "", 0
}

func const0(out string, code int) func(Cmd) (string, int) {
	return func(Cmd) (string, int) { return out, code }
}

func TestDiesOpaTest(t *testing.T) {
	const id = "dies:opa-test"
	pinned := "Version: " + checks.OpaVersion
	test := func(out string, code int) map[string]func(Cmd) (string, int) {
		return map[string]func(Cmd) (string, int){"test": const0(out, code)}
	}
	for _, tc := range []struct {
		name    string
		script  opaScript
		ranTest bool
		state   int
		result  string
		needles []string
	}{
		{"a suite with assertions passes", opaScript{version: pinned, on: test("PASS: 12/12", 0)}, true, 0, pass, []string{"12 assertion(s) pass"}},
		{"a zero-test run is refused", opaScript{version: pinned, on: test("", 0)}, true, 2, cannot, []string{"REFUSING a zero-test run"}},
		{"a failing assertion (opa's 2) is a finding", opaScript{version: pinned, on: test("FAIL: 1/3", 2)}, true, 1, findings,
			[]string{"FINDINGS - the rego suite did not come back clean", "FAIL: 1/3"}},
		{"a load error (opa's 1) is a finding", opaScript{version: pinned, on: test("rego_parse_error", 1)}, true, 1, findings, []string{"rego_parse_error"}},
		{"a code opa does not use is a could-not-run", opaScript{version: pinned, on: test("killed", 137)}, true, 2, cannot, []string{"opa exited 137"}},
		{"an opa that would not start never ran", opaScript{version: pinned, on: test("opa: gone", -1)}, true, 2, cannot, []string{"the atom never ran: opa: gone"}},
		{"an opa that is not the pinned one", opaScript{version: "Version: 0.1.0"}, false, 2, cannot, []string{"does not answer", "A policy suite that never ran"}},
		{"an opa that does not run", opaScript{version: "opa: not found", vcode: -1}, false, 2, cannot, []string{"does not run (exit -1)"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := toolTree(t, dieTree(nil), tc.script.answer)
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
			last := f.calls[len(f.calls)-1]
			if tc.ranTest != (flagged(last) == "test policy/ -v") || last.Dir != in.Root {
				t.Errorf("the last call was %q in %q; the suite should have run: %v", flagged(last), last.Dir, tc.ranTest)
			}
		})
	}
	t.Run("off the die's source it is absent and opa is not touched", func(t *testing.T) {
		in, f := toolTree(t, map[string]string{"main.go": "x"}, nil)
		expect(t, runAtom(t, id, in), stateOf(0), absent, "this tree is not the policy die's source")
		if len(f.calls) != 0 {
			t.Errorf("an absent atom touched %v", f.ran())
		}
	})
}

const denyOne = `{"result":[{"expressions":[{"value":["no stellar principal"]}]}]}`
const denyNone = `{"result":[{"expressions":[{"value":[]}]}]}`

func TestDiesAdmissionDogfood(t *testing.T) {
	const id = "dies:admission-dogfood"
	pinned := "Version: " + checks.OpaVersion
	full := dieTree(map[string]string{"policy/admission/a.rego": "x", "tests/fixtures/ouranos-self.json": "{}"})
	for _, tc := range []struct {
		name    string
		files   map[string]string
		eval    func(Cmd) (string, int)
		state   int
		result  string
		needles []string
	}{
		{"a star the domain admits passes", full, const0(denyNone, 0), 0, pass, []string{"deny count = 0; the admission domain admits our own star shape"}},
		{"a deny is a finding with its reasons", full, const0(denyOne, 0), 1, findings, []string{"deny count = 1", "  no stellar principal"}},
		{"an eval that did not complete is a 2", full, const0("rego_type_error", 3), 2, cannot, []string{"opa eval did not complete", "rego_type_error"}},
		{"an answer this atom cannot read is a 2", full, const0("{}", 0), 2, cannot, []string{"a shape this atom cannot read"}},
		{"an opa that would not start never ran", full, const0("opa: gone", -1), 2, cannot, []string{"the atom never ran: opa: gone"}},
		{"no admission domain is a 2", dieTree(map[string]string{"policy/other/a.rego": "x", "tests/fixtures/ouranos-self.json": "{}"}), nil, 2, cannot, []string{"policy/admission is absent"}},
		{"a missing fixture is a 2", dieTree(map[string]string{"policy/admission/a.rego": "x"}), nil, 2, cannot, []string{"tests/fixtures/ouranos-self.json is absent"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := opaScript{version: pinned}
			if tc.eval != nil {
				script.on = map[string]func(Cmd) (string, int){"eval": tc.eval}
			}
			in, f := toolTree(t, tc.files, script.answer)
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
			if tc.eval == nil {
				if len(f.calls) != 0 {
					t.Errorf("the atom settled before asking opa anything, and ran %v", f.ran())
				}
				return
			}
			want := "eval -d policy/admission -i tests/fixtures/ouranos-self.json data.admission.deny --format json"
			if c := f.calls[0]; flagged(c) != "version" {
				t.Errorf("opa was asked %q before its version was proved", flagged(c))
			}
			if c := f.calls[len(f.calls)-1]; flagged(c) != want || c.Dir != in.Root {
				t.Errorf("opa was run as %q in %q, want %q", flagged(c), c.Dir, want)
			}
		})
	}
	t.Run("an opa that is not the pinned one is a 2 and nothing is evaluated", func(t *testing.T) {
		in, f := toolTree(t, full, opaScript{version: "Version: 0.1.0"}.answer)
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "does not answer", "A policy suite that never ran")
		if len(f.calls) != 1 {
			t.Errorf("opa evaluated after failing its version: %v", f.ran())
		}
	})
	t.Run("off the die's source it is absent and opa is not touched", func(t *testing.T) {
		in, f := toolTree(t, map[string]string{"main.go": "x"}, nil)
		expect(t, runAtom(t, id, in), stateOf(0), absent, "this tree is not the policy die's source")
		if len(f.calls) != 0 {
			t.Errorf("an absent atom touched %v", f.ran())
		}
	})
}

// chaosData is a bundle whose first curated chaos verb is "graph_x".
const chaosData = `{"authz_audience":{"star_only":{"chaos":["graph_x","graph_y"]}}}`

// allowedFor answers a probe with the verbs its principal may see, reading the
// input file opa is pointed at, so the test holds that the file carries the
// principal and the canary.
func allowedFor(session, star string) func(*testing.T) func(Cmd) (string, int) {
	return func(t *testing.T) func(Cmd) (string, int) {
		return func(c Cmd) (string, int) {
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
func fixed(out string, code int) func(*testing.T) func(Cmd) (string, int) {
	return func(*testing.T) func(Cmd) (string, int) { return const0(out, code) }
}

func TestDiesCanaryVisibility(t *testing.T) {
	const id = "dies:canary-visibility"
	pinned := "Version: " + checks.OpaVersion
	run := func(t *testing.T, data string, eval func(*testing.T) func(Cmd) (string, int)) (checks.Verdict, *toolFake) {
		var built string
		in, f := toolTree(t, dieTree(nil), nil)
		f.answer = func(c Cmd) (string, int) {
			switch {
			case c.Name == "tar":
				return data, 0
			case c.Name == "opa" && c.Args[0] == "build":
				built = c.Args[4]
			case c.Name == "opa" && c.Args[0] == "eval":
				if c.Args[1] != "-b" || c.Args[2] != built {
					t.Errorf("the bundle built at %q was interrogated as %q", built, flagged(c))
				}
				return eval(t)(c)
			}
			return opaScript{version: pinned}.answer(c)
		}
		return runAtom(t, id, in), f
	}
	for _, tc := range []struct {
		name    string
		data    string
		eval    func(*testing.T) func(Cmd) (string, int)
		state   int
		result  string
		needles []string
	}{
		{"hidden from a session, visible to a star", chaosData, allowedFor(`["search"]`, `["search","graph_x"]`), 0, pass,
			[]string{"the built bundle still hides what it should", "canary: graph_x"}},
		{"visible to a session is the fail-open", chaosData, allowedFor(`["search","graph_x"]`, `["search","graph_x"]`), 1, findings, []string{"VISIBLE to a session principal"}},
		{"invisible to a star narrowed both audiences", chaosData, allowedFor(`["search"]`, `["search"]`), 1, findings, []string{"INVISIBLE to a star principal"}},
		{"a bundle with no chaos row did not keep its roster", `{"authz_audience":{"star_only":{}}}`, fixed("", 0), 1, findings, []string{"chaos has no star_only row"}},
		{"a probe that did not complete", chaosData, fixed("rego_error", 2), 2, cannot,
			[]string{"the session-principal probe against the built bundle did not evaluate", "rego_error"}},
		{"a probe that would not start", chaosData, fixed("opa: gone", -1), 2, cannot, []string{"opa: gone"}},
		{"a probe that answers a value that is not a list", chaosData, fixed(`{"result":[{"expressions":[{"value":"x"}]}]}`, 0), 2, cannot, []string{"not a list of verbs"}},
		{"a probe that answers nothing", chaosData, fixed("{}", 0), 2, cannot, []string{"no result[0]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, _ := run(t, tc.data, tc.eval)
			expect(t, v, stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("the star probe is not asked when the session probe failed", func(t *testing.T) {
		_, f := run(t, chaosData, fixed("rego_error", 2))
		evals := 0
		for _, c := range f.calls {
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
		in, f := toolTree(t, dieTree(nil), nil)
		f.answer = func(c Cmd) (string, int) {
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
			return opaScript{version: pinned}.answer(c)
		}
		expect(t, runAtom(t, id, in), stateOf(0), pass)
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
			in, f := toolTree(t, dieTree(nil), nil)
			f.answer = func(c Cmd) (string, int) {
				if c.Name == "opa" && c.Args[0] == "build" {
					if seen[c.Args[4]] {
						t.Errorf("the bundle path %s was used twice", c.Args[4])
					}
					seen[c.Args[4]] = true
				}
				return opaScript{version: pinned}.answer(c)
			}
			runAtom(t, id, in)
		}
	})
	t.Run("a probe input that cannot be written is a could-not-run", func(t *testing.T) {
		// A scratch directory that sits under a file can never be made.
		blocker := t.TempDir() + "/file"
		if err := os.WriteFile(blocker, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TMPDIR", blocker+"/sub")
		in, _ := toolTree(t, dieTree(nil), func(c Cmd) (string, int) {
			if c.Name == "tar" {
				return chaosData, 0
			}
			return opaScript{version: pinned}.answer(c)
		})
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the session-principal probe against the built bundle did not evaluate")
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
			expect(t, v, stateOf(1), findings, c.why)
		})
	}
	t.Run("a star probe that did not complete is named", func(t *testing.T) {
		v, f := run(t, chaosData, func(t *testing.T) func(Cmd) (string, int) {
			ok := allowedFor(`["search"]`, `["search","graph_x"]`)(t)
			return func(c Cmd) (string, int) {
				if body, _ := os.ReadFile(c.Args[4]); strings.Contains(string(body), `"type":"star"`) {
					return "rego_error", 2
				}
				return ok(c)
			}
		})
		expect(t, v, stateOf(2), cannot, "the star-principal probe against the built bundle did not evaluate", "rego_error")
		if strings.Contains(v.Reason, "session-principal") {
			t.Errorf("the session probe was blamed: %s", v.Reason)
		}
		_ = f
	})
	t.Run("an opa that is not the pinned one is a 2 and no bundle is built", func(t *testing.T) {
		in, f := toolTree(t, dieTree(nil), opaScript{version: "Version: 0.1.0"}.answer)
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "does not answer", "A policy suite that never ran")
		if len(f.calls) != 1 {
			t.Errorf("a bundle was built with the wrong opa: %v", f.ran())
		}
	})
	t.Run("a build that did not produce a bundle is a 2 and nothing is probed", func(t *testing.T) {
		in, f := toolTree(t, dieTree(nil), opaScript{version: pinned, on: map[string]func(Cmd) (string, int){"build": const0("rego_error", 1)}}.answer)
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "opa build did not produce a bundle", "opa build exited 1")
		for _, c := range f.calls {
			if c.Name == "opa" && c.Args[0] == "eval" {
				t.Errorf("a bundle that was not built was interrogated")
			}
		}
	})
	t.Run("a bundle with no data.json is a finding and nothing is probed", func(t *testing.T) {
		in, f := toolTree(t, dieTree(nil), func(c Cmd) (string, int) {
			if c.Name == "tar" {
				return "tar: /data.json: Not found in archive", 2
			}
			return opaScript{version: pinned}.answer(c)
		})
		expect(t, runAtom(t, id, in), stateOf(1), findings, "carries NO data.json member at all")
		for _, c := range f.calls {
			if c.Name == "opa" && c.Args[0] == "eval" {
				t.Errorf("a bundle with no data was interrogated")
			}
		}
	})
	t.Run("off the die's source it is absent and opa is not touched", func(t *testing.T) {
		in, f := toolTree(t, map[string]string{"main.go": "x"}, nil)
		expect(t, runAtom(t, id, in), stateOf(0), absent, "this tree is not the policy die's source")
		if len(f.calls) != 0 {
			t.Errorf("an absent atom touched %v", f.ran())
		}
	})
}
