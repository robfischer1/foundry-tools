package fleetstopjustifications

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

// The directive is assembled from fragments: written whole, the gate's own
// stop-justifications scan of THIS file would read it as a suppression.
const sjNoqa = "no" + "qa"

func TestStopJustifications(t *testing.T) {
	drive := "import subprocess\np = subprocess.Popen([x])  # " + sjNoqa + ": S603\n"
	now := time.Date(2026, 10, 8, 3, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		files     map[string]string
		origin    string
		wantState int
		wantLog   string
	}{
		{"a clean tree", map[string]string{"a.py": "x = 1\n"}, "", 0, "stop-justifications: 1 file(s) scanned, no undocumented suppressions."},
		{"an undocumented suppression", map[string]string{"a.py": "x = 1  # " + sjNoqa + ": BLE001\n"}, "", 1, "a.py"},
		{"the origin names the exemption: a path-style URL", map[string]string{"probes/seam/drive.py": drive}, "http://door/cerberus.git", 0, "excused by a DIRECTORY exemption in cerberus"},
		{"the origin names the exemption: an scp-style URL", map[string]string{"probes/seam/drive.py": drive}, "git@host:rob/cerberus.git", 0, "excused by a DIRECTORY exemption in cerberus"},
		{"another repository's exemption is not this one's", map[string]string{"probes/seam/drive.py": drive}, "http://door/notcerberus.git", 1, "probes/seam/drive.py"},
		{"no origin names no exemption", map[string]string{"probes/seam/drive.py": drive}, "", 1, "probes/seam/drive.py"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var tracked []string
			for rel, body := range tc.files {
				put(t, dir, rel, body)
				tracked = append(tracked, rel)
			}
			v := check(context.Background(), atomkit.In{Root: dir, Tracked: tracked, Origin: tc.origin, Now: now})
			if v.State != tc.wantState || !strings.Contains(strings.Join(v.Logs, "\n"), tc.wantLog) {
				t.Errorf("state %d, want %d, wanting %q:\n%s", v.State, tc.wantState, tc.wantLog, v.Reason)
			}
		})
	}
}

// Justification expiry is the reason this atom is whole-tree scope. The grant
// below ends 2026-11-20 (checks.DirectoryExemption); Now is converted to UTC
// before the date is taken, so 23:30 on the 20th in New York — already the
// 21st in UTC — is past it.
func TestStopJustificationsReadsTheUTCDate(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "bases/blade-go/Dockerfile", "FROM x\n# hado"+"lint ignore=DL3002\n")
	newYork := time.FixedZone("EST", -5*3600)
	for _, tc := range []struct {
		name      string
		now       time.Time
		wantState int
	}{
		{"the grant's last day, UTC", time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC), 0},
		{"the day after, UTC", time.Date(2026, 11, 21, 0, 0, 1, 0, time.UTC), 1},
		{"the 20th in New York is the 21st in UTC", time.Date(2026, 11, 20, 23, 30, 0, 0, newYork), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := atomkit.In{Root: dir, Tracked: []string{"bases/blade-go/Dockerfile"}, Origin: "http://door/foundry-stocks.git", Now: tc.now}
			if v := check(context.Background(), in); v.State != tc.wantState {
				t.Errorf("state %d, want %d\n%s", v.State, tc.wantState, v.Reason)
			}
		})
	}
}

func TestStopJustificationsCannotRun(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in       atomkit.In
		wantText string
	}{
		{"a tree that will not list", atomkit.In{TrackedErr: errors.New("git ls-files failed")}, "CANNOT RUN — git ls-files failed"},
		{"a tracked file that will not read", atomkit.In{Root: t.TempDir(), Tracked: []string{"gone.py"}}, "could not read gone.py"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := check(context.Background(), tc.in)
			if v.State != 2 || !strings.Contains(strings.Join(v.Logs, "\n"), tc.wantText) {
				t.Errorf("state %d:\n%s", v.State, v.Reason)
			}
		})
	}
}

// The declaration is the catalogue row it replaced: the same stage, group, text and
// fork lane, and the needs the split table gives it.
func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "none" || Def.NotOnForks != checks.ForkFleetStyle || Def.SubsumedBy != "" || Def.NeedsManifest {
		t.Errorf("%+v", Def)
	}
	if Def.Desc != "No silent suppression of any gate — a suppression carries a tool-conflict line." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if !slices.Equal(Def.Needs, nil) {
		t.Errorf("needs %v", Def.Needs)
	}
}
