package atoms

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// Cmd is one program an atom runs: the few atoms whose tool is a program (opa,
// uv, orbitparse, tar) and not a function. They run as plain execs with a fixed
// argument vector, never a shell, exactly as the chains' WithExec calls did.
type Cmd struct {
	// Dir is the working directory: the repository root, where the chains'
	// containers were workdir /src.
	Dir  string
	Name string
	Args []string
	// Env is added to the process environment, as the chains' WithEnvVariable
	// was added to the container's (opengrep decodes source by the locale).
	Env []string
	// Stdin is what the program reads, as the chains' ContainerWithExecOpts
	// Stdin was; empty reads nothing.
	Stdin string
	// StdoutOnly answers stdout alone, untrimmed, on every exit: the module's
	// Container.Stdout, which a failing program's stderr is never part of. The
	// one reader is ansible-lint's debt count, which the chain took off stdout
	// and which a stderr folded in would change. It outranks Both.
	StdoutOnly bool
	// Both answers stdout and stderr on EVERY exit, as the module's
	// outputBoth and verdict() do. Without it the output is the module's
	// output(): stdout, plus stderr only when the program exited non-zero,
	// trimmed.
	Both bool
}

// Exec runs a Cmd and answers what it printed and how it exited. A program that
// would not START is exit -1 with the reason as its output, so "opa is not
// installed" is refused by the same line as "opa said no" and no error path
// sits beside it that a test cannot reach.
type Exec func(ctx context.Context, c Cmd) (out string, code int)

// pythonBinDir is the venv's bin as RunProgram looks for it; a variable so a
// test can say where the venv is.
var pythonBinDir = PythonBinDir

// pythonPrograms are the programs the venv provides. They are found there by
// path and not by the container's PATH: a shadow measured on cerberus
// (gate-cerberus-038a512-tth5v) reported `exec: "python3": executable file not
// found in $PATH` while the venv was installed, so nothing here depends on the
// PATH the container was left with.
var pythonPrograms = []string{"python3", "python", "copier", "ansible-playbook", "ansible-lint", "ansible-galaxy"}

// programPath is the file a program runs from: the venv's own for the venv's
// programs when it is there, and the name left to the PATH otherwise.
func programPath(name string) string {
	if !slices.Contains(pythonPrograms, name) {
		return name
	}
	p := filepath.Join(pythonBinDir, name)
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return p
	}
	return name
}

// programEnv is the environment a program runs in: the process's, with the
// venv's bin first on PATH when the venv is there (so ansible and copier find
// `python3` the way they were installed to), and the Cmd's own on top.
func programEnv(extra []string) []string {
	env := os.Environ()
	if fi, err := os.Stat(pythonBinDir); err == nil && fi.IsDir() {
		env = append(env, "PATH="+pythonBinDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	return append(env, extra...)
}

// RunProgram is the real Exec.
func RunProgram(ctx context.Context, c Cmd) (string, int) {
	cmd := exec.CommandContext(ctx, programPath(c.Name), c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = programEnv(c.Env)
	if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		ee := new(exec.ExitError)
		if !errors.As(err, &ee) {
			return fmt.Sprintf("%s: %v", c.Name, err), -1
		}
		code = ee.ExitCode()
	}
	if c.StdoutOnly {
		return stdout.String(), code
	}
	if c.Both {
		return stdout.String() + stderr.String(), code
	}
	out := stdout.String()
	if code != 0 {
		out += stderr.String()
	}
	return strings.TrimSpace(out), code
}

// run is Exec with the input's seam: a test says what a program answered.
func (in Input) run(ctx context.Context, c Cmd) (string, int) {
	if in.Exec != nil {
		return in.Exec(ctx, c)
	}
	return RunProgram(ctx, c)
}

// door is the git door the input reads from.
func (in Input) door() checks.Door {
	if in.Door.Base == "" {
		return checks.NewDoor()
	}
	return in.Door
}
