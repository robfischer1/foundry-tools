package diesdatakeys

import (
	"context"
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

// fakeOpa answers for opa, tar and git the way the tools do, recording each call.
type fakeOpa struct {
	version     string
	versionCode int
	buildCode   int
	tarOut      string
	tarCode     int
	calls       []atoms.Cmd
}

func (f *fakeOpa) exec(_ context.Context, c atoms.Cmd) (string, int) {
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

func TestGradesTheBuiltDataDocument(t *testing.T) {
	pinned := atomtest.PinnedOpa()
	for _, tc := range []struct {
		name    string
		opa     fakeOpa
		state   checks.State
		result  string
		needles []string
	}{
		{"every data root present passes", fakeOpa{version: pinned, tarOut: atomtest.GoodData}, checks.StatePass, atomtest.Pass, []string{"data roots ok; star_only carries 2 stars"}},
		{"an empty root is the fail-open", fakeOpa{version: pinned, tarOut: `{"authz_audience":{},"grants":{"a":1},"principals":{"a":1},"capabilities":{"stars":{"athena":{"prefix":"plan","verbs":["task_read"],"tier":{"task_read":"default"}}}}}`},
			checks.StateFindings, atomtest.Findings, []string{"bundle data.json is missing/empty: [authz_audience]"}},
		{"a bundle with no data.json member is a finding", fakeOpa{version: pinned, tarOut: "tar: /data.json: Not found in archive", tarCode: 2}, checks.StateFindings, atomtest.Findings,
			[]string{"carries NO data.json member at all", "Not found in archive"}},
		{"a data.json that is not JSON is a 2", fakeOpa{version: pinned, tarOut: "{"}, checks.StateCannotRun, atomtest.Cannot, []string{"a shape this atom cannot read"}},
		{"a build that failed is a 2", fakeOpa{version: pinned, buildCode: 1}, checks.StateCannotRun, atomtest.Cannot, []string{"opa build did not produce a bundle", "opa build exited 1"}},
		{"an opa that is not the pinned one is a 2", fakeOpa{version: "Version: 0.1.0"}, checks.StateCannotRun, atomtest.Cannot,
			[]string{"does not answer \"Version: " + checks.OpaVersion + "\"", "A policy suite that never ran is not a policy suite that passed"}},
		{"an opa that does not run is a 2", fakeOpa{version: "opa: not found", versionCode: -1}, checks.StateCannotRun, atomtest.Cannot, []string{"opa is on disk but does not run (exit -1)"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := atomtest.TreeIn(t, atomtest.DieTree(nil))
			f := tc.opa
			in.Exec = f.exec
			atomtest.Expect(t, check(context.Background(), in), tc.state, tc.result, tc.needles...)
		})
	}
	t.Run("the bundle is built from policy/ in the tree, stamped with HEAD, tests ignored", func(t *testing.T) {
		in := atomtest.TreeIn(t, atomtest.DieTree(nil))
		f := &fakeOpa{version: pinned, tarOut: atomtest.GoodData}
		in.Exec = f.exec
		check(context.Background(), in)
		var build atoms.Cmd
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
			in := atomtest.TreeIn(t, atomtest.DieTree(nil))
			var rev string
			in.Exec = func(_ context.Context, c atoms.Cmd) (string, int) {
				switch {
				case c.Name == "git":
					return git()
				case c.Name == "opa" && c.Args[0] == "version":
					return pinned, 0
				case c.Name == "opa":
					rev = strings.Join(c.Args, " ")
					return "", 0
				}
				return atomtest.GoodData, 0
			}
			atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass)
			if !strings.Contains(rev, "--revision unknown") {
				t.Errorf("the build ran as %q", rev)
			}
		})
	}
	t.Run("off the die's source it is absent and opa is not touched", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, map[string]string{"main.go": "x"}, nil)
		atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Absent, "this tree is not the policy die's source")
		if len(f.Calls) != 0 {
			t.Errorf("an absent atom touched %v", f.Ran())
		}
	})
}

// opa, git and tar are programs the tools container carries.
func TestExecsOnlyListedPrograms(t *testing.T) {
	in, f := atomtest.ToolTree(t, atomtest.DieTree(nil), atomtest.OpaScript{Version: atomtest.PinnedOpa()}.Answer)
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass)
	if len(f.Calls) == 0 {
		t.Error("no program was exec'd")
	}
}
