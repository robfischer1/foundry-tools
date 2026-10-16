package opsshell

import (
	"context"
	"io/fs"
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

// openFailsFS is a file system whose files will not open.
type openFailsFS struct{ fs.FS }

func (openFailsFS) Open(string) (fs.File, error) { return nil, atomtest.ErrBoom }

// readFailsFS is a file system whose files open and then fail to read.
type readFailsFS struct{ fs.FS }

func (f readFailsFS) Open(name string) (fs.File, error) {
	fh, err := f.FS.Open(name)
	return readFails{fh}, err
}

type readFails struct{ fs.File }

func (readFails) Read([]byte) (int, error) { return 0, atomtest.ErrBoom }

// shellTree is an ops tree (it carries flux/) with one shebang script, one
// script found by its interpreter alone, a sourced fragment, a zsh file and a
// binary that starts like a script.
func shellTree() map[string]string {
	return map[string]string{
		"flux/x.yaml": "a: 1\n",
		"run.sh":      "#!/bin/bash\necho hi\n",
		"bin/tool":    "#!/usr/bin/env bash\necho hi\n",
		"lib.sh":      "echo sourced\n",
		"rc.zsh":      "#!/bin/zsh\n",
		"blob":        "#!/bin/bash\n\x00\x01",
		"notes.txt":   "#! not a script, no shell named\n",
	}
}

func TestOpsShell(t *testing.T) {
	ok := func(c atoms.Cmd) (string, int) { return "ShellCheck - shell script analysis tool\nversion: 0.11.0", 0 }
	for _, tc := range []struct {
		name    string
		answer  func(c atoms.Cmd) (string, int)
		state   int
		result  string
		needles []string
	}{
		{"clean scripts pass, the debt is counted and not gating", func(c atoms.Cmd) (string, int) {
			if c.Args[0] == "-S" && c.Args[1] == "warning" {
				return "a.sh:1:1: warning: x [SC1]\n", 1
			}
			return ok(c)
		}, 0, atomtest.Pass, []string{"shell: 3 script(s), gating at severity error", "shellcheck -S warning: 2 finding(s) — reported, not gating"}},
		{"a finding at error severity is the tool's exit 1", func(c atoms.Cmd) (string, int) {
			if c.Args[0] == "-S" && c.Args[1] == "error" {
				return "run.sh:2:1: error: unclosed if [SC1046]\n", 1
			}
			return ok(c)
		}, 1, atomtest.Findings, []string{"run.sh:2:1: error: unclosed if", "shell failed (rc=1) — findings"}},
		{"a fault of the substrate is a 2, not a finding", func(c atoms.Cmd) (string, int) {
			if c.Args[0] == "-S" && c.Args[1] == "error" {
				return "connection refused", 1
			}
			return ok(c)
		}, 2, atomtest.Cannot, []string{"failed on a fault of the substrate"}},
		{"a shellcheck that would not start never ran", func(c atoms.Cmd) (string, int) {
			if c.Args[0] == "-S" {
				return "shellcheck: gone", -1
			}
			return ok(c)
		}, 2, atomtest.Cannot, []string{"the atom never ran: shellcheck: gone"}},
		{"a shellcheck that fails its probe is unprovisioned", func(atoms.Cmd) (string, int) { return "shellcheck: gone", 127 }, 2, atomtest.Cannot,
			[]string{"the phase's tool could not be provisioned", "shellcheck --version exited 127"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := atomtest.ToolTree(t, shellTree(), tc.answer)
			atomtest.Expect(t, run(in), atomtest.StateOf(tc.state), tc.result, tc.needles...)
			if tc.state == 0 {
				var lines []string
				for _, c := range f.Calls {
					lines = append(lines, atomtest.Flagged(c))
					if c.Dir != in.Root || !c.Both {
						t.Errorf("%s ran in %q (both streams %v)", c.Name, c.Dir, c.Both)
					}
				}
				want := []string{
					"--version",
					"-S error -f gcc bin/tool run.sh", "-S error -s bash -f gcc lib.sh",
					"-S warning -f gcc bin/tool run.sh", "-S warning -s bash -f gcc lib.sh",
				}
				if strings.Join(lines, "|") != strings.Join(want, "|") {
					t.Errorf("shellcheck ran as\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
				}
			}
		})
	}
	t.Run("a symlink is not read for a shebang, so it is a sourced fragment as git saw it", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, shellTree(), nil)
		if err := os.Symlink("run.sh", filepath.Join(in.Root, "link.sh")); err != nil {
			t.Fatal(err)
		}
		in.Committable = append(in.Committable, "link.sh")
		atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass, "shell: 4 script(s)")
		var lines []string
		for _, c := range f.Calls[1:] {
			lines = append(lines, atomtest.Flagged(c))
		}
		want := []string{
			"-S error -f gcc bin/tool run.sh", "-S error -s bash -f gcc lib.sh link.sh",
			"-S warning -f gcc bin/tool run.sh", "-S warning -s bash -f gcc lib.sh link.sh",
		}
		if strings.Join(lines, "|") != strings.Join(want, "|") {
			t.Errorf("shellcheck ran as\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
		}
	})
	t.Run("a template and a zsh file are never opened", func(t *testing.T) {
		in, _ := atomtest.ToolTree(t, shellTree(), nil)
		in.Committable = append(in.Committable, "ghost.tmpl", "ghost.zsh")
		atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass, "shell: 3 script(s)")
	})
	for name, files := range map[string]map[string]string{
		"shebang scripts alone":   {"flux/x.yaml": "a: 1\n", "run.sh": "#!/bin/bash\n"},
		"sourced fragments alone": {"flux/x.yaml": "a: 1\n", "lib.sh": "echo sourced\n"},
	} {
		t.Run(name+" are enough to run shellcheck", func(t *testing.T) {
			in, f := atomtest.ToolTree(t, files, nil)
			atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass, "shell: 1 script(s)")
			if len(f.Calls) != 3 {
				t.Errorf("ran %v", f.Ran())
			}
		})
	}
	for name, severity := range map[string]string{"the gating pass": "error", "the report pass": "warning"} {
		t.Run("a shellcheck that would not start in "+name+" never ran", func(t *testing.T) {
			in, _ := atomtest.ToolTree(t, shellTree(), func(c atoms.Cmd) (string, int) {
				if c.Args[0] == "-S" && c.Args[1] == severity {
					return "shellcheck: gone", -1
				}
				return "", 0
			})
			atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the atom never ran: shellcheck: gone")
		})
	}
	for name, fsys := range map[string]func(root string) fs.FS{
		"opens": func(root string) fs.FS { return openFailsFS{os.DirFS(root)} },
		"reads": func(root string) fs.FS { return readFailsFS{os.DirFS(root)} },
	} {
		t.Run("a script that never "+name+" is a could-not-run before any tool", func(t *testing.T) {
			in, f := atomtest.ToolTree(t, shellTree(), nil)
			in.FS = fsys(in.Root)
			atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "shell: could not read the scripts' first lines: boom")
			if len(f.Calls) != 0 {
				t.Errorf("shellcheck ran over a tree it could not read: %v", f.Ran())
			}
		})
	}
	for name, files := range map[string]map[string]string{
		"a tree that is not an ops tree": {"run.sh": "#!/bin/bash\n"},
		"an ops tree with no shell":      {"flux/x.yaml": "a: 1\n", "notes.txt": "hello\n"},
	} {
		t.Run(name+" is absent and shellcheck is not touched", func(t *testing.T) {
			in, f := atomtest.ToolTree(t, files, nil)
			atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Absent)
			if len(f.Calls) != 0 {
				t.Errorf("an absent atom touched %v", f.Ran())
			}
		})
	}
	t.Run("a script that will not read is a could-not-run before any tool", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, shellTree(), nil)
		in.Committable = append(in.Committable, "ghost.sh")
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "shell: could not read the scripts' first lines", "ghost.sh")
		if len(f.Calls) != 0 {
			t.Errorf("shellcheck ran over a tree it could not read: %v", f.Ran())
		}
	})
	t.Run("a directory listed as a file is skipped like a link", func(t *testing.T) {
		in, _ := atomtest.ToolTree(t, shellTree(), nil)
		atomtest.Put(t, in.Root, "dir.sh/inner", "x")
		in.Committable = append(in.Committable, "dir.sh")
		atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass)
	})
	t.Run("a population that failed", func(t *testing.T) {
		in, _ := atomtest.ToolTree(t, shellTree(), nil)
		in.FilesErr = atomtest.ErrBoom
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the tree would not enumerate")
	})
}
