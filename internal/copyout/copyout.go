// Package copyout runs one build and carries the files it produced out of a
// cache volume into the container's own filesystem, IN THE SAME EXEC.
//
// WHY ONE EXEC. A release build's target directory is a cache volume
// (checks.ReleaseCacheFor) so its dependencies stay compiled between runs, and
// a file in a cache mount is not in the container: it cannot be read back as
// an output. It has to be copied out, and the copy cannot be an exec of its
// own. The engine caches an exec by its inputs, so a later copy exec could run
// against a build exec the engine answered from cache — reading whatever the
// volume holds NOW, which another tree may have written since. Building and
// copying in one process makes the copy part of the result the engine caches.
//
// NOT A SHELL. `cargo build … && cp …` would be one `sh -c`, which runtime.go
// rule 7 refuses; this is the same two steps as a typed argv.
//
// A STALE OUTPUT IS NEVER CARRIED. Each source is removed BEFORE the build, so
// a file the build did not produce this time is absent rather than the copy a
// previous tree left in the volume. An absent source is skipped, not an error:
// the caller reads the destination lazily, and its own "no such file" is the
// answer it gave before the target moved into a volume.
package copyout

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// CopyFailed is the exit code for a build that succeeded and an output that
// would not copy: 126, the shell's "could not be run", so no caller reads it as
// the build's own verdict.
const CopyFailed = 126

// Pair is one output: Src in the cache, Dst in the container.
type Pair struct{ Src, Dst string }

// Parse splits copyout's arguments: SRC=DST pairs, then "--", then the build.
func Parse(args []string) ([]Pair, []string, error) {
	var pairs []Pair
	for i, a := range args {
		if a == "--" {
			if len(args[i+1:]) == 0 {
				return nil, nil, errors.New("no command after --")
			}
			return pairs, args[i+1:], nil
		}
		src, dst, ok := strings.Cut(a, "=")
		if !ok || src == "" || dst == "" {
			return nil, nil, fmt.Errorf("%q is not SRC=DST", a)
		}
		pairs = append(pairs, Pair{src, dst})
	}
	return nil, nil, errors.New("no -- before the command")
}

// Main is copyout's process.
func Main(args []string, stdout, stderr io.Writer) int {
	pairs, cmd, err := Parse(args)
	if err != nil {
		fmt.Fprintf(stderr, "copyout: %v\nusage: copyout SRC=DST... -- <command> [args...]\n", err)
		return 2
	}
	return Run(pairs, cmd, stdout, stderr)
}

// Run clears every source, runs cmd with the process's own streams, and on a
// zero exit copies each source that exists to its destination. It answers the
// command's own exit code (128 plus a signal, 127 when it did not start), or
// CopyFailed when an output would not copy.
func Run(pairs []Pair, cmd []string, stdout, stderr io.Writer) int {
	for _, p := range pairs {
		if err := os.Remove(p.Src); err != nil && !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(stderr, "copyout: could not clear %s before the build: %v\n", p.Src, err)
			return CopyFailed
		}
	}
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, stdout, stderr
	if err := c.Start(); err != nil {
		fmt.Fprintf(stderr, "copyout: could not start %s: %v\n", cmd[0], err)
		return 127
	}
	if code := exitCode(c.Wait()); code != 0 {
		return code
	}
	for _, p := range pairs {
		if err := copyFile(p.Src, p.Dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(stderr, "copyout: could not copy %s to %s: %v\n", p.Src, p.Dst, err)
			return CopyFailed
		}
	}
	return 0
}

// copyFile copies src to dst with src's mode, making dst's directory.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// exitCode is a waited command's code as a shell would report it.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return 1
	}
	if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return exit.ExitCode()
}
