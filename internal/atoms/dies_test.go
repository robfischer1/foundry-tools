package atoms

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// dieTree is the smallest tree diesShape calls the policy die's source.
func dieTree(extra map[string]string) map[string]string {
	files := map[string]string{"policy/.manifest": "x", "fleet/repos/chaos.json": "{\n  \"a\": 1\n}\n"}
	for k, v := range extra {
		files[k] = v
	}
	return files
}

func TestDiesShape(t *testing.T) {
	for _, id := range []string{"dies:data-keys", "dies:canonical", "dies:refusal-codes"} {
		t.Run(id+" is absent off the die's source", func(t *testing.T) {
			for name, files := range map[string]map[string]string{
				"neither marker":    {"main.go": "x"},
				"no manifest":       {"policy/rules.rego": "x", "fleet/repos/a.json": "x"},
				"no repos":          {"policy/.manifest": "x", "fleet/other": "x"},
				"no fleet at all":   {"policy/.manifest": "x"},
				"no policy at all":  {"fleet/repos/a.json": "x"},
				"manifest is a dir": {"policy/.manifest/x": "x", "fleet/repos/a.json": "x"},
			} {
				v := runAtom(t, id, treeIn(t, files))
				if v.State != 0 || v.Result != absent {
					t.Errorf("%s: %s: state %d (%s)\n%s", id, name, v.State, v.Result, v.Reason)
				}
			}
		})
		t.Run(id+" cannot judge a shape it could not read", func(t *testing.T) {
			expect(t, runAtom(t, id, missingRoot(t)), stateOf(2), cannot, "the repository root could not be read")
			in := treeIn(t, map[string]string{"policy/.manifest": "x", "x": "y"})
			put(t, in.Root, "fleet", "a file, not a directory")
			expect(t, runAtom(t, id, in), stateOf(2), cannot, "fleet/ is in the root listing but could not be read")
		})
	}
}

func TestDiesCanonical(t *testing.T) {
	const id = "dies:canonical"
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		{"a document that is its own canonical form passes", dieTree(nil), 0, pass, []string{"1 catalog document(s) are each their own canonical form"}},
		{"keys out of order diverge", dieTree(map[string]string{"fleet/repos/b.json": "{\"b\": 1, \"a\": 2}\n"}), 1, findings,
			[]string{"1 of 2 catalog document(s)", "fleet/repos/b.json: diverges from its canonical form"}},
		{"a document that is not JSON is a finding", dieTree(map[string]string{"fleet/repos/b.json": "{"}), 1, findings,
			[]string{"fleet/repos/b.json: not one JSON document"}},
		{"no document at all grades nothing", map[string]string{"policy/.manifest": "x", "fleet/repos/a/other.json": "x"}, 2, cannot,
			[]string{"carries no catalog entry"}},
		{"a document that will not read", map[string]string{"policy/.manifest": "x", "fleet/repos/a.json/x": "x"}, 2, cannot,
			[]string{"fleet/repos/a.json/ could not be read"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expect(t, runAtom(t, id, treeIn(t, tc.files)), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
}

const goodData = `{"authz_audience":{"star_only":{"chaos":["v"],"nyx":["w"]}},"authz_grants":{"a":1},"authz_meta":{"a":1},"path_grants":{"a":1},"subject_aliases":{"a":1},"capabilities":{"stars":{"athena":{"prefix":"plan","verbs":["task_read"],"tier":{"task_read":"default"}}}}}`

// fakeOpa answers for opa, tar and git the way the tools do, recording each call.
type fakeOpa struct {
	version     string
	versionCode int
	buildCode   int
	tarOut      string
	tarCode     int
	calls       []Cmd
}

func (f *fakeOpa) exec(_ context.Context, c Cmd) (string, int) {
	f.calls = append(f.calls, c)
	switch {
	case c.Name == "git":
		return "abc123", 0
	case c.Name == "opa" && c.Args[0] == "version":
		return f.version, f.versionCode
	case c.Name == "opa":
		return "build output", f.buildCode
	}
	return f.tarOut, f.tarCode
}

func TestDiesDataKeys(t *testing.T) {
	const id = "dies:data-keys"
	pinned := "Version: " + checks.OpaVersion
	for _, tc := range []struct {
		name    string
		opa     fakeOpa
		state   int
		result  string
		needles []string
	}{
		{"every data root present passes", fakeOpa{version: pinned, tarOut: goodData}, 0, pass, []string{"data roots ok; star_only carries 2 stars"}},
		{"an empty root is the fail-open", fakeOpa{version: pinned, tarOut: `{"authz_audience":{},"authz_grants":{"a":1},"authz_meta":{"a":1},"path_grants":{"a":1},"subject_aliases":{"a":1},"capabilities":{"stars":{"athena":{"prefix":"plan","verbs":["task_read"],"tier":{"task_read":"default"}}}}}`},
			1, findings, []string{"bundle data.json is missing/empty: [authz_audience]"}},
		{"a bundle with no data.json member is a finding", fakeOpa{version: pinned, tarOut: "tar: /data.json: Not found in archive", tarCode: 2}, 1, findings,
			[]string{"carries NO data.json member at all", "Not found in archive"}},
		{"a data.json that is not JSON is a 2", fakeOpa{version: pinned, tarOut: "{"}, 2, cannot, []string{"a shape this atom cannot read"}},
		{"a build that failed is a 2", fakeOpa{version: pinned, buildCode: 1}, 2, cannot, []string{"opa build did not produce a bundle", "opa build exited 1"}},
		{"an opa that is not the pinned one is a 2", fakeOpa{version: "Version: 0.1.0"}, 2, cannot,
			[]string{"does not answer \"Version: " + checks.OpaVersion + "\"", "A policy suite that never ran is not a policy suite that passed"}},
		{"an opa that does not run is a 2", fakeOpa{version: "opa: not found", versionCode: -1}, 2, cannot, []string{"opa is on disk but does not run (exit -1)"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := treeIn(t, dieTree(nil))
			f := tc.opa
			in.Exec = f.exec
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("the bundle is built from policy/ in the tree, stamped with HEAD, tests ignored", func(t *testing.T) {
		in := treeIn(t, dieTree(nil))
		f := &fakeOpa{version: pinned, tarOut: goodData}
		in.Exec = f.exec
		runAtom(t, id, in)
		var build Cmd
		for _, c := range f.calls {
			if c.Name == "opa" && c.Args[0] == "build" {
				build = c
			}
		}
		line := strings.Join(build.Args, " ")
		for _, want := range []string{"build -b policy/", "--revision abc123", "--ignore *_test.rego"} {
			if !strings.Contains(line, want) {
				t.Errorf("opa was run as %q, lacking %q", line, want)
			}
		}
		if build.Dir != in.Root {
			t.Errorf("opa ran in %q, not the repository root %q", build.Dir, in.Root)
		}
	})
	for name, git := range map[string]func() (string, int){
		"a tree git cannot resolve a HEAD in":  func() (string, int) { return "fatal: bad revision", 128 },
		"a git that succeeds and says nothing": func() (string, int) { return "", 0 },
	} {
		t.Run(name+" builds as unknown", func(t *testing.T) {
			in := treeIn(t, dieTree(nil))
			var rev string
			in.Exec = func(_ context.Context, c Cmd) (string, int) {
				switch {
				case c.Name == "git":
					return git()
				case c.Name == "opa" && c.Args[0] == "version":
					return pinned, 0
				case c.Name == "opa":
					rev = strings.Join(c.Args, " ")
					return "", 0
				}
				return goodData, 0
			}
			expect(t, runAtom(t, id, in), stateOf(0), pass)
			if !strings.Contains(rev, "--revision unknown") {
				t.Errorf("the build ran as %q", rev)
			}
		})
	}
}

func TestDiesCapabilitiesServed(t *testing.T) {
	const id = "dies:capabilities-served"
	decl := "{\"stars\":{\"athena\":{\"prefix\":\"plan\",\"verbs\":[\"task_read\"],\"tier\":{\"task_read\":\"default\"}}}}\n"
	served := "{\"schema\":1,\"star\":\"athena\",\"verbs\":[\"task_read\"]}\n"
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		{"declared exactly as served passes", dieTree(map[string]string{
			"fleet/repos/athena.json": "{\"verb_prefix\":\"plan\"}\n", "policy/capabilities/data.json": decl, "capabilities/served/athena.json": served,
		}), 0, pass, []string{"1 verb(s) across 1 star(s)"}},
		{"a die that has not adopted capabilities is absent", dieTree(nil), 0, absent, []string{"neither policy/capabilities/data.json"}},
		{"a served verb the document omits is findings", dieTree(map[string]string{
			"fleet/repos/athena.json": "{\"verb_prefix\":\"plan\"}\n", "policy/capabilities/data.json": decl,
			"capabilities/served/athena.json": "{\"schema\":1,\"star\":\"athena\",\"verbs\":[\"task_read\",\"task_new\"]}\n",
		}), 1, findings, []string{"served but not declared (denied to every caller): task_new"}},
		{"a declared verb nothing serves is findings", dieTree(map[string]string{
			"fleet/repos/athena.json": "{\"verb_prefix\":\"plan\"}\n", "policy/capabilities/data.json": decl,
			"capabilities/served/athena.json": "{\"schema\":1,\"star\":\"athena\",\"verbs\":[]}\n",
		}), 1, findings, []string{"declared but not served: task_read"}},
		{"a declaration landed beside the path the gate reads is findings, not absent", dieTree(map[string]string{
			"fleet/repos/athena.json": "{\"verb_prefix\":\"plan\"}\n", "policy/capabilities/stars/data.json": decl,
		}), 1, findings, []string{"not policy/capabilities/data.json"}},
		{"a document with no stars key is findings even with no exports", dieTree(map[string]string{
			"policy/capabilities/data.json": "{}\n",
		}), 1, findings, []string{"no \"stars\" object"}},
		{"declared with no export committed is findings", dieTree(map[string]string{
			"fleet/repos/athena.json": "{\"verb_prefix\":\"plan\"}\n", "policy/capabilities/data.json": decl,
		}), 1, findings, []string{"athena: declared in policy/capabilities/data.json but no served-verbs export"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expect(t, runAtom(t, id, treeIn(t, tc.files)), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
}
