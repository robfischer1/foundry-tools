package composeconfig

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// run is the atom's check over an input, as the runner calls it.
func run(in atoms.Input) checks.Verdict { return check(context.Background(), in) }

func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	atomtest.DeclaresItsRow(t, Def, "precommit", "none")
	if Def.ID != id {
		t.Errorf("%+v", Def)
	}
}

const composeSpec = "services:\n  a:\n    image: x:1\n    env_file:\n      - ./secrets/app.env\n"

func TestComposeConfig(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   map[string]string
		answer  func(c atoms.Cmd) (string, int)
		state   int
		result  string
		needles []string
	}{
		{"every spec parses", map[string]string{"compose.yaml": composeSpec, "sub/compose.yml": "services: {}\n"}, nil, 0, atomtest.Pass,
			[]string{"every tracked compose spec parses", "1 env_file reference(s) stubbed", "compose.yaml", "sub/compose.yml"}},
		{"a spec that does not parse is a finding with the tool's words", map[string]string{"compose.yaml": composeSpec, "sub/compose.yml": "services: {}\n"},
			func(c atoms.Cmd) (string, int) {
				if len(c.Args) > 1 && c.Args[1] == "sub/compose.yml" {
					return "yaml: line 2: mapping values", 1
				}
				return "", 0
			}, 1, atomtest.Findings, []string{"a tracked compose spec does not parse", "sub/compose.yml", "FAIL", "      yaml: line 2: mapping values"}},
		{"a client that would not start never parsed", map[string]string{"compose.yaml": composeSpec},
			func(c atoms.Cmd) (string, int) {
				if c.Args[0] == "-f" {
					return "docker-compose: gone", -1
				}
				return "", 0
			}, 2, atomtest.Cannot, []string{"the parse of compose.yaml never ran: docker-compose: gone"}},
		{"a client that fails its probe", map[string]string{"compose.yaml": composeSpec},
			func(atoms.Cmd) (string, int) { return "gone", 127 }, 2, atomtest.Cannot, []string{"the pinned docker/compose client", "did not run", "gone"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dirs []string
			in, f := atomtest.ToolTree(t, tc.files, nil)
			f.Answer = func(c atoms.Cmd) (string, int) {
				if c.Args[0] == "-f" {
					dirs = append(dirs, c.Dir)
				}
				if tc.answer == nil {
					return "", 0
				}
				return tc.answer(c)
			}
			atomtest.Expect(t, run(in), atomtest.StateOf(tc.state), tc.result, tc.needles...)
			for _, d := range dirs {
				if d == in.Root {
					t.Errorf("compose parsed in the shared tree")
				}
				if _, err := os.Stat(d); err == nil {
					t.Errorf("the private copy %s was left behind", d)
				}
			}
		})
	}
	t.Run("the parse runs in a private copy of every tracked file and the shared tree is not written", func(t *testing.T) {
		files := map[string]string{
			"compose.yaml": composeSpec + "    # also ../up.env and tracked.env\n",
			"tracked.env":  "KEY=1\n",
			// Not YAML and not an env file the spec names: a label_file, an extends
			// or an include target could look like this, and the copy holds it.
			"labels/app.labels": "a=b\n",
			"big.bin":           "not named by the spec",
		}
		in, f := atomtest.ToolTree(t, files, nil)
		before := atomtest.ListTree(t, in.Root)
		var seen []string
		f.Answer = func(c atoms.Cmd) (string, int) {
			if c.Args[0] != "-f" {
				return "", 0
			}
			if want := "-f compose.yaml config --no-interpolate --quiet"; atomtest.Flagged(c) != want {
				t.Errorf("compose was run as %q, want %q", atomtest.Flagged(c), want)
			}
			if c.Dir == in.Root {
				t.Fatalf("compose parsed in the shared tree")
			}
			seen = atomtest.ListTree(t, c.Dir)
			if _, err := os.Stat(filepath.Join(filepath.Dir(c.Dir), "up.env")); err == nil {
				t.Errorf("a stub for ../up.env was written beside the copy")
			}
			return "", 0
		}
		atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass, "2 env_file reference(s) stubbed")
		if got, want := strings.Join(seen, ","), "big.bin,compose.yaml,labels/app.labels,secrets/app.env,tracked.env,up.env"; got != want {
			t.Errorf("the private copy held %s, want %s", got, want)
		}
		if after := atomtest.ListTree(t, in.Root); strings.Join(after, ",") != strings.Join(before, ",") {
			t.Errorf("the shared tree changed: before %v, after %v", before, after)
		}
	})
	t.Run("a tracked env file is copied, not stubbed over", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, map[string]string{"compose.yaml": strings.Replace(composeSpec, "./secrets/app.env", "./app.env", 1), "app.env": "KEY=1\n"}, nil)
		f.Answer = func(c atoms.Cmd) (string, int) {
			if c.Args[0] == "-f" {
				if body, err := os.ReadFile(filepath.Join(c.Dir, "app.env")); err != nil || string(body) != "KEY=1\n" {
					t.Errorf("the tracked env file in the copy: %q, %v", body, err)
				}
			}
			return "", 0
		}
		atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass, "0 env_file reference(s) stubbed")
	})
	t.Run("no spec is absent and the client is not touched", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, map[string]string{"a.yml": "x: 1\n"}, nil)
		atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Absent, "tracks no compose.yaml/compose.yml")
		if len(f.Calls) != 0 {
			t.Errorf("an absent atom touched %v", f.Ran())
		}
	})
	t.Run("a scan that failed is not an absence", func(t *testing.T) {
		in, _ := atomtest.ToolTree(t, map[string]string{"compose.yaml": composeSpec}, nil)
		in.FilesErr = atomtest.ErrBoom
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the compose-surface scan itself failed")
	})
	t.Run("a body that will not read is a scan that did not run", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, map[string]string{"compose.yaml": composeSpec}, nil)
		in.Committable = append(in.Committable, "ghost.yml")
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the env_file scan failed (could not read ghost.yml")
		if len(f.Calls) != 0 {
			t.Errorf("the client ran after a failed scan: %v", f.Ran())
		}
	})
	t.Run("a copy that cannot be made is a could-not-run", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocker, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TMPDIR", filepath.Join(blocker, "sub"))
		in, f := atomtest.ToolTree(t, map[string]string{"compose.yaml": composeSpec}, nil)
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the private copy the parse runs in could not be made", "not a directory")
		if len(f.Calls) != 1 {
			t.Errorf("a parse ran without its copy: %v", f.Ran())
		}
	})
	t.Run("a tracked file that vanished cannot be scanned", func(t *testing.T) {
		in, _ := atomtest.ToolTree(t, map[string]string{"compose.yaml": composeSpec}, nil)
		if err := os.Remove(filepath.Join(in.Root, "compose.yaml")); err != nil {
			t.Fatal(err)
		}
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the env_file scan failed")
	})
}
