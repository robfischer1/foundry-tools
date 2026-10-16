package atomlib

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/atoms"
)

// PrivateCopy lays the named tracked files of a tree, and an empty file for each
// stub, into dir, which the caller made the name of and removes. It is how an
// atom that has to WRITE beside what it reads (compose:config stubs the env_file
// targets that are correctly not in the repository) leaves the shared tree
// alone: in the chain the stubs went into the engine's copy of the tree, and
// the atom runs on the tree itself, where a stub would be a file the
// developer's next `git status` shows.
//
// THE STUBS GO IN AFTER THE FILES, so a stub is written over whatever the copy
// already holds at its path. A symlink is copied as a symlink (the chain's copy
// of the tree kept them, and a link that points nowhere is not an error in a
// tree that never follows it), and a file keeps its permission bits.
//
// A STUB'S PATH IS CLEANED AS IF ROOTED, so `../x.env` lands inside the copy as
// `x.env` and never beside it.
func PrivateCopy(dir, root string, tracked, stubs []string) error {
	for _, f := range tracked {
		if err := copyTracked(filepath.Join(root, f), filepath.Join(dir, f)); err != nil {
			return err
		}
	}
	for _, s := range stubs {
		if err := atoms.Place(filepath.Join(dir, strings.TrimPrefix(path.Clean("/"+s), "/")), nil); err != nil {
			return err
		}
	}
	return nil
}

// copyTracked copies one path of the tree to dst: a link as a link, a file with
// its bytes and its permission bits.
func copyTracked(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	// The directory is made first and its error is not branched on: it fails the
	// write below with the same cause, and one test reaches that.
	_ = os.MkdirAll(filepath.Dir(dst), 0o755)
	if fi.Mode()&os.ModeSymlink != 0 {
		// A link that vanished since the Lstat reads as an empty target, which
		// os.Symlink refuses with the same error a failed read would have been.
		target, _ := os.Readlink(src)
		return os.Symlink(target, dst)
	}
	body, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, body, fi.Mode().Perm())
}

// PrivateTree lays every file the repository would commit into a directory of
// this run's own and makes it a repository (init, then add everything),
// answering the directory and the call that removes it. It is the chain's tree
// for an atom that runs a recipe or a tool which may WRITE (a cache, a lock, a
// rendered file) or which reads the index (`git ls-files`): the chain ran those
// in the engine's copy of the tree, and the atom must not run them in the tree
// its siblings are reading at the same moment. Git ignores the developer's own
// configuration, so a global ignore file cannot change what the index holds.
func PrivateTree(ctx context.Context, in atoms.Input, kind string) (string, func(), error) {
	dir := atoms.TempPath(kind, "")
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := PrivateCopy(dir, in.Root, slices.Compact(slices.Sorted(slices.Values(in.Committable))), nil); err != nil {
		cleanup()
		return "", nil, err
	}
	env := []string{"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"}
	for _, args := range [][]string{{"init", "-q", "."}, {"add", "-A"}} {
		if out, code := in.Run(ctx, atoms.Cmd{Dir: dir, Name: "git", Args: args, Env: env, Both: true}); code != 0 {
			cleanup()
			return "", nil, fmt.Errorf("git %s exited %d: %s", strings.Join(args, " "), code, strings.TrimSpace(out))
		}
	}
	return dir, cleanup, nil
}
