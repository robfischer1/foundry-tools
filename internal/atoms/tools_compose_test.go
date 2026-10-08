package atoms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const composeSpec = "services:\n  a:\n    image: x:1\n    env_file:\n      - ./secrets/app.env\n"

func TestComposeConfig(t *testing.T) {
	const id = "compose:config"
	for _, tc := range []struct {
		name    string
		files   map[string]string
		answer  func(c Cmd) (string, int)
		state   int
		result  string
		needles []string
	}{
		{"every spec parses", map[string]string{"compose.yaml": composeSpec, "sub/compose.yml": "services: {}\n"}, nil, 0, pass,
			[]string{"every tracked compose spec parses", "1 env_file reference(s) stubbed", "compose.yaml", "sub/compose.yml"}},
		{"a spec that does not parse is a finding with the tool's words", map[string]string{"compose.yaml": composeSpec, "sub/compose.yml": "services: {}\n"},
			func(c Cmd) (string, int) {
				if len(c.Args) > 1 && c.Args[1] == "sub/compose.yml" {
					return "yaml: line 2: mapping values", 1
				}
				return "", 0
			}, 1, findings, []string{"a tracked compose spec does not parse", "sub/compose.yml", "FAIL", "      yaml: line 2: mapping values"}},
		{"a client that would not start never parsed", map[string]string{"compose.yaml": composeSpec},
			func(c Cmd) (string, int) {
				if c.Args[0] == "-f" {
					return "docker-compose: gone", -1
				}
				return "", 0
			}, 2, cannot, []string{"the parse of compose.yaml never ran: docker-compose: gone"}},
		{"a client that fails its probe", map[string]string{"compose.yaml": composeSpec},
			func(Cmd) (string, int) { return "gone", 127 }, 2, cannot, []string{"the pinned docker/compose client", "did not run", "gone"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dirs []string
			in, f := toolTree(t, tc.files, nil)
			f.answer = func(c Cmd) (string, int) {
				if c.Args[0] == "-f" {
					dirs = append(dirs, c.Dir)
				}
				if tc.answer == nil {
					return "", 0
				}
				return tc.answer(c)
			}
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
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
	t.Run("the parse runs in a private copy and the shared tree is not written", func(t *testing.T) {
		files := map[string]string{
			"compose.yaml": composeSpec + "    # also ../up.env and tracked.env\n",
			"tracked.env":  "KEY=1\n",
			"big.bin":      "not part of the parse",
		}
		in, f := toolTree(t, files, nil)
		before := listTree(t, in.Root)
		var seen []string
		f.answer = func(c Cmd) (string, int) {
			if c.Args[0] != "-f" {
				return "", 0
			}
			if want := "-f compose.yaml config --no-interpolate --quiet"; flagged(c) != want {
				t.Errorf("compose was run as %q, want %q", flagged(c), want)
			}
			if c.Dir == in.Root {
				t.Fatalf("compose parsed in the shared tree")
			}
			seen = listTree(t, c.Dir)
			if _, err := os.Stat(filepath.Join(filepath.Dir(c.Dir), "up.env")); err == nil {
				t.Errorf("a stub for ../up.env was written beside the copy")
			}
			return "", 0
		}
		expect(t, runAtom(t, id, in), stateOf(0), pass, "2 env_file reference(s) stubbed")
		if got, want := strings.Join(seen, ","), "compose.yaml,secrets/app.env,tracked.env,up.env"; got != want {
			t.Errorf("the private copy held %s, want %s", got, want)
		}
		if after := listTree(t, in.Root); strings.Join(after, ",") != strings.Join(before, ",") {
			t.Errorf("the shared tree changed: before %v, after %v", before, after)
		}
	})
	t.Run("a tracked env file is copied, not stubbed over", func(t *testing.T) {
		in, f := toolTree(t, map[string]string{"compose.yaml": strings.Replace(composeSpec, "./secrets/app.env", "./app.env", 1), "app.env": "KEY=1\n"}, nil)
		f.answer = func(c Cmd) (string, int) {
			if c.Args[0] == "-f" {
				if body, err := os.ReadFile(filepath.Join(c.Dir, "app.env")); err != nil || string(body) != "KEY=1\n" {
					t.Errorf("the tracked env file in the copy: %q, %v", body, err)
				}
			}
			return "", 0
		}
		expect(t, runAtom(t, id, in), stateOf(0), pass, "0 env_file reference(s) stubbed")
	})
	t.Run("no spec is absent and the client is not touched", func(t *testing.T) {
		in, f := toolTree(t, map[string]string{"a.yml": "x: 1\n"}, nil)
		expect(t, runAtom(t, id, in), stateOf(0), absent, "tracks no compose.yaml/compose.yml")
		if len(f.calls) != 0 {
			t.Errorf("an absent atom touched %v", f.ran())
		}
	})
	t.Run("a scan that failed is not an absence", func(t *testing.T) {
		in, _ := toolTree(t, map[string]string{"compose.yaml": composeSpec}, nil)
		in.FilesErr = errBoom
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the compose-surface scan itself failed")
	})
	t.Run("a body that will not read is a scan that did not run", func(t *testing.T) {
		in, f := toolTree(t, map[string]string{"compose.yaml": composeSpec}, nil)
		in.Committable = append(in.Committable, "ghost.yml")
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the env_file scan failed (could not read ghost.yml")
		if len(f.calls) != 0 {
			t.Errorf("the client ran after a failed scan: %v", f.ran())
		}
	})
	t.Run("a copy that cannot be made is a could-not-run", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocker, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TMPDIR", filepath.Join(blocker, "sub"))
		in, f := toolTree(t, map[string]string{"compose.yaml": composeSpec}, nil)
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the private copy the parse runs in could not be made", "not a directory")
		if len(f.calls) != 1 {
			t.Errorf("a parse ran without its copy: %v", f.ran())
		}
	})
	t.Run("a tracked file that vanished cannot be scanned", func(t *testing.T) {
		in, _ := toolTree(t, map[string]string{"compose.yaml": composeSpec}, nil)
		if err := os.Remove(filepath.Join(in.Root, "compose.yaml")); err != nil {
			t.Fatal(err)
		}
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the env_file scan failed")
	})
}

// listTree is every file under dir, slash-separated and sorted.
func listTree(t *testing.T, dir string) []string {
	t.Helper()
	files, err := tree{root: dir}.files()
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestPrivateCopy(t *testing.T) {
	root := t.TempDir()
	put(t, root, "a.yml", "a")
	put(t, root, "d/b.yml", "b")
	dir := filepath.Join(t.TempDir(), "copy")
	if err := privateCopy(dir, root, []string{"a.yml", "d/b.yml"}, []string{"./x.env", "../up.env", "s/y.env"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(listTree(t, dir), ","); got != "a.yml,d/b.yml,s/y.env,up.env,x.env" {
		t.Errorf("the copy holds %s", got)
	}
	if body, _ := os.ReadFile(filepath.Join(dir, "d/b.yml")); string(body) != "b" {
		t.Errorf("a copied file lost its bytes: %q", body)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "up.env")); err == nil {
		t.Errorf("a stub escaped the copy")
	}
	t.Run("a tracked file that is not there is an error", func(t *testing.T) {
		err := privateCopy(filepath.Join(t.TempDir(), "copy"), root, []string{"gone.yml"}, nil)
		if err == nil || !strings.Contains(err.Error(), "gone.yml") {
			t.Errorf("a missing tracked file was copied: %v", err)
		}
	})
	// A directory that sits under a file can never be made, so every write fails.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	under := filepath.Join(blocker, "copy")
	t.Run("a copy that cannot be written is an error", func(t *testing.T) {
		if err := privateCopy(under, root, []string{"a.yml"}, nil); err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("a copy into a place that is not a directory: %v", err)
		}
	})
	t.Run("a stub that cannot be written is an error", func(t *testing.T) {
		if err := privateCopy(under, root, nil, []string{"x.env"}); err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("a stub into a place that is not a directory: %v", err)
		}
	})
}

func TestWitValidate(t *testing.T) {
	const id = "wit:validate"
	files := map[string]string{"wit/a.wit": "package a:b;\n", "justfile": "validate:\n\twasm-tools component wit wit/\n"}
	for _, tc := range []struct {
		name    string
		recipe  string
		code    int
		state   int
		result  string
		needles []string
	}{
		{"a recipe that passes", "ok", 0, 0, pass, nil},
		{"the recipe's exit 1 passes through as a finding", "error: unresolved type", 1, 1, findings, []string{"unresolved type"}},
		{"any other exit is a could-not-run", "boom", 101, 2, cannot, []string{"boom"}},
		{"a just that would not start never ran", "just: gone", -1, 2, cannot, []string{"the atom never ran: just: gone"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := toolTree(t, files, func(c Cmd) (string, int) {
				if c.Args[0] == "--version" {
					return "1.0", 0
				}
				return tc.recipe, tc.code
			})
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
			if got := strings.Join(f.ran(), " "); got != "wasm-tools just just" {
				t.Errorf("ran %s", got)
			}
			c := f.calls[2]
			if flagged(c) != "validate" || c.Dir != in.Root || !c.Both {
				t.Errorf("the recipe ran as %q in %q (both streams %v)", flagged(c), c.Dir, c.Both)
			}
		})
	}
	for _, tool := range []string{"wasm-tools", "just"} {
		t.Run("a "+tool+" that fails its probe", func(t *testing.T) {
			in, f := toolTree(t, files, func(c Cmd) (string, int) {
				if c.Name == tool {
					return "gone", 127
				}
				return "1.0", 0
			})
			expect(t, runAtom(t, id, in), stateOf(2), cannot, tool+" --version exited 127", "WIT that was never resolved is not WIT that passed")
			for _, c := range f.calls {
				if c.Name == "just" && c.Args[0] == "validate" {
					t.Errorf("the recipe ran with %s unprovisioned", tool)
				}
			}
		})
	}
	for name, tc := range map[string]struct {
		files map[string]string
		why   string
	}{
		"no WIT":                      {map[string]string{"justfile": "validate:\n\ttrue\n"}, "tracks no wit/*.wit"},
		"WIT and no justfile":         {map[string]string{"wit/a.wit": "x"}, "tracks WIT but no root justfile"},
		"a justfile with no validate": {map[string]string{"wit/a.wit": "x", "justfile": "build:\n\ttrue\n"}, "defines no validate recipe"},
	} {
		t.Run(name+" is absent and no tool is touched", func(t *testing.T) {
			in, f := toolTree(t, tc.files, nil)
			expect(t, runAtom(t, id, in), stateOf(0), absent, tc.why)
			if len(f.calls) != 0 {
				t.Errorf("an absent atom touched %v", f.ran())
			}
		})
	}
	t.Run("a justfile that would not read", func(t *testing.T) {
		in, _ := toolTree(t, files, nil)
		if err := os.Remove(filepath.Join(in.Root, "justfile")); err != nil {
			t.Fatal(err)
		}
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the justfile would not read")
	})
	t.Run("a population that failed", func(t *testing.T) {
		in, _ := toolTree(t, files, nil)
		in.FilesErr = errBoom
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the tree would not enumerate")
	})
}
