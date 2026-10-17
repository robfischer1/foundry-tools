package atoms

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestTakeTokenRemovesItFromTheEnvironment(t *testing.T) {
	t.Setenv(EnvMutantsToken, "tok")
	if got := TakeToken(); got != "tok" {
		t.Fatalf("got %q", got)
	}
	if v, ok := os.LookupEnv(EnvMutantsToken); ok {
		t.Errorf("the token is still set: %q", v)
	}
	if TakeToken() != "" {
		t.Error("a second take found one")
	}
}

func TestGitConfigAppendsAfterWhatIsThere(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "")
	got := GitConfig("a.b=c", "d.e=f")
	want := []string{"GIT_CONFIG_KEY_0=a.b", "GIT_CONFIG_VALUE_0=c", "GIT_CONFIG_KEY_1=d.e", "GIT_CONFIG_VALUE_1=f", "GIT_CONFIG_COUNT=2"}
	if !slices.Equal(got, want) {
		t.Errorf("%v", got)
	}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	got = GitConfig("a.b=c")
	if !slices.Equal(got, []string{"GIT_CONFIG_KEY_1=a.b", "GIT_CONFIG_VALUE_1=c", "GIT_CONFIG_COUNT=2"}) {
		t.Errorf("%v", got)
	}
}

func TestNeverRanReasonWordsAStartAndASignal(t *testing.T) {
	if got := NeverRanReason([]string{"x"}, -1, "x: not found"); got != "the atom never ran: x: not found" {
		t.Error(got)
	}
	if got := NeverRanReason([]string{"x", "-y"}, 137, "a\nb"); got != "the atom never ran: exit code: 137\nfailed: x -y\na\nb" {
		t.Error(got)
	}
}

func TestCopyTreeLeavesTheHistoryAndTheSkippedAndOpensTheModes(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "copy")
	for p, body := range map[string]string{"a.txt": "a", "d/b.txt": "b", ".git/HEAD": "h", ".venv/x": "v", "d/.venv/y": "w"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(src, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, p), []byte(body), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("a.txt", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	if err := CopyTree(src, dst, ".venv"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".git", ".venv", "d/.venv"} {
		if _, err := os.Stat(filepath.Join(dst, p)); err == nil {
			t.Errorf("%s was copied", p)
		}
	}
	if info, err := os.Stat(filepath.Join(dst, "d/b.txt")); err != nil || info.Mode().Perm()&0o200 == 0 {
		t.Errorf("not writable: %v %v", info, err)
	}
	if target, err := os.Readlink(filepath.Join(dst, "link")); err != nil || target != "a.txt" {
		t.Errorf("link: %q %v", target, err)
	}
	// a missing source and an unwritable target are errors
	if err := CopyTree(filepath.Join(src, "nope"), dst); err == nil {
		t.Error("a missing source copied")
	}
	if err := CopyTree(src, filepath.Join(src, "a.txt", "x")); err == nil {
		t.Error("a target under a file was made")
	}
	// a link that cannot be read, a file that cannot be read
	if err := os.Chmod(filepath.Join(src, "a.txt"), 0); err != nil {
		t.Fatal(err)
	}
	if os.Getuid() != 0 {
		if err := CopyTree(src, filepath.Join(t.TempDir(), "c")); err == nil {
			t.Error("an unreadable file copied")
		}
	}
}

func TestWorkingTreeCopiesOnlyALockedTree(t *testing.T) {
	root, work := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dir, copied, err := WorkingTree(root, work); dir != root || copied || err != nil {
		t.Errorf("a writable tree is built in: %q %v %v", dir, copied, err)
	}
	if err := os.Chmod(filepath.Join(root, "Cargo.toml"), 0o444); err != nil {
		t.Fatal(err)
	}
	dir, copied, err := WorkingTree(root, work)
	if dir != filepath.Join(work, "src") || !copied || err != nil {
		t.Fatalf("a locked tree is copied: %q %v %v", dir, copied, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err != nil {
		t.Error(err)
	}
	// a work directory that cannot be cleared
	if err := os.WriteFile(filepath.Join(work, "f"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := WorkingTree(root, filepath.Join(work, "f")); err == nil {
		t.Error("copied under a file")
	}
	// an unreadable root is not locked
	if locked(filepath.Join(root, "missing")) {
		t.Error("a missing root is locked")
	}
}

func TestRunProgramSplitsStderrAndDropsWhatIsUnset(t *testing.T) {
	var errOut string
	out, code := RunProgram(context.Background(), Cmd{Name: "sh", Args: []string{"-c", "echo out; echo err >&2; exit 3"}, Stderr: &errOut})
	if out != "out\n" || errOut != "err\n" || code != 3 {
		t.Errorf("%q %q %d", out, errOut, code)
	}
	t.Setenv("ATOM_TEST_KEEP", "1")
	t.Setenv("ATOM_TEST_DROP", "1")
	out, _ = RunProgram(context.Background(), Cmd{Name: "env", Unset: []string{"ATOM_TEST_DROP"}})
	if !strings.Contains(out, "ATOM_TEST_KEEP=1") || strings.Contains(out, "ATOM_TEST_DROP") {
		t.Errorf("%s", out)
	}
}

func TestAnEmptyTreeIsNotLocked(t *testing.T) {
	if locked(t.TempDir()) {
		t.Error("a tree with no file is locked")
	}
}
