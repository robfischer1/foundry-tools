// Package releasekit is the release build's one implementation: what the four
// release atoms (go:release, python:release, rust:release, ts:release) and the
// Dagger build lane's Release() both need to know before and after a compile.
//
// The compile itself is run twice over, once as the atom's os/exec argv and once
// as the engine's WithExec chain, and that is the part that cannot be shared.
// Everything that DECIDES is here: whose image this is, whether it asks for a
// release build, what the build produces, which argv builds it, what its exit
// means and what the verdict says. A runner reads the tree through a Tree and
// runs the steps; it never re-derives any of that.
package releasekit

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"

	"dagger/foundry-tools/internal/atoms"
)

// Tree is what a release build reads of the repository: the engine's directory
// on the Dagger path, the checkout on disk in a pod. Each method keeps the
// failure the engine reported, because the caller words it ("could not be read")
// and a read fault is never an absence.
type Tree interface {
	// Population is the gate's files matching the patterns (all when none).
	Population(ctx context.Context, patterns ...string) ([]string, error)
	// File is a file's body; an error is a file that could not be read.
	File(ctx context.Context, rel string) (string, error)
	// Present is a regular file's body, false for one that is absent, is a
	// directory or would not read.
	Present(ctx context.Context, rel string) (string, bool)
	// Entries lists the repository root; a directory's name ends in a slash.
	Entries(ctx context.Context) ([]string, error)
	// IsDir says whether rel is a directory.
	IsDir(ctx context.Context, rel string) (bool, error)
	// RecordPaths lists the fleet's records (fleet/repos/*.json in foundry-dies).
	RecordPaths(ctx context.Context) ([]string, error)
	// Record reads one of them.
	Record(ctx context.Context, rel string) (string, error)
}

// Disk is a Tree over a checkout in a pod: the root, the gate's population as
// the executor's tree@1 carries it, and a checkout of foundry-dies (ATOM_DIES).
type Disk struct {
	Root     string
	Files    []string
	FilesErr error
	Dies     string
}

// DiskOf is the Tree an atom reads: the input's checkout, its population and
// its foundry-dies.
func DiskOf(in atoms.Input) Disk {
	return Disk{Root: in.Root, Files: in.Files, FilesErr: in.FilesErr, Dies: in.Dies}
}

// Population matches the patterns against the files, as a Dagger glob does.
func (d Disk) Population(_ context.Context, patterns ...string) ([]string, error) {
	if d.FilesErr != nil {
		return nil, d.FilesErr
	}
	if len(patterns) == 0 {
		return slices.Clone(d.Files), nil
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range patterns {
		for _, f := range atoms.Matching(d.Files, p) {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	slices.Sort(out)
	return out, nil
}

// File reads rel under the root.
func (d Disk) File(_ context.Context, rel string) (string, error) {
	b, err := os.ReadFile(filepath.Join(d.Root, rel))
	return string(b), err
}

// Present reads rel when it is a regular file.
func (d Disk) Present(_ context.Context, rel string) (string, bool) {
	return atoms.ReadIfPresent(d.Root, rel)
}

// Entries lists the root.
func (d Disk) Entries(_ context.Context) ([]string, error) { return atoms.RootEntries(d.Root) }

// IsDir says whether rel is a directory under the root.
func (d Disk) IsDir(_ context.Context, rel string) (bool, error) {
	fi, err := os.Stat(filepath.Join(d.Root, rel))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return fi.IsDir(), nil
}

// RecordPaths lists the records of the foundry-dies checkout. No checkout is an
// error: a name that cannot be looked up is unknown, never absent.
func (d Disk) RecordPaths(_ context.Context) ([]string, error) {
	if d.Dies == "" {
		return nil, errors.New("no foundry-dies checkout was given")
	}
	// the pattern is constant, and Glob fails only on a malformed one
	paths, _ := filepath.Glob(filepath.Join(d.Dies, "fleet", "repos", "*.json"))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		rel, _ := filepath.Rel(d.Dies, p) // p is under the checkout by construction
		out = append(out, filepath.ToSlash(rel))
	}
	slices.Sort(out)
	return out, nil
}

// Record reads one record of the checkout.
func (d Disk) Record(_ context.Context, rel string) (string, error) {
	b, err := os.ReadFile(filepath.Join(d.Dies, path.Clean(rel)))
	return string(b), err
}
