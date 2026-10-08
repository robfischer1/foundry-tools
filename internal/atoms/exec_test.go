package atoms

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// THE VENV'S PROGRAMS ARE FOUND BY PATH, NOT BY THE CONTAINER'S PATH. A shadow
// on cerberus reported `exec: "python3": executable file not found in $PATH`
// while the chain passed: whatever PATH the container was left with, the venv's
// programs run from the venv.
func TestRunProgramFindsTheVenvsProgramsWhateverThePathIs(t *testing.T) {
	venv := t.TempDir()
	for _, name := range pythonPrograms {
		script := "#!/bin/sh\necho venv-" + name + " $PYPATH\n"
		if err := os.WriteFile(filepath.Join(venv, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := pythonBinDir
	pythonBinDir = venv
	t.Cleanup(func() { pythonBinDir = old })
	t.Setenv("PATH", "/usr/bin:/bin") // the venv is not on it

	for _, name := range pythonPrograms {
		t.Run(name, func(t *testing.T) {
			out, code := RunProgram(context.Background(), Cmd{Dir: t.TempDir(), Name: name})
			if code != 0 || !strings.HasPrefix(out, "venv-"+name) {
				t.Errorf("got %q, %d: the venv's %s was not run", out, code, name)
			}
		})
	}
	t.Run("a program run by the venv's programs finds its siblings on PATH", func(t *testing.T) {
		out, code := RunProgram(context.Background(), Cmd{Dir: t.TempDir(), Name: "sh", Args: []string{"-c", "echo $PATH"}})
		if code != 0 || !strings.HasPrefix(out, venv+string(os.PathListSeparator)) {
			t.Errorf("PATH %q does not start with the venv's bin", out)
		}
	})
	t.Run("Env still has the last word", func(t *testing.T) {
		out, _ := RunProgram(context.Background(), Cmd{Dir: t.TempDir(), Name: "python3", Env: []string{"PYPATH=x"}})
		if out != "venv-python3 x" {
			t.Errorf("got %q", out)
		}
	})
	t.Run("a program the venv does not provide is left to the PATH", func(t *testing.T) {
		if got := programPath("git"); got != "git" {
			t.Errorf("git resolved to %q", got)
		}
	})
	t.Run("a venv program that is not there is left to the PATH, and fails as it would", func(t *testing.T) {
		pythonBinDir = filepath.Join(venv, "nowhere")
		if got := programPath("python3"); got != "python3" {
			t.Errorf("python3 resolved to %q with no venv", got)
		}
		env := programEnv([]string{"A=b"})
		if !slices.Contains(env, "A=b") || slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, "PATH=") && strings.Contains(e, "nowhere") }) {
			t.Errorf("env %v", env)
		}
	})
	t.Run("a directory of that name is not a program", func(t *testing.T) {
		d := t.TempDir()
		if err := os.Mkdir(filepath.Join(d, "python3"), 0o755); err != nil {
			t.Fatal(err)
		}
		pythonBinDir = d
		if got := programPath("python3"); got != "python3" {
			t.Errorf("a directory resolved: %q", got)
		}
	})
}

// EVERY EXEC NAME THE PYTHON ATOMS USE IS ONE OF THE VENV'S PROGRAMS (and so
// resolves in the container), and is listed in Programs. The names are read off
// the source: a string that names an interpreter or an ansible/copier entry
// point, wherever it is spelled (a Cmd, a probe, a checker's python field).
func TestEveryPythonExecNameIsAVenvProgram(t *testing.T) {
	name := regexp.MustCompile(`^(python3?|copier|ansible-[a-z]+)$`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if v, _ := strconv.Unquote(lit.Value); name.MatchString(v) {
					seen[v] = true
					if !slices.Contains(pythonPrograms, v) {
						t.Errorf("%s: %s execs %q, which is not a program the venv provides", f, fset.Position(lit.Pos()), v)
					}
				}
			}
			return true
		})
	}
	for _, want := range []string{"python3", "python", "copier", "ansible-playbook", "ansible-lint"} {
		if !seen[want] {
			t.Errorf("no source spells %q; the scan is not reading the atoms", want)
		}
	}
	for _, p := range pythonPrograms {
		if !slices.Contains(Programs, p) {
			t.Errorf("%q is a venv program and Programs does not list it", p)
		}
	}
}

func TestOnlyTheVenvsProgramsAreResolvedThere(t *testing.T) {
	venv := t.TempDir()
	if err := os.WriteFile(filepath.Join(venv, "git"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := pythonBinDir
	pythonBinDir = venv
	t.Cleanup(func() { pythonBinDir = old })
	if got := programPath("git"); got != "git" {
		t.Errorf("git resolved to %q: a file of that name in the venv is not the venv's program", got)
	}
}

func TestAMissingVenvIsNamedNotLeftToThePathError(t *testing.T) {
	old := pythonBinDir
	pythonBinDir = filepath.Join(t.TempDir(), "no-venv")
	t.Cleanup(func() { pythonBinDir = old })
	t.Setenv("PATH", t.TempDir()) // nothing on it
	out, code := RunProgram(context.Background(), Cmd{Dir: t.TempDir(), Name: "python3"})
	if code != -1 || out != errNoVenv {
		t.Errorf("got %q, %d", out, code)
	}
	if !strings.Contains(errNoVenv, "atoms tools:") {
		t.Errorf("the message does not point at the stderr line: %s", errNoVenv)
	}
	t.Run("a program that is not the venv's keeps its own error", func(t *testing.T) {
		out, code := RunProgram(context.Background(), Cmd{Dir: t.TempDir(), Name: "no-such-program-here"})
		if code != -1 || out == errNoVenv || !strings.HasPrefix(out, "no-such-program-here: ") {
			t.Errorf("got %q, %d", out, code)
		}
	})
	t.Run("a python the PATH still finds is run", func(t *testing.T) {
		bin := t.TempDir()
		if err := os.WriteFile(filepath.Join(bin, "python3"), []byte("#!/bin/sh\necho local\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin)
		if out, code := RunProgram(context.Background(), Cmd{Dir: t.TempDir(), Name: "python3"}); code != 0 || out != "local" {
			t.Errorf("got %q, %d", out, code)
		}
	})
}
