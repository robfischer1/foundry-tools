package atoms

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// sized writes a file of exactly n bytes (sparse: the size is what stat reads).
func sized(t *testing.T, dir, rel string, n int64) {
	t.Helper()
	put(t, dir, rel, "")
	if err := os.Truncate(filepath.Join(dir, rel), n); err != nil {
		t.Fatal(err)
	}
}

func TestCheckAddedLargeFiles(t *testing.T) {
	const limit = checks.LargeFileLimitKB * 1024
	a := checks.AtomByID("fleet:check-added-large-files")
	clean := "fleet:check-added-large-files: nothing over 2048 KB"
	for _, tc := range []struct {
		name      string
		build     func(t *testing.T, dir string) Input
		wantState int
		wantLog   string
	}{
		{
			name:      "nothing to measure is a pass",
			build:     func(t *testing.T, dir string) Input { return Input{} },
			wantState: 0, wantLog: clean,
		},
		{
			name: "a file exactly at the limit passes: the ceiling is strictly greater",
			build: func(t *testing.T, dir string) Input {
				sized(t, dir, "edge.bin", limit)
				put(t, dir, "small.txt", "x")
				return Input{Files: []string{"edge.bin", "small.txt"}}
			},
			wantState: 0, wantLog: clean,
		},
		{
			name: "one byte over is a finding, named alone",
			build: func(t *testing.T, dir string) Input {
				sized(t, dir, "over.bin", limit+1)
				sized(t, dir, "edge.bin", limit)
				return Input{Files: []string{"edge.bin", "over.bin"}}
			},
			wantState: 1, wantLog: "files over 2048 KB:\nover.bin",
		},
		{
			name: "findings keep the population's order and a path with spaces survives",
			build: func(t *testing.T, dir string) Input {
				sized(t, dir, "z big.bin", limit+10)
				sized(t, dir, "dir/a  big.bin", limit+10)
				return Input{Files: []string{"dir/a  big.bin", "z big.bin"}}
			},
			wantState: 1, wantLog: "files over 2048 KB:\ndir/a  big.bin\nz big.bin",
		},
		{
			name: "a symlink is measured as itself, as stat without -L does",
			build: func(t *testing.T, dir string) Input {
				sized(t, dir, "target.bin", limit+1)
				if err := os.Symlink("target.bin", filepath.Join(dir, "link.bin")); err != nil {
					t.Fatal(err)
				}
				return Input{Files: []string{"link.bin"}}
			},
			wantState: 0, wantLog: clean,
		},
		{
			name: "a file that cannot be measured is a could-not-run, not a clean tree",
			build: func(t *testing.T, dir string) Input {
				put(t, dir, "small.txt", "x")
				return Input{Files: []string{"missing.bin", "small.txt"}}
			},
			wantState: 2, wantLog: "1 file(s) in the population could not be measured",
		},
		{
			name: "a finding outranks a file that could not be measured",
			build: func(t *testing.T, dir string) Input {
				sized(t, dir, "over.bin", limit+1)
				return Input{Files: []string{"missing.bin", "over.bin"}}
			},
			wantState: 1, wantLog: "files over 2048 KB:\nover.bin",
		},
		{
			name: "a tree that would not enumerate is a could-not-run",
			build: func(t *testing.T, dir string) Input {
				return Input{FilesErr: errors.New("git said no")}
			},
			wantState: 2, wantLog: "the tree would not enumerate: git said no",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			in := tc.build(t, dir)
			in.Root = dir
			v := checkAddedLargeFiles(context.Background(), a, in)
			if v.State != tc.wantState {
				t.Fatalf("state %d, want %d\n%s", v.State, tc.wantState, v.Reason)
			}
			if got := strings.Join(v.Logs, "\n"); !strings.Contains(got, tc.wantLog) {
				t.Errorf("logs %q do not contain %q", got, tc.wantLog)
			}
			if v.Atom != a.ID || v.Stage != a.Stage {
				t.Errorf("verdict is for %s/%s, want %s/%s", v.Atom, v.Stage, a.ID, a.Stage)
			}
		})
	}
}

func TestCheckAddedLargeFilesSaysHowManyWereNotMeasured(t *testing.T) {
	var missing []string
	for _, n := range []string{"1", "2", "3", "4", "5", "6", "7"} {
		missing = append(missing, "missing"+n)
	}
	v := checkAddedLargeFiles(context.Background(), checks.AtomByID("fleet:check-added-large-files"), Input{Root: t.TempDir(), Files: missing})
	if v.State != 2 || !strings.Contains(v.Reason, "7 file(s)") || !strings.Contains(v.Reason, "... and 2 more") {
		t.Errorf("settled %d: %s", v.State, v.Reason)
	}
}

// unmeasured shows the first five and says how many it left out - and says
// nothing when there is nothing left out (the boundary `len(errs) > shown`).
func TestUnmeasuredBoundary(t *testing.T) {
	mk := func(n int) []string {
		var e []string
		for i := range n {
			e = append(e, "e"+strings.Repeat("x", i))
		}
		return e
	}
	for _, tc := range []struct {
		n        int
		wantLine bool
		wantText string
	}{
		{1, false, "e"},
		{4, false, "exxx"},
		{5, false, "exxxx"},
		{6, true, "... and 1 more"},
		{7, true, "... and 2 more"},
	} {
		got := unmeasured(mk(tc.n))
		if has := strings.Contains(got, "... and"); has != tc.wantLine {
			t.Errorf("%d errors: %q (more-line present=%v, want %v)", tc.n, got, has, tc.wantLine)
		}
		if !strings.Contains(got, tc.wantText) {
			t.Errorf("%d errors: %q lacks %q", tc.n, got, tc.wantText)
		}
		if lines := strings.Count(got, "\n") + 1; lines != min(tc.n, 5)+btoi(tc.wantLine) {
			t.Errorf("%d errors shown on %d lines", tc.n, lines)
		}
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestCheckAddedLargeFilesStopsWhenItsContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	put(t, dir, "a.txt", "x")
	v := checkAddedLargeFiles(ctx, checks.AtomByID("fleet:check-added-large-files"), Input{Root: dir, Files: []string{"a.txt"}})
	if v.State != 2 || !strings.Contains(v.Reason, "stopped before the scan finished") {
		t.Errorf("settled %d: %s", v.State, v.Reason)
	}
}

func TestCheckMergeConflict(t *testing.T) {
	a := checks.AtomByID("fleet:check-merge-conflict")
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
			v := checkMergeConflict(context.Background(), a, Input{Root: dir, Files: tc.order})
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

func TestCheckMergeConflictScanThatDidNotFinish(t *testing.T) {
	a := checks.AtomByID("fleet:check-merge-conflict")
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
			v := checkMergeConflict(context.Background(), a, Input{Root: dir, Files: tc.files})
			if v.State != tc.wantState || !strings.Contains(strings.Join(v.Logs, "\n"), tc.wantText) {
				t.Errorf("state %d, want %d, text %q:\n%s", v.State, tc.wantState, tc.wantText, v.Reason)
			}
		})
	}
}

func TestCheckMergeConflictRefusesATreeThatWouldNotEnumerate(t *testing.T) {
	v := checkMergeConflict(context.Background(), checks.AtomByID("fleet:check-merge-conflict"), Input{FilesErr: errors.New("boom")})
	if v.State != 2 || !strings.Contains(v.Reason, "the tree would not enumerate: boom") {
		t.Errorf("settled %d: %s", v.State, v.Reason)
	}
}

func TestCheckMergeConflictStopsWhenItsContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	put(t, dir, "a.txt", "<<<<<<< HEAD\n")
	v := checkMergeConflict(ctx, checks.AtomByID("fleet:check-merge-conflict"), Input{Root: dir, Files: []string{"a.txt"}})
	if v.State != 2 {
		t.Errorf("a stopped scan is not a verdict about the tree: state %d", v.State)
	}
}
