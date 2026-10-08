package atoms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// opsPyAnswer answers every `--version` probe and hands the rest to the script.
func opsPyAnswer(answer func(Cmd) (string, int)) func(Cmd) (string, int) {
	return func(c Cmd) (string, int) {
		if len(c.Args) == 1 && c.Args[0] == "--version" {
			return "Python 3.14.0", 0
		}
		if answer == nil {
			return "", 0
		}
		return answer(c)
	}
}

// checkerTree is an ops tree (it carries flux/) with one of the tree's own
// checkers, tracked executable or not.
func checkerTree(t *testing.T, tool string, mode os.FileMode, answer func(Cmd) (string, int)) (Input, *toolFake) {
	t.Helper()
	in, f := toolTree(t, map[string]string{"flux/x.yaml": "a: 1\n", "tools/" + tool: "#!/usr/bin/env python3\n"}, opsPyAnswer(answer))
	if err := os.Chmod(filepath.Join(in.Root, "tools", tool), mode); err != nil {
		t.Fatal(err)
	}
	return in, f
}

func TestOpsPythonCheckers(t *testing.T) {
	for _, tc := range []struct {
		id         string
		tool       string
		python     string
		args       []string
		executable bool
	}{
		{"ops:dup", "dup-check", "python3", []string{"--blocking"}, true},
		{"ops:declaration", "declaration-integrity", "python", nil, false},
		{"ops:metrics", "metric-allowlist", "python", nil, false},
		{"ops:specs", "console-specs", "python", []string{"--check"}, false},
	} {
		t.Run(tc.id, func(t *testing.T) {
			phase := strings.TrimPrefix(tc.id, "ops:")
			for _, c := range []struct {
				name    string
				code    int
				out     string
				state   int
				result  string
				needles []string
			}{
				{"a checker that holds is a pass", 0, "checked", 0, pass, []string{"checked"}},
				{"a checker that finds something is a finding", 1, "a duplicate", 1, findings, []string{"a duplicate", phase + " failed (rc=1) — findings"}},
				{"a fault of the substrate is a 2, not a finding", 1, "connection refused", 2, cannot, []string{"failed on a fault of the substrate"}},
				{"a python that would not start never ran", -1, "python: gone", 2, cannot, []string{"the atom never ran: python: gone"}},
			} {
				t.Run(c.name, func(t *testing.T) {
					in, f := checkerTree(t, tc.tool, 0o755, func(Cmd) (string, int) { return c.out, c.code })
					expect(t, runAtom(t, tc.id, in), stateOf(c.state), c.result, c.needles...)
					run := f.lastCall(t)
					want := append([]string{"tools/" + tc.tool}, tc.args...)
					if run.Name != tc.python || strings.Join(run.Args, " ") != strings.Join(want, " ") || run.Dir != in.Root || !run.Both {
						t.Errorf("the checker ran as %+v, want %s %v in the root with both streams", run, tc.python, want)
					}
					if probe := f.calls[0]; probe.Name != tc.python || flagged(probe) != "--version" {
						t.Errorf("the first call was %+v: the version probe comes before the work", probe)
					}
					if !strings.Contains(strings.Join(run.Env, " "), "PYTHONDONTWRITEBYTECODE=1") {
						t.Errorf("the checker may write bytecode into the tree: %v", run.Env)
					}
				})
			}
			t.Run("a python that fails its probe is unprovisioned", func(t *testing.T) {
				in, f := toolTree(t, map[string]string{"flux/x.yaml": "a: 1\n", "tools/" + tc.tool: "#!/usr/bin/env python3\n"}, func(Cmd) (string, int) { return "gone", 127 })
				if err := os.Chmod(filepath.Join(in.Root, "tools", tc.tool), 0o755); err != nil {
					t.Fatal(err)
				}
				expect(t, runAtom(t, tc.id, in), stateOf(2), cannot, "the phase's tool could not be provisioned", tc.python+" --version exited 127")
				if len(f.calls) != 1 {
					t.Errorf("the checker ran on an unprovisioned container: %v", f.ran())
				}
			})
			t.Run("no checker is absent and python is not touched", func(t *testing.T) {
				in, f := toolTree(t, map[string]string{"flux/x.yaml": "a: 1\n"}, nil)
				expect(t, runAtom(t, tc.id, in), stateOf(0), absent, "no tools/"+tc.tool+" in this tree")
				if len(f.calls) != 0 {
					t.Errorf("an absent atom touched %v", f.ran())
				}
			})
			t.Run("a tree that is not an ops tree is absent", func(t *testing.T) {
				in, f := toolTree(t, map[string]string{"main.go": "x"}, nil)
				expect(t, runAtom(t, tc.id, in), stateOf(0), absent, "has no ops shape")
				if len(f.calls) != 0 {
					t.Errorf("an absent atom touched %v", f.ran())
				}
			})
			t.Run("a population that failed", func(t *testing.T) {
				in, _ := checkerTree(t, tc.tool, 0o755, nil)
				in.FilesErr = errBoom
				expect(t, runAtom(t, tc.id, in), stateOf(2), cannot, "the tree would not enumerate")
			})
		})
	}
	t.Run("ops:dup is run as a program, so it has to be tracked executable", func(t *testing.T) {
		in, f := checkerTree(t, "dup-check", 0o644, nil)
		expect(t, runAtom(t, "ops:dup", in), stateOf(0), absent, "no tools/dup-check in this tree")
		if len(f.calls) != 0 {
			t.Errorf("a script that is not executable was run: %v", f.ran())
		}
	})
	t.Run("the other two are run through python, so they need not be", func(t *testing.T) {
		in, _ := checkerTree(t, "declaration-integrity", 0o644, nil)
		expect(t, runAtom(t, "ops:declaration", in), stateOf(0), pass)
	})
	t.Run("a tracked name with no file behind it is not a checker", func(t *testing.T) {
		in, _ := toolTree(t, map[string]string{"flux/x.yaml": "a: 1\n"}, nil)
		in.Committable = append(in.Committable, "tools/dup-check")
		expect(t, runAtom(t, "ops:dup", in), stateOf(0), absent, "no tools/dup-check in this tree")
	})
	t.Run("a tracked symlink is not an executable script", func(t *testing.T) {
		in, _ := toolTree(t, map[string]string{"flux/x.yaml": "a: 1\n", "elsewhere": "#!/usr/bin/env python3\n"}, nil)
		if err := os.MkdirAll(filepath.Join(in.Root, "tools"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(in.Root, "elsewhere"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../elsewhere", filepath.Join(in.Root, "tools/dup-check")); err != nil {
			t.Fatal(err)
		}
		in.Committable = append(in.Committable, "tools/dup-check")
		expect(t, runAtom(t, "ops:dup", in), stateOf(0), absent, "no tools/dup-check in this tree")
	})
	t.Run("ops:metrics: the tool grades itself, and its 2 is a could-not-run whatever its words were", func(t *testing.T) {
		in, _ := checkerTree(t, "metric-allowlist", 0o755, func(Cmd) (string, int) { return "could not read a source", 2 })
		expect(t, runAtom(t, "ops:metrics", in), stateOf(2), cannot, "could not read a source", "metrics: could not read a keep-list or a source — did not look")
	})
	t.Run("ops:declaration has no exit-2 case: any other exit is a finding, as the chain settled it", func(t *testing.T) {
		in, _ := checkerTree(t, "declaration-integrity", 0o755, func(Cmd) (string, int) { return "a payload is undeclared", 2 })
		expect(t, runAtom(t, "ops:declaration", in), stateOf(1), findings, "a payload is undeclared", "declaration failed (rc=2) — findings")
	})
}

// ops:specs' tool compares the tree's copy to nas01-stacks, cloned live when
// nothing names a source; the atom names none, so the tool's own fallback is what
// runs. Its 2 is a could-not-run whatever its words were.
func TestOpsSpecsLeavesTheSourceToTheTool(t *testing.T) {
	t.Run("an unreachable source is the tool's exit 2", func(t *testing.T) {
		in, f := checkerTree(t, "console-specs", 0o755, func(Cmd) (string, int) { return "could not clone http://ourea:8215/nas01-stacks.git", 2 })
		expect(t, runAtom(t, "ops:specs", in), stateOf(2), cannot, "could not clone", "specs: could not read the source — did not look")
		run := f.lastCall(t)
		if strings.Contains(flagged(run), "--source") || strings.Contains(strings.Join(run.Env, " "), "CONSOLE_SPECS_SOURCE") {
			t.Errorf("the atom named a source, which would pin the live clone: %+v", run)
		}
	})
	t.Run("drift is a finding", func(t *testing.T) {
		in, _ := checkerTree(t, "console-specs", 0o755, func(Cmd) (string, int) { return "drift: repo-console.readmodel.spec", 1 })
		expect(t, runAtom(t, "ops:specs", in), stateOf(1), findings, "drift: repo-console.readmodel.spec", "specs failed (rc=1) — findings")
	})
}
