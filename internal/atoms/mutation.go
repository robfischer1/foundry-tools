package atoms

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// WHAT THE FOUR MUTATION ATOMS SHARE OF THEIR ENVIRONMENT. Where they put what
// they write, and the two facts the run hands the atom that no tree fact carries:
// the commit (ATOM_SHA) and the credential the grading lookup is asked under
// (ATOM_MUTANTS_TOKEN). Both runners set the same variables: the Dagger adapter
// from the call's --record-token and the commit it fetched, the executor's atom
// environment from the lane's record token and the run plan.

const (
	// EnvSha is the commit under grade.
	EnvSha = "ATOM_SHA"
	// EnvMutantsToken is the run's record token, which the door's /ci/mutants
	// believes for a mutation lane. Set only for a lane that reuses gradings.
	EnvMutantsToken = "ATOM_MUTANTS_TOKEN"
)

// TakeToken reads the grading-lookup credential and removes it from the process
// environment, so no program the atom starts after this inherits it. The tree
// under grade is code the pull wrote, and its tests run as this atom's children:
// a credential in their environment is a credential the pull can read, and the
// record token authorises a lookup and a record post for the run.
func TakeToken() string {
	t := os.Getenv(EnvMutantsToken)
	_ = os.Unsetenv(EnvMutantsToken)
	return t
}

// GitConfig is the environment that adds key=value pairs to git's configuration
// for the programs the atom starts, after whatever GIT_CONFIG_COUNT already
// carries (the executor sets safe.directory that way), so neither is lost.
func GitConfig(pairs ...string) []string {
	n, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT")) // unset or garbage is none
	env := make([]string, 0, 2*len(pairs)+1)
	for i, kv := range pairs {
		k, v, _ := strings.Cut(kv, "=")
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", n+i, k), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", n+i, v))
	}
	return append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(n+len(pairs)))
}

// WorkingTree answers the directory a mutation atom may build and write in, and
// whether it had to make it. The Dagger runner mounts the tree writable and the
// atom works in it, as the chains did. The executor mounts it read-only (its
// files are locked: no atom edits the evidence), and a tool that copies the tree
// and then mutates the copy (cargo-mutants) copies the modes with it and cannot
// write what it mutated. So a locked tree is copied, without its .git, into
// <work>/src with the owner's write bit set; git questions are still asked of
// the original, which keeps its history.
func WorkingTree(root, work string) (dir string, copied bool, err error) {
	if !locked(root) {
		return root, false, nil
	}
	dst := filepath.Join(work, "src")
	if err := os.RemoveAll(dst); err != nil {
		return "", false, err
	}
	return dst, true, CopyTree(root, dst)
}

// locked says the atom cannot write the tree: its first regular file at the top
// (a manifest, in a tree worth grading) lacks the owner's write bit, or is
// another uid's and not writable to the caller through its group or other bits.
// The executor lays the tree in git's modes (0644) as the fetching uid's, so the
// owner bit is set and the atom still cannot write it, nor rename over it in a
// sticky directory (Stryker's backup restore died EPERM, DEF-B4-059).
func locked(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
			uid, gid := -1, -1 // unknown where the platform does not say: not locked on that account
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				uid, gid = int(st.Uid), int(st.Gid)
			}
			groups, _ := os.Getgroups()
			return lockedTo(info.Mode().Perm(), uid, gid, os.Geteuid(), append(groups, os.Getegid()))
		}
	}
	return false
}

// lockedTo says a file of this mode, owner and group is not writable by the
// caller (euid me, in groups). Root writes anything but a file with no owner bit
// at all, which is how the old locked tree read (0444).
func lockedTo(mode fs.FileMode, uid, gid, me int, groups []int) bool {
	switch {
	case mode&0o200 == 0:
		return true
	case me == 0 || uid == me || uid < 0:
		return false
	case slices.Contains(groups, gid):
		return mode&0o020 == 0
	}
	return mode&0o002 == 0
}

// CopyTree copies src to dst without its .git, with the owner's write bit set on
// every file and directory: a copy to build and mutate in, not to keep. A
// directory named in skip is left out wherever it sits (a virtualenv's absolute
// paths name the tree it was made in, so its copy would import the original).
func CopyTree(src, dst string, skip ...string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || slices.Contains(skip, d.Name())) {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(src, p) // p is under src, so Rel cannot fail
		to := filepath.Join(dst, rel)
		perm := fs.FileMode(0o644)
		if info, err := d.Info(); err == nil {
			perm = info.Mode().Perm()
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(to, perm|0o700)
		case d.Type()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(p) // a link that will not read is made empty, and Symlink refuses it
			return os.Symlink(target, to)
		}
		body, rerr := os.ReadFile(p)
		return errors.Join(rerr, os.WriteFile(to, body, perm|0o200))
	})
}
