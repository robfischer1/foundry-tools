package releasekit

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/buildlane"
)

// The trees the base-built releases copy into their build directory: the tree
// as it is, less what the build must not start from. Both lists are Dagger
// globs (the engine's filter on the Dagger path, CopyTree on a pod).
var (
	// TSExcludes leaves out a release/ a local `just image` left behind, and
	// node_modules, which the frozen install makes.
	TSExcludes = []string{buildlane.ReleaseDir, "**/node_modules"}
	// PythonExcludes leaves out a local .venv and release/ and the bytecode.
	PythonExcludes = []string{".venv", buildlane.ReleaseDir, "**/__pycache__"}
)

// excluded says whether rel, or a directory above it, matches an exclude glob.
func excluded(rel string, globs []string) bool {
	parts := strings.Split(rel, "/")
	for i := range parts {
		prefix := strings.Join(parts[:i+1], "/")
		for _, g := range globs {
			if len(atoms.Matching([]string{prefix}, g)) > 0 {
				return true
			}
		}
	}
	return false
}

// CopyTree copies the tree at src into dst without what the globs exclude (a
// match excludes everything beneath it), making dst. The release steps write
// into what they build from, so the build runs on a copy and the checkout is
// left as it was found.
func CopyTree(src, dst string, globs []string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// p is under src by construction, so it has a relative path
		rel, _ := filepath.Rel(src, p)
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if excluded(rel, globs) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		to := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(to, 0o755)
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err == nil {
				err = os.Symlink(target, to)
			}
			return err
		}
		return copyFile(p, to)
	})
}

// copyFile copies one regular file, keeping its mode.
func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	perm := os.FileMode(0o644)
	if fi, err := in.Stat(); err == nil { // the stat of a file just opened does not fail
		perm = fi.Mode().Perm()
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	_, err = io.Copy(out, in)
	return err
}

// Entries lists a directory's names, directories with a trailing slash, as the
// engine's Directory.Entries does; none when it is not there.
func Entries(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() {
			n += "/"
		}
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// Carry copies a built tree's directory into dst/<name> (a bun star's release/,
// a python star's venv), making what it needs; an error names what failed.
func Carry(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("could not clear %s: %w", dst, err)
	}
	return CopyTree(src, dst, nil)
}
