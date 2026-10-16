package fleetcheckmergeconflict

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// put writes a file, making its directories.
func put(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestUT_488_TheDeclarationIsTheRowItReplaced: the Def carries the identity the
// catalogue row did, and validates.
func TestUT_488_TheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != "precommit" || Def.Group != checks.GroupOf(Def.ID) || Def.Toolchain != "none" {
		t.Errorf("%+v", Def)
	}
}

func TestMergeConflict(t *testing.T) {
	clean := "fleet:check-merge-conflict: no conflict markers"
	for _, tc := range []struct {
		name      string
		files     map[string]string
		order     []string
		wantState int
		want      string // the whole result, for findings; a substring for the rest
	}{
		{"an empty population is a pass", nil, nil, 0, clean},
		{"a clean file", map[string]string{"a.txt": "hello\nworld\n"}, []string{"a.txt"}, 0, clean},
		{
			"the opening marker, numbered from 1",
			map[string]string{"a.txt": "one\ntwo\n<<<<<<< HEAD\nmine\n"},
			[]string{"a.txt"}, 1, "a.txt:3:<<<<<<< HEAD",
		},
		{
			"the closing marker, on a last line with no newline",
			map[string]string{"a.txt": "one\n>>>>>>> feature"},
			[]string{"a.txt"}, 1, "a.txt:2:>>>>>>> feature",
		},
		{
			"both markers in one file, and the middle row alone is not one",
			map[string]string{"a.txt": "<<<<<<< HEAD\nmine\n=======\ntheirs\n>>>>>>> other\n"},
			[]string{"a.txt"}, 1, "a.txt:1:<<<<<<< HEAD\na.txt:5:>>>>>>> other",
		},
		{
			"a row of equals signs is a setext heading, not a conflict",
			map[string]string{"a.md": "Title\n=======\ntext\n"},
			[]string{"a.md"}, 0, clean,
		},
		{
			"the marker is anchored to the line start and needs its space",
			map[string]string{"a.txt": "  <<<<<<< HEAD\n<<<<<<<HEAD\n<<<<<< HEAD\n>>>>>>>\nx <<<<<<< HEAD\n"},
			[]string{"a.txt"}, 0, clean,
		},
		{
			"a binary file is skipped whole, markers and all",
			map[string]string{"a.bin": "\x00\x01\n<<<<<<< HEAD\n"},
			[]string{"a.bin"}, 0, clean,
		},
		{
			"a NUL after the marker still makes the file binary",
			map[string]string{"a.bin": "<<<<<<< HEAD\n\x00"},
			[]string{"a.bin"}, 0, clean,
		},
		{
			"hits follow the population's order across files, and keep a carriage return that is not last",
			map[string]string{"b.txt": "x\r\n<<<<<<< HEAD\r\n", "a.txt": ">>>>>>> y\n"},
			[]string{"b.txt", "a.txt"}, 1, "b.txt:2:<<<<<<< HEAD\r\na.txt:1:>>>>>>> y",
		},
		{
			"a binary file beside a text one: only the text one is read for markers",
			map[string]string{"a.bin": "\x00<<<<<<< HEAD\n", "b.txt": "<<<<<<< HEAD\n"},
			[]string{"a.bin", "b.txt"}, 1, "b.txt:1:<<<<<<< HEAD",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, body := range tc.files {
				put(t, dir, rel, body)
			}
			v := check(context.Background(), atomkit.In{Root: dir, Files: tc.order})
			if v.State != tc.wantState {
				t.Fatalf("state %d, want %d\n%s", v.State, tc.wantState, v.Reason)
			}
			got := strings.Join(v.Logs, "\n")
			if tc.wantState == 1 {
				if got != tc.want {
					t.Errorf("findings %q, want exactly %q", got, tc.want)
				}
			} else if !strings.Contains(got, tc.want) {
				t.Errorf("logs %q lack %q", got, tc.want)
			}
		})
	}
}

func TestMergeConflictScanThatDidNotFinish(t *testing.T) {
	for _, tc := range []struct {
		name      string
		files     []string
		wantState int
		wantText  string
	}{
		{"an unreadable file and no hit is a could-not-run", []string{"gone.txt", "ok.txt"}, 2, "1 file(s) unreadable"},
		{"a hit outranks an unreadable file", []string{"gone.txt", "bad.txt"}, 1, "bad.txt:1:<<<<<<< HEAD"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			put(t, dir, "ok.txt", "fine\n")
			put(t, dir, "bad.txt", "<<<<<<< HEAD\n")
			v := check(context.Background(), atomkit.In{Root: dir, Files: tc.files})
			if v.State != tc.wantState || !strings.Contains(strings.Join(v.Logs, "\n"), tc.wantText) {
				t.Errorf("state %d, want %d, text %q:\n%s", v.State, tc.wantState, tc.wantText, v.Reason)
			}
		})
	}
}

func TestMergeConflictRefusesATreeThatWouldNotEnumerate(t *testing.T) {
	v := check(context.Background(), atomkit.In{FilesErr: errors.New("boom")})
	if v.State != 2 || !strings.Contains(v.Reason, "the tree would not enumerate: boom") {
		t.Errorf("settled %d: %s", v.State, v.Reason)
	}
}

func TestMergeConflictStopsWhenItsContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	put(t, dir, "a.txt", "<<<<<<< HEAD\n")
	v := check(ctx, atomkit.In{Root: dir, Files: []string{"a.txt"}})
	if v.State != 2 {
		t.Errorf("a stopped scan is not a verdict about the tree: state %d", v.State)
	}
}
