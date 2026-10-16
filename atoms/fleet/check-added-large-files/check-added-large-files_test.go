package fleetcheckaddedlargefiles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
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
	clean := "fleet:check-added-large-files: nothing over 2048 KB"
	for _, tc := range []struct {
		name      string
		build     func(t *testing.T, dir string) atomkit.In
		wantState int
		wantLog   string
	}{
		{
			name:      "nothing to measure is a pass",
			build:     func(t *testing.T, dir string) atomkit.In { return atomkit.In{} },
			wantState: 0, wantLog: clean,
		},
		{
			name: "a file exactly at the limit passes: the ceiling is strictly greater",
			build: func(t *testing.T, dir string) atomkit.In {
				sized(t, dir, "edge.bin", limit)
				put(t, dir, "small.txt", "x")
				return atomkit.In{Files: []string{"edge.bin", "small.txt"}}
			},
			wantState: 0, wantLog: clean,
		},
		{
			name: "one byte over is a finding, named alone",
			build: func(t *testing.T, dir string) atomkit.In {
				sized(t, dir, "over.bin", limit+1)
				sized(t, dir, "edge.bin", limit)
				return atomkit.In{Files: []string{"edge.bin", "over.bin"}}
			},
			wantState: 1, wantLog: "files over 2048 KB:\nover.bin",
		},
		{
			name: "findings keep the population's order and a path with spaces survives",
			build: func(t *testing.T, dir string) atomkit.In {
				sized(t, dir, "z big.bin", limit+10)
				sized(t, dir, "dir/a  big.bin", limit+10)
				return atomkit.In{Files: []string{"dir/a  big.bin", "z big.bin"}}
			},
			wantState: 1, wantLog: "files over 2048 KB:\ndir/a  big.bin\nz big.bin",
		},
		{
			name: "a symlink is measured as itself, as stat without -L does",
			build: func(t *testing.T, dir string) atomkit.In {
				sized(t, dir, "target.bin", limit+1)
				if err := os.Symlink("target.bin", filepath.Join(dir, "link.bin")); err != nil {
					t.Fatal(err)
				}
				return atomkit.In{Files: []string{"link.bin"}}
			},
			wantState: 0, wantLog: clean,
		},
		{
			name: "a file that cannot be measured is a could-not-run, not a clean tree",
			build: func(t *testing.T, dir string) atomkit.In {
				put(t, dir, "small.txt", "x")
				return atomkit.In{Files: []string{"missing.bin", "small.txt"}}
			},
			wantState: 2, wantLog: "1 file(s) in the population could not be measured",
		},
		{
			name: "a finding outranks a file that could not be measured",
			build: func(t *testing.T, dir string) atomkit.In {
				sized(t, dir, "over.bin", limit+1)
				return atomkit.In{Files: []string{"missing.bin", "over.bin"}}
			},
			wantState: 1, wantLog: "files over 2048 KB:\nover.bin",
		},
		{
			name: "a tree that would not enumerate is a could-not-run",
			build: func(t *testing.T, dir string) atomkit.In {
				return atomkit.In{FilesErr: errors.New("git said no")}
			},
			wantState: 2, wantLog: "the tree would not enumerate: git said no",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			in := tc.build(t, dir)
			in.Root = dir
			v := check(context.Background(), in)
			if v.State != tc.wantState {
				t.Fatalf("state %d, want %d\n%s", v.State, tc.wantState, v.Reason)
			}
			if got := strings.Join(v.Logs, "\n"); !strings.Contains(got, tc.wantLog) {
				t.Errorf("logs %q do not contain %q", got, tc.wantLog)
			}
			if v.Atom != id || v.Stage != row.Stage {
				t.Errorf("verdict is for %s/%s, want %s/%s", v.Atom, v.Stage, id, row.Stage)
			}
		})
	}
}

func TestCheckAddedLargeFilesSaysHowManyWereNotMeasured(t *testing.T) {
	var missing []string
	for _, n := range []string{"1", "2", "3", "4", "5", "6", "7"} {
		missing = append(missing, "missing"+n)
	}
	v := check(context.Background(), atomkit.In{Root: t.TempDir(), Files: missing})
	if v.State != 2 || !strings.Contains(v.Reason, "7 file(s)") || !strings.Contains(v.Reason, "... and 2 more") {
		t.Errorf("settled %d: %s", v.State, v.Reason)
	}
}

func TestCheckAddedLargeFilesStopsWhenItsContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	put(t, dir, "a.txt", "x")
	v := check(ctx, atomkit.In{Root: dir, Files: []string{"a.txt"}})
	if v.State != 2 || !strings.Contains(v.Reason, "stopped before the scan finished") {
		t.Errorf("settled %d: %s", v.State, v.Reason)
	}
}

// The declaration is the catalogue row it replaced: the same stage, group, text and
// fork lane, and the needs the split table gives it.
func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "none" || Def.NotOnForks != "" || Def.SubsumedBy != "" || Def.NeedsManifest {
		t.Errorf("%+v", Def)
	}
	if Def.Desc != "No file in the tree exceeds 2 MB." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if !slices.Equal(Def.Needs, nil) {
		t.Errorf("needs %v", Def.Needs)
	}
}
