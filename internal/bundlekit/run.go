package bundlekit

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/checks"
)

// Run is one atom's view of the tree and the programs it runs.
type Run struct {
	In  atomkit.In
	Row checks.AtomDef
}

// Exec runs a program and answers what it printed and its code; a program that
// never ran is the lane's fault (bundle.go exec: "<step> never ran").
func (r Run) Exec(ctx context.Context, step, dir, stdin, name string, args ...string) (string, int, *Fault) {
	out, code := r.In.Runner()(ctx, atoms.Cmd{Dir: dir, Name: name, Args: args, Stdin: stdin})
	if code < 0 {
		return "", 0, CouldNotRun("%s never ran: %s", step, out)
	}
	return out, code, nil
}

// Sha is the commit the lane builds: ATOM_SHA, which the pod gives every atom,
// else the checkout's HEAD.
func (r Run) Sha(ctx context.Context) (string, *Fault) {
	if sha := os.Getenv("ATOM_SHA"); sha != "" {
		return sha, nil
	}
	out, code, bad := r.Exec(ctx, "git rev-parse HEAD", r.In.Root, "", "git", "-c", "safe.directory=*", "rev-parse", "HEAD")
	if bad != nil {
		return "", bad
	}
	if code != 0 || strings.TrimSpace(out) == "" {
		return "", CouldNotRun("the commit could not be read (exit %d): %s", code, out)
	}
	return strings.TrimSpace(out), nil
}

// Changed answers the paths this landing touched since the previous tip, or nil
// when the previous tip cannot be read — and cannot-tell publishes both dies
// (bundle.go changed). On a merge, <sha>^1 is main before the merge.
func (r Run) Changed(ctx context.Context, sha string) ([]string, *Fault) {
	git := func(step string, args ...string) (string, int, *Fault) {
		return r.Exec(ctx, step, r.In.Root, "", "git", append([]string{"-c", "safe.directory=*"}, args...)...)
	}
	_, code, bad := git("git rev-parse "+sha+"^1", "rev-parse", "--verify", "-q", sha+"^1")
	if bad != nil {
		return nil, CouldNotRun("the history could not be read: %s", bad.Msg)
	}
	if code != 0 {
		return nil, nil
	}
	out, code, bad := git("git diff", "diff", "--name-only", sha+"^1", sha)
	if bad != nil || code != 0 {
		msg := out
		if bad != nil {
			msg = bad.Msg
		}
		return nil, CouldNotRun("the change set could not be read (exit %d): %s", code, msg)
	}
	return bundlelane.Changed(out), nil
}

// Members lists a bundle tarball's member names, as `tar tzf` prints them.
func Members(tgz []byte) ([]string, error) {
	var names []string
	err := walk(tgz, func(h *tar.Header, _ io.Reader) bool {
		names = append(names, h.Name)
		return true
	})
	return names, err
}

// Member reads one file out of a bundle tarball; name is matched with or
// without OPA's leading slash.
func Member(tgz []byte, name string) (string, bool, error) {
	var body []byte
	var found bool
	var rerr error
	err := walk(tgz, func(h *tar.Header, r io.Reader) bool {
		if strings.TrimPrefix(h.Name, "/") != strings.TrimPrefix(name, "/") {
			return true
		}
		found = true
		body, rerr = io.ReadAll(r)
		return false
	})
	if err == nil {
		err = rerr
	}
	return string(body), found, err
}

// walk calls fn on each member until it answers false.
func walk(tgz []byte, fn func(*tar.Header, io.Reader) bool) error {
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return fmt.Errorf("not gzip: %w", err)
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("not a tar: %w", err)
		}
		if !fn(h, tr) {
			return nil
		}
	}
}

// ReadFile reads a built file, as the lane's fault when it cannot.
func ReadFile(path string) ([]byte, *Fault) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, CouldNotRun("%s could not be read: %v", path, err)
	}
	return b, nil
}
