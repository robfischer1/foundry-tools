package atoms

import (
	"context"
	"slices"
)

// The rest of the surface the atoms in their own packages read (the tree, the
// merge base, Place and TempPath are in kit.go).

// Run runs a program through the input's seam: Input.Exec when a test set one,
// the real thing (RunProgram) otherwise.
func (in Input) Run(ctx context.Context, c Cmd) (string, int) { return in.run(ctx, c) }

// Git runs git in root, answering its output and exit code (-1 when it would not
// start).
func Git(ctx context.Context, root string, args ...string) (string, int) {
	return git(ctx, root, args...)
}

// PythonEnv is the environment a python checker runs in: bytecode off, so the
// tree under check is left as it was found.
func PythonEnv() []string { return slices.Clone(pythonEnv) }
