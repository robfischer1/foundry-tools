package atoms

import (
	"os"
	"path/filepath"
	"slices"
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
	t.Run("the parse runs in a private copy of every tracked file and the shared tree is not written", func(t *testing.T) {
		files := map[string]string{
			"compose.yaml": composeSpec + "    # also ../up.env and tracked.env\n",
			"tracked.env":  "KEY=1\n",
			// Not YAML and not an env file the spec names: a label_file, an extends
			// or an include target could look like this, and the copy holds it.
			"labels/app.labels": "a=b\n",
			"big.bin":           "not named by the spec",
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
		if got, want := strings.Join(seen, ","), "big.bin,compose.yaml,labels/app.labels,secrets/app.env,tracked.env,up.env"; got != want {
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
	t.Run("a link is copied as a link, a mode is kept and a stub is laid over a file", func(t *testing.T) {
		src := t.TempDir()
		put(t, src, "run.sh", "#!/bin/sh\n")
		put(t, src, "kept.txt", "original\n")
		if err := os.Chmod(filepath.Join(src, "run.sh"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("run.sh", filepath.Join(src, "alias.sh")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("nowhere", filepath.Join(src, "dangling")); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(t.TempDir(), "copy")
		if err := privateCopy(dst, src, []string{"alias.sh", "dangling", "run.sh", "kept.txt"}, []string{"kept.txt"}); err != nil {
			t.Fatal(err)
		}
		if target, err := os.Readlink(filepath.Join(dst, "alias.sh")); err != nil || target != "run.sh" {
			t.Errorf("the link: %q, %v", target, err)
		}
		if target, err := os.Readlink(filepath.Join(dst, "dangling")); err != nil || target != "nowhere" {
			t.Errorf("a link to nowhere is a link: %q, %v", target, err)
		}
		if fi, err := os.Stat(filepath.Join(dst, "run.sh")); err != nil || fi.Mode().Perm() != 0o755 {
			t.Errorf("the script's mode: %v, %v", fi, err)
		}
		if body, _ := os.ReadFile(filepath.Join(dst, "kept.txt")); len(body) != 0 {
			t.Errorf("the stub did not replace the file: %q", body)
		}
		if body, _ := os.ReadFile(filepath.Join(src, "kept.txt")); string(body) != "original\n" {
			t.Errorf("the stub reached the source: %q", body)
		}
	})
	t.Run("a link that cannot be made, and a path that cannot be read, are errors", func(t *testing.T) {
		src := t.TempDir()
		if err := os.Symlink("x", filepath.Join(src, "l")); err != nil {
			t.Fatal(err)
		}
		blocker := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocker, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := privateCopy(filepath.Join(blocker, "copy"), src, []string{"l"}, nil); err == nil {
			t.Error("a link into a place that is not a directory was made")
		}
		put(t, src, "dir/f", "x")
		if err := privateCopy(filepath.Join(t.TempDir(), "copy"), src, []string{"dir"}, nil); err == nil {
			t.Error("a directory was copied as a file")
		}
	})
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
				if c.Name == "just" && c.Args[0] == "validate" {
					return tc.recipe, tc.code
				}
				return "1.0", 0
			})
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
			// The probes, the private tree's repository, and then the recipe.
			if got := strings.Join(f.ran(), " "); got != "wasm-tools just git git just" {
				t.Errorf("ran %s", got)
			}
			c := f.calls[4]
			if flagged(c) != "validate" || c.Dir == in.Root || !c.Both {
				t.Errorf("the recipe ran as %q in %q (both streams %v): not the shared tree", flagged(c), c.Dir, c.Both)
			}
			if _, err := os.Stat(c.Dir); err == nil {
				t.Errorf("the private tree %s was left behind", c.Dir)
			}
			for _, g := range f.calls[2:4] {
				if g.Dir != c.Dir || !slices.Contains(g.Env, "GIT_CONFIG_NOSYSTEM=1") {
					t.Errorf("git %v ran in %q with %v: the private tree's index, off the developer's configuration", g.Args, g.Dir, g.Env)
				}
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
	t.Run("a recipe that writes leaves the shared tree as it was", func(t *testing.T) {
		in, _ := toolTree(t, files, func(c Cmd) (string, int) {
			if c.Name == "just" && c.Args[0] == "validate" {
				put(t, c.Dir, "wit/generated.wit", "written by the recipe")
				put(t, c.Dir, ".cache/x", "a cache")
				if body, err := os.ReadFile(filepath.Join(c.Dir, "wit/a.wit")); err != nil || string(body) != "package a:b;\n" {
					t.Errorf("the recipe could not read the tree it was handed: %q, %v", body, err)
				}
			}
			return "ok", 0
		})
		before := listTree(t, in.Root)
		expect(t, runAtom(t, id, in), stateOf(0), pass)
		if after := listTree(t, in.Root); strings.Join(after, ",") != strings.Join(before, ",") {
			t.Errorf("the shared tree changed: before %v, after %v", before, after)
		}
		if body, _ := os.ReadFile(filepath.Join(in.Root, "justfile")); string(body) != files["justfile"] {
			t.Errorf("a tracked file changed: %q", body)
		}
	})
	t.Run("a private tree that cannot be made is a could-not-run and the recipe does not run", func(t *testing.T) {
		in, f := toolTree(t, files, func(c Cmd) (string, int) {
			if c.Name == "git" {
				return "fatal: no", 128
			}
			return "1.0", 0
		})
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the private tree the recipe runs in could not be made", "git init -q . exited 128")
		for _, c := range f.calls {
			if c.Name == "just" && c.Args[0] == "validate" {
				t.Error("the recipe ran without its tree")
			}
		}
	})
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
