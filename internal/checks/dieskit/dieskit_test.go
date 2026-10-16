package dieskit

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
)

var row = checks.AtomDef{ID: "dies:x", Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// TWO MARKERS, BOTH REQUIRED. Either alone is ambiguous: policy/ turns up in
// more than one repo and fleet/repos/ is a name a fleet inventory could take.
func TestShapeIsAbsentWithoutBothMarkers(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"neither marker":    {"main.go": "x"},
		"no manifest":       {"policy/rules.rego": "x", "fleet/repos/a.json": "x"},
		"no repos":          {"policy/.manifest": "x", "fleet/other": "x"},
		"no fleet at all":   {"policy/.manifest": "x"},
		"no policy at all":  {"fleet/repos/a.json": "x"},
		"manifest is a dir": {"policy/.manifest/x": "x", "fleet/repos/a.json": "x"},
	} {
		t.Run(name, func(t *testing.T) {
			v := Shape(atomtest.TreeIn(t, files).Tree(), row)
			if v == nil {
				t.Fatal("a tree that is not the die's source was taken for it")
			}
			atomtest.Expect(t, *v, checks.StatePass, atomtest.Absent, "this tree is not the policy die's source")
		})
	}
	if v := Shape(atomtest.TreeIn(t, atomtest.DieTree(nil)).Tree(), row); v != nil {
		t.Errorf("the die's source was refused: %+v", v)
	}
}

func TestShapeCannotJudgeATreeItCouldNotRead(t *testing.T) {
	v := Shape(atomtest.MissingRoot(t).Tree(), row)
	atomtest.Expect(t, *v, checks.StateCannotRun, atomtest.Cannot, "the repository root could not be read")
	in := atomtest.TreeIn(t, map[string]string{"policy/.manifest": "x", "x": "y"})
	atomtest.Put(t, in.Root, "fleet", "a file, not a directory")
	atomtest.Expect(t, *Shape(in.Tree(), row), checks.StateCannotRun, atomtest.Cannot, "fleet/ is in the root listing but could not be read")
}

func TestOpaProvisionedProvesThePinnedVersion(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		version string
		code    int
		needles []string
	}{
		{"the pinned opa", atomtest.PinnedOpa(), 0, nil},
		{"another major", "Version: 0.1.0", 0, []string{"does not answer", "A policy suite that never ran is not a policy suite that passed"}},
		{"a prerelease is not the pin", atomtest.PinnedOpa() + "-rc1", 0, []string{"does not answer"}},
		{"an opa that does not run", "opa: not found", -1, []string{"opa is on disk but does not run (exit -1)"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := atomtest.ToolTree(t, atomtest.DieTree(nil), atomtest.OpaScript{Version: tc.version, VCode: tc.code}.Answer)
			v := OpaProvisioned(ctx, row, in)
			if tc.needles == nil {
				if v != nil {
					t.Errorf("the pinned opa was refused: %+v", v)
				}
			} else {
				atomtest.Expect(t, *v, checks.StateCannotRun, atomtest.Cannot, tc.needles...)
			}
			if c := f.Last(); c.Name != "opa" || atomtest.Flagged(c) != "version" || c.Dir != in.Root {
				t.Errorf("the probe was %+v", c)
			}
		})
	}
}

func TestBundleBuildsAtHeadAndOpensTheDataDocument(t *testing.T) {
	ctx := context.Background()
	in, f := atomtest.ToolTree(t, atomtest.DieTree(nil), atomtest.OpaScript{Version: atomtest.PinnedOpa()}.Answer)
	data, stop := Bundle(ctx, row, in, "/tmp/b.tar.gz")
	if stop != nil || data != atomtest.GoodData {
		t.Fatalf("%q %+v", data, stop)
	}
	got := []string{}
	for _, c := range f.Calls {
		got = append(got, c.Name+" "+atomtest.Flagged(c))
	}
	for i, want := range []string{
		"git -c safe.directory=* rev-parse HEAD",
		"opa build -b policy/ -o /tmp/b.tar.gz --revision abc123 --ignore *_test.rego",
		"tar xzOf /tmp/b.tar.gz /data.json",
	} {
		if got[i] != want {
			t.Errorf("call %d: %q, want %q", i, got[i], want)
		}
	}
}

func TestBundleSeparatesABrokenBuildFromAnEmptyArtifactAndStampsUnknown(t *testing.T) {
	ctx := context.Background()
	t.Run("a build that did not complete is a 2", func(t *testing.T) {
		in, _ := atomtest.ToolTree(t, atomtest.DieTree(nil), atomtest.OpaScript{On: map[string]func(atoms.Cmd) (string, int){"build": atomtest.Const0("rego_error", 1)}}.Answer)
		_, stop := Bundle(ctx, row, in, "/tmp/b.tar.gz")
		atomtest.Expect(t, *stop, checks.StateCannotRun, atomtest.Cannot, "opa build did not produce a bundle", "opa build exited 1: rego_error")
	})
	t.Run("a bundle with no data.json member is a 1", func(t *testing.T) {
		in, _ := atomtest.ToolTree(t, atomtest.DieTree(nil), func(c atoms.Cmd) (string, int) {
			if c.Name == "tar" {
				return "tar: /data.json: Not found in archive", 2
			}
			return atomtest.OpaScript{}.Answer(c)
		})
		_, stop := Bundle(ctx, row, in, "/tmp/b.tar.gz")
		atomtest.Expect(t, *stop, checks.StateFindings, atomtest.Findings, "carries NO data.json member at all", "Not found in archive")
	})
	for name, git := range map[string]func() (string, int){
		"a tree git cannot resolve a HEAD in":  func() (string, int) { return "fatal: bad revision", 128 },
		"a git that succeeds and says nothing": func() (string, int) { return "", 0 },
	} {
		t.Run(name+" builds as unknown", func(t *testing.T) {
			var build string
			in, _ := atomtest.ToolTree(t, atomtest.DieTree(nil), func(c atoms.Cmd) (string, int) {
				switch {
				case c.Name == "git":
					return git()
				case c.Name == "opa":
					build = atomtest.Flagged(c)
					return "", 0
				}
				return atomtest.GoodData, 0
			})
			if _, stop := Bundle(ctx, row, in, "/tmp/b.tar.gz"); stop != nil {
				t.Fatalf("%+v", stop)
			}
			if !strings.Contains(build, "--revision unknown") {
				t.Errorf("the build ran as %q", build)
			}
		})
	}
}

func TestUVIsPython3UnderUVRunFromTheRootWithBothStreams(t *testing.T) {
	c := UV("/src", WithTOML, "x.py", "--flag")
	atomtest.WantUV(t, c, "/src", WithTOML, "x.py", "--flag")
	if got := strings.Join(c.Args, " "); got != "run --no-project --quiet --with tomli>=2.0 python3 x.py --flag" {
		t.Errorf("%s", got)
	}
	if got := strings.Join(UV("/src", "", "x.py").Args, " "); got != "run --no-project --quiet python3 x.py" {
		t.Errorf("a program that needs no package is resolved with none: %s", got)
	}
}

func TestProbeImportsSettleAndVerdictRunTheProgramsTheyName(t *testing.T) {
	ctx := context.Background()
	t.Run("a container without uv is a 2 and the program is not run", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, atomtest.DieTree(nil), func(atoms.Cmd) (string, int) { return "gone", 127 })
		f.Free = []string{"uv"}
		atomtest.Expect(t, *Probe(ctx, row, in), checks.StateCannotRun, atomtest.Cannot, "the atom never ran: exit code: 127", "failed: uv --version", "gone")
		atomtest.Expect(t, Verdict(ctx, row, in, "", "x.py"), checks.StateCannotRun, atomtest.Cannot, "exit code: 127", "failed: uv --version")
		if len(f.Calls) != 2 {
			t.Errorf("the program ran on an unprovisioned container: %v", f.Ran())
		}
	})
	t.Run("a probe that passes returns nothing", func(t *testing.T) {
		in, _ := atomtest.UVTree(t, atomtest.DieTree(nil), nil)
		if v := Probe(ctx, row, in); v != nil {
			t.Errorf("%+v", v)
		}
	})
	t.Run("an import the resolver cannot supply is a 2", func(t *testing.T) {
		in, f := atomtest.UVTree(t, atomtest.DieTree(nil), atomtest.Const0("ModuleNotFoundError: No module named 'jsonschema'", 1))
		atomtest.Expect(t, *Imports(ctx, row, in, WithJSONSchema, "jsonschema"), checks.StateCannotRun, atomtest.Cannot,
			"the atom never ran: exit code: 1", "failed: uv run --no-project --quiet --with jsonschema>=4.20 python3 -c import jsonschema", "No module named 'jsonschema'")
		atomtest.WantUV(t, f.Last(), in.Root, WithJSONSchema, "-c", "import jsonschema")
		ok, _ := atomtest.UVTree(t, atomtest.DieTree(nil), nil)
		if v := Imports(ctx, row, ok, WithJSONSchema, "jsonschema"); v != nil {
			t.Errorf("%+v", v)
		}
	})
	t.Run("the exit is the state, unmapped, and a uv that would not start never ran", func(t *testing.T) {
		for _, tc := range []struct {
			code   int
			state  checks.State
			result string
			words  string
		}{
			{0, checks.StatePass, atomtest.Pass, "all clear"},
			{1, checks.StateFindings, atomtest.Findings, "all clear"},
			{2, checks.StateCannotRun, atomtest.Cannot, "all clear"},
			{-1, checks.StateCannotRun, atomtest.Cannot, "the atom never ran: all clear"},
		} {
			in, f := atomtest.UVTree(t, atomtest.DieTree(nil), atomtest.Const0("all clear", tc.code))
			atomtest.Expect(t, Verdict(ctx, row, in, WithTOML, "x.py"), tc.state, tc.result, tc.words)
			atomtest.WantUV(t, f.Last(), in.Root, WithTOML, "x.py")
		}
	})
}

func TestCheckerSettlesTheShapeThePresenceAndTheProbeBeforeTheProgram(t *testing.T) {
	ctx := context.Background()
	files := atomtest.DieTree(map[string]string{"tools/check.py": "x"})
	in, f := atomtest.UVTree(t, files, atomtest.Const0("checked", 0))
	atomtest.Expect(t, Checker(ctx, row, in, "", "tools/check.py"), checks.StatePass, atomtest.Pass, "checked")
	atomtest.WantUV(t, f.Last(), in.Root, "", "tools/check.py")
	in2, _ := atomtest.UVTree(t, atomtest.DieTree(nil), nil)
	atomtest.Expect(t, Checker(ctx, row, in2, "", "tools/check.py"), checks.StateCannotRun, atomtest.Cannot, "tools/check.py is absent, so there is no checker to run")
	in3, _ := atomtest.UVTree(t, map[string]string{"main.go": "x"}, nil)
	atomtest.Expect(t, Checker(ctx, row, in3, "", "tools/check.py"), checks.StatePass, atomtest.Absent, "not the policy die's source")
}
