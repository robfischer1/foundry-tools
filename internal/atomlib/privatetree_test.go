package atomlib

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
)

func TestPrivateCopy(t *testing.T) {
	root := t.TempDir()
	atomtest.Put(t, root, "a.yml", "a")
	atomtest.Put(t, root, "d/b.yml", "b")
	dir := filepath.Join(t.TempDir(), "copy")
	if err := PrivateCopy(dir, root, []string{"a.yml", "d/b.yml"}, []string{"./x.env", "../up.env", "s/y.env"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(atomtest.ListTree(t, dir), ","); got != "a.yml,d/b.yml,s/y.env,up.env,x.env" {
		t.Errorf("the copy holds %s", got)
	}
	if body, _ := os.ReadFile(filepath.Join(dir, "d/b.yml")); string(body) != "b" {
		t.Errorf("a copied file lost its bytes: %q", body)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "up.env")); err == nil {
		t.Errorf("a stub escaped the copy")
	}
	t.Run("a link is copied as a link, a mode is kept and a stub is laid over a file", func(t *testing.T) {
		src := t.TempDir()
		atomtest.Put(t, src, "run.sh", "#!/bin/sh\n")
		atomtest.Put(t, src, "kept.txt", "original\n")
		if err := os.Chmod(filepath.Join(src, "run.sh"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("run.sh", filepath.Join(src, "alias.sh")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("nowhere", filepath.Join(src, "dangling")); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(t.TempDir(), "copy")
		if err := PrivateCopy(dst, src, []string{"alias.sh", "dangling", "run.sh", "kept.txt"}, []string{"kept.txt"}); err != nil {
			t.Fatal(err)
		}
		if target, err := os.Readlink(filepath.Join(dst, "alias.sh")); err != nil || target != "run.sh" {
			t.Errorf("the link: %q, %v", target, err)
		}
		if target, err := os.Readlink(filepath.Join(dst, "dangling")); err != nil || target != "nowhere" {
			t.Errorf("a link to nowhere is a link: %q, %v", target, err)
		}
		if fi, err := os.Stat(filepath.Join(dst, "run.sh")); err != nil || fi.Mode().Perm() != 0o755 {
			t.Errorf("the script's mode: %v, %v", fi, err)
		}
		if body, _ := os.ReadFile(filepath.Join(dst, "kept.txt")); len(body) != 0 {
			t.Errorf("the stub did not replace the file: %q", body)
		}
		if body, _ := os.ReadFile(filepath.Join(src, "kept.txt")); string(body) != "original\n" {
			t.Errorf("the stub reached the source: %q", body)
		}
	})
	t.Run("a link that cannot be made, and a path that cannot be read, are errors", func(t *testing.T) {
		src := t.TempDir()
		if err := os.Symlink("x", filepath.Join(src, "l")); err != nil {
			t.Fatal(err)
		}
		blocker := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocker, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := PrivateCopy(filepath.Join(blocker, "copy"), src, []string{"l"}, nil); err == nil {
			t.Error("a link into a place that is not a directory was made")
		}
		atomtest.Put(t, src, "dir/f", "x")
		if err := PrivateCopy(filepath.Join(t.TempDir(), "copy"), src, []string{"dir"}, nil); err == nil {
			t.Error("a directory was copied as a file")
		}
	})
	t.Run("a tracked file that is not there is an error", func(t *testing.T) {
		err := PrivateCopy(filepath.Join(t.TempDir(), "copy"), root, []string{"gone.yml"}, nil)
		if err == nil || !strings.Contains(err.Error(), "gone.yml") {
			t.Errorf("a missing tracked file was copied: %v", err)
		}
	})
	// A directory that sits under a file can never be made, so every write fails.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	under := filepath.Join(blocker, "copy")
	t.Run("a copy that cannot be written is an error", func(t *testing.T) {
		if err := PrivateCopy(under, root, []string{"a.yml"}, nil); err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("a copy into a place that is not a directory: %v", err)
		}
	})
	t.Run("a stub that cannot be written is an error", func(t *testing.T) {
		if err := PrivateCopy(under, root, nil, []string{"x.env"}); err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("a stub into a place that is not a directory: %v", err)
		}
	})
}

// privateTree is every committable file in a directory of the run's own, made a
// repository off the developer's configuration, and removed by what it answers.
func TestPrivateTree(t *testing.T) {
	files := map[string]string{"a.yml": "a", "d/b.yml": "b"}
	t.Run("the files, a repository and a cleanup", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("TMPDIR", tmp)
		in, f := atomtest.ToolTree(t, files, nil)
		dir, cleanup, err := PrivateTree(context.Background(), in, "x")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(atomtest.ListTree(t, dir), ","); got != "a.yml,d/b.yml" {
			t.Errorf("the private tree holds %s", got)
		}
		if filepath.Dir(dir) != tmp || strings.HasPrefix(dir, in.Root) {
			t.Errorf("the tree is at %s, want a directory of its own under %s", dir, tmp)
		}
		if got := strings.Join(f.Ran(), " "); got != "git git" {
			t.Errorf("ran %s", got)
		}
		for i, want := range []string{"init -q .", "add -A"} {
			c := f.Calls[i]
			if atomtest.Flagged(c) != want || c.Dir != dir || !slices.Contains(c.Env, "GIT_CONFIG_NOSYSTEM=1") || !slices.Contains(c.Env, "GIT_CONFIG_GLOBAL="+os.DevNull) {
				t.Errorf("git call %d: %+v, want %q in %s off the developer's configuration", i, c, want, dir)
			}
		}
		cleanup()
		if left, _ := os.ReadDir(tmp); len(left) != 0 {
			t.Errorf("cleanup left %v", left)
		}
	})
	for _, tc := range []struct {
		name  string
		setup func(in *atoms.Input)
		git   func(c atoms.Cmd) (string, int)
		want  string
		calls int
	}{
		{"a file that cannot be copied", func(in *atoms.Input) { in.Committable = append(in.Committable, "ghost.yml") }, nil, "ghost.yml", 0},
		{"a repository that cannot be started", nil, func(atoms.Cmd) (string, int) { return "fatal: no", 128 }, "git init -q . exited 128: fatal: no", 1},
		{"an index that cannot be made", nil, func(c atoms.Cmd) (string, int) {
			if c.Args[0] == "add" {
				return "fatal: bad", 1
			}
			return "", 0
		}, "git add -A exited 1: fatal: bad", 2},
	} {
		t.Run(tc.name+" leaves nothing behind", func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			in, f := atomtest.ToolTree(t, files, tc.git)
			if tc.setup != nil {
				tc.setup(&in)
			}
			dir, cleanup, err := PrivateTree(context.Background(), in, "x")
			if err == nil || !strings.Contains(err.Error(), tc.want) || dir != "" || cleanup != nil {
				t.Fatalf("got %q, %v, %v; want an error containing %q", dir, cleanup != nil, err, tc.want)
			}
			if len(f.Calls) != tc.calls {
				t.Errorf("git ran %d times, want %d", len(f.Calls), tc.calls)
			}
			if left, _ := os.ReadDir(tmp); len(left) != 0 {
				t.Errorf("the failed tree was left behind: %v", left)
			}
		})
	}
}
