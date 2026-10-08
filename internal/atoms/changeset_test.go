package atoms

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// Each case builds a repository in the test and names the change set it must
// answer. The first is the one that matters: main moved under the pull.
func TestChangeSet(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		build func(t *testing.T, dir string) (base string)
		want  []string
	}{
		{
			// MEASURED urania #63: a two-tree diff against main's tip reads
			// main's own landing (a.txt v2) as this pull's change.
			name: "with a base, the change is measured from the merge base, not from the base's tip",
			build: func(t *testing.T, dir string) string {
				put(t, dir, "a.txt", "v1\n")
				put(t, dir, "d.txt", "d\n")
				commitAll(t, dir, "c1")
				gitIn(t, dir, "checkout", "-q", "-b", "feat")
				put(t, dir, "b.txt", "b\n")
				commitAll(t, dir, "feat")
				gitIn(t, dir, "checkout", "-q", "main")
				put(t, dir, "a.txt", "v2\n")
				tip := commitAll(t, dir, "main moves")
				gitIn(t, dir, "checkout", "-q", "feat")
				return tip
			},
			want: []string{"b.txt"},
		},
		{
			name: "with a base, a modified and a renamed path are the change and a deleted one is not",
			build: func(t *testing.T, dir string) string {
				put(t, dir, "keep.txt", "keep\n")
				put(t, dir, "gone.txt", "gone\n")
				put(t, dir, "old.txt", "a long enough body that git sees the rename\nline two\nline three\n")
				base := commitAll(t, dir, "c1")
				put(t, dir, "keep.txt", "changed\n")
				gitIn(t, dir, "rm", "-q", "gone.txt")
				gitIn(t, dir, "mv", "old.txt", "new.txt")
				commitAll(t, dir, "c2")
				return base
			},
			want: []string{"keep.txt", "new.txt"},
		},
		{
			name: "no base reads the tip against its parent",
			build: func(t *testing.T, dir string) string {
				put(t, dir, "a.txt", "1\n")
				commitAll(t, dir, "c1")
				put(t, dir, "b.txt", "2\n")
				commitAll(t, dir, "c2")
				put(t, dir, "c.txt", "3\n")
				gitIn(t, dir, "rm", "-q", "a.txt")
				commitAll(t, dir, "c3")
				return ""
			},
			want: []string{"c.txt"},
		},
		{
			name: "a root commit has no parent: every file it carries is the change",
			build: func(t *testing.T, dir string) string {
				put(t, dir, "a.txt", "1\n")
				put(t, dir, "dir/b c.txt", "2\n")
				commitAll(t, dir, "root")
				return ""
			},
			want: []string{"a.txt", "dir/b c.txt"},
		},
		{
			// git config --get exits 0 for a key set to "": that is no marker.
			name: "an empty ca.snapshot is not a snapshot",
			build: func(t *testing.T, dir string) string {
				put(t, dir, "a.txt", "1\n")
				commitAll(t, dir, "c1")
				gitIn(t, dir, "config", "--local", "ca.snapshot", "")
				return ""
			},
			want: []string{"a.txt"},
		},
		{
			name: "a base that is HEAD itself is an empty change",
			build: func(t *testing.T, dir string) string {
				put(t, dir, "a.txt", "1\n")
				return commitAll(t, dir, "c1")
			},
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := newRepo(t)
			base := tc.build(t, dir)
			got, err := ChangeSet(ctx, dir, base)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("change set %q, want %q", got, tc.want)
			}
		})
	}
}

func TestChangeSetErrors(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		build   func(t *testing.T, dir string) (base string)
		wantErr string
	}{
		{
			name: "a base the history does not reach is an error, not an empty change",
			build: func(t *testing.T, dir string) string {
				put(t, dir, "a.txt", "1\n")
				commitAll(t, dir, "c1")
				return strings.Repeat("0", 40)
			},
			wantErr: "is not in this history",
		},
		{
			name: "unrelated histories have no merge base",
			build: func(t *testing.T, dir string) string {
				put(t, dir, "a.txt", "1\n")
				tip := commitAll(t, dir, "c1")
				gitIn(t, dir, "checkout", "-q", "--orphan", "other")
				put(t, dir, "z.txt", "z\n")
				commitAll(t, dir, "orphan")
				return tip
			},
			wantErr: "no merge base",
		},
		{
			name: "a linked-worktree snapshot has no change set, even with commits",
			build: func(t *testing.T, dir string) string {
				put(t, dir, "a.txt", "1\n")
				commitAll(t, dir, "synthetic")
				gitIn(t, dir, "config", "--local", "ca.snapshot", "linked-worktree")
				return ""
			},
			wantErr: "linked-worktree snapshot",
		},
		{
			name: "a repository with no commits has no change set",
			build: func(t *testing.T, dir string) string {
				return ""
			},
			wantErr: "git exited",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := newRepo(t)
			base := tc.build(t, dir)
			got, err := ChangeSet(ctx, dir, base)
			if err == nil {
				t.Fatalf("want an error, got the change set %q", got)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
			if got != nil {
				t.Errorf("an error carries no paths, got %q", got)
			}
		})
	}
}

// git that will not start is the error, with the verb that failed.
func TestChangeSetWithoutGit(t *testing.T) {
	dir := newRepo(t)
	t.Setenv("PATH", t.TempDir())
	for _, base := range []string{"", "abc123"} {
		want := "git exited -1 reading the change set: git show: "
		if base != "" {
			want = "git would not run: git rev-parse: "
		}
		_, err := ChangeSet(context.Background(), dir, base)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("base %q: error %v, want one containing %q", base, err, want)
		}
	}
}

func TestCollect(t *testing.T) {
	dir := newRepo(t)
	put(t, dir, ".gitignore", "ignored.txt\nforced.txt\n")
	put(t, dir, "tracked.txt", "t\n")
	put(t, dir, "forced.txt", "tracked and ignored\n")
	put(t, dir, "deleted.txt", "tracked, then removed from disk\n")
	put(t, dir, ".claude/x.txt", "excluded by the fleet\n")
	put(t, dir, "vendor/v.txt", "excluded by the fleet\n")
	gitIn(t, dir, "add", "-f", "forced.txt")
	commitAll(t, dir, "c1")
	if err := os.Remove(filepath.Join(dir, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	put(t, dir, "untracked.txt", "u\n")
	put(t, dir, "ignored.txt", "i\n")
	gitIn(t, dir, "remote", "add", "origin", "http://door/cerberus.git")
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)

	in := Collect(context.Background(), dir, "", "", now)
	if in.FilesErr != nil || in.TrackedErr != nil || in.ChangedErr != nil {
		t.Fatalf("errors: %v / %v / %v", in.FilesErr, in.TrackedErr, in.ChangedErr)
	}
	if want := []string{".claude/x.txt", ".gitignore", "deleted.txt", "forced.txt", "tracked.txt", "vendor/v.txt"}; !reflect.DeepEqual(in.Tracked, want) {
		t.Errorf("tracked %q, want %q (git's list: force-added and deleted files stay, fleet exclude not applied)", in.Tracked, want)
	}
	if want := []string{".gitignore", "tracked.txt", "untracked.txt"}; !reflect.DeepEqual(in.Files, want) {
		t.Errorf("population %q, want %q (committable, less ignored, fleet-excluded, force-added-but-ignored and deleted: the chains' engine filter sees none of those)", in.Files, want)
	}
	if want := []string{".claude/x.txt", ".gitignore", "tracked.txt", "untracked.txt", "vendor/v.txt"}; !reflect.DeepEqual(in.Committable, want) {
		t.Errorf("committable %q, want %q (the same population with the fleet exclude NOT applied: the compose atoms and ops:yaml read the raw tree)", in.Committable, want)
	}
	if want := []string{".claude/x.txt", ".gitignore", "deleted.txt", "forced.txt", "tracked.txt", "vendor/v.txt"}; !reflect.DeepEqual(in.Changed, want) {
		t.Errorf("changed %q, want %q (a root commit)", in.Changed, want)
	}
	if in.Origin != "http://door/cerberus.git" {
		t.Errorf("origin %q is not the remote's", in.Origin)
	}
	if in.Root != dir || !in.Now.Equal(now) {
		t.Errorf("root %q now %v", in.Root, in.Now)
	}

	if got := Collect(context.Background(), dir, "", "http://named/other.git", now).Origin; got != "http://named/other.git" {
		t.Errorf("an origin the caller names is not overridden by the remote: %q", got)
	}
}

func TestCollectWithoutAnOrigin(t *testing.T) {
	dir := newRepo(t)
	put(t, dir, "a.txt", "a\n")
	commitAll(t, dir, "c1")
	if got := Collect(context.Background(), dir, "", "", time.Now()).Origin; got != "" {
		t.Errorf("no remote names no origin, got %q", got)
	}
}

// A directory that is not a repository fails every git question, and each
// failure rides in its own field for the atom that needed it.
func TestCollectOutsideARepository(t *testing.T) {
	in := Collect(context.Background(), t.TempDir(), "", "", time.Now())
	if in.TrackedErr == nil || in.FilesErr == nil || in.ChangedErr == nil {
		t.Errorf("each question must fail: tracked %v, files %v, changed %v", in.TrackedErr, in.FilesErr, in.ChangedErr)
	}
	if len(in.Tracked) != 0 || len(in.Files) != 0 || in.Origin != "" {
		t.Errorf("a failed question carries no answer: %q %q %q", in.Tracked, in.Files, in.Origin)
	}
}

func TestPopulation(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "sub/keep.txt", "k\n")
	put(t, dir, "a.txt", "a\n")
	put(t, dir, "submodule/f", "gitlink stand-in\n")
	listed := []string{"sub/keep.txt", "a.txt", "a.txt", "submodule", ".furnace/x", "node_modules/y", "z.melt"}
	raw := committable(dir, listed)
	// The raw listing keeps what the fleet excludes: the compose atoms and
	// ops:yaml never applied the exclude, and Files is this list with it.
	if want := []string{".furnace/x", "a.txt", "node_modules/y", "sub/keep.txt", "z.melt"}; !reflect.DeepEqual(raw, want) {
		t.Errorf("committable %q, want %q (sorted, deduplicated, no directories, not fleet-excluded)", raw, want)
	}
	got := checks.GatePopulation(raw)
	if want := []string{"a.txt", "sub/keep.txt"}; !reflect.DeepEqual(got, want) {
		t.Errorf("population %q, want %q (fleet-excluded)", got, want)
	}
	if !reflect.DeepEqual(listed[:3], []string{"sub/keep.txt", "a.txt", "a.txt"}) {
		t.Errorf("the caller's list was reordered: %q", listed)
	}
}

// git answers the output AND the exit code of a command that ran and said no,
// and exit -1 with the reason for one that would not start.
func TestGit(t *testing.T) {
	dir := newRepo(t)
	out, code := git(context.Background(), dir, "rev-parse", "--verify", "--quiet", "nope")
	if code != 1 || out != "" {
		t.Errorf("a verify that finds nothing: %q, %d", out, code)
	}
	out, code = git(context.Background(), dir, "no-such-subcommand")
	if code != 1 || !strings.Contains(out, "not a git command") {
		t.Errorf("an unknown subcommand: %q, %d (stderr rides in the output)", out, code)
	}
	out, code = git(context.Background(), dir, "rev-parse", "--git-dir")
	if code != 0 || strings.TrimSpace(out) != ".git" {
		t.Errorf("a command that succeeds: %q, %d", out, code)
	}
	t.Setenv("PATH", t.TempDir())
	if out, code = git(context.Background(), dir, "status"); code != -1 || !strings.HasPrefix(out, "git status: ") {
		t.Errorf("a git that will not start: %q, %d", out, code)
	}
}
