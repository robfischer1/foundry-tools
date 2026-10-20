package fleetspeckitrendercurrent

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// treeIn writes files to a directory and answers the In an atom is handed over
// it, WITHOUT git.
func treeIn(t *testing.T, files map[string]string) atomkit.In {
	t.Helper()
	dir := t.TempDir()
	var names []string
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return atomkit.In{
		Root: dir, Files: checks.GatePopulation(names), Committable: names, Tracked: names,
		Now: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC),
	}
}

func TestSpeckitRenderCurrent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   map[string]string
		in      func(*testing.T) atomkit.In
		state   checks.State
		needles []string
	}{
		{"no answers file passes", map[string]string{"a": "x"}, nil, checks.StatePass, []string{"takes no speckit template"}},
		{"a pin and a render that agree pass", map[string]string{answersFile: "_commit: v0.232.0\n_src_path: x\n_rendered: v0.232.0\n"}, nil, checks.StatePass, []string{"no pin without its render"}},
		{"a quoted stamp that agrees passes", map[string]string{answersFile: "_commit: 'v0.232.0'\n_rendered: \"v0.232.0\"\n"}, nil, checks.StatePass, []string{"no pin without its render"}},
		{"a file from before the stamp passes", map[string]string{answersFile: "_commit: v0.231.0\n_src_path: x\n"}, nil, checks.StatePass, []string{"no pin without its render"}},
		{"a file with no pin passes", map[string]string{answersFile: "_rendered: v0.231.0\n"}, nil, checks.StatePass, []string{"no pin without its render"}},
		{"a pin ahead of its render is a finding", map[string]string{answersFile: "_commit: v0.233.0\n_src_path: x\n_rendered: v0.232.0\n"}, nil, checks.StateFindings,
			[]string{"pins _commit v0.233.0", "rendered from v0.232.0", "--vcs-ref v0.233.0", "FIX:"}},
		{"an answers file that will not read is a file not examined", map[string]string{answersFile + "/x": "x"}, nil, checks.StateCannotRun, []string{"would not read"}},
		{"a tree that would not enumerate", nil, func(t *testing.T) atomkit.In {
			return atomkit.In{Root: t.TempDir() + "/not-there", Now: time.Now()}
		}, checks.StateCannotRun, []string{"enumerate"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var in atomkit.In
			if tc.in != nil {
				in = tc.in(t)
			} else {
				in = treeIn(t, tc.files)
			}
			v := check(context.Background(), in)
			if v.State != int(tc.state) {
				t.Errorf("state %d, want %d\n%s", v.State, tc.state, v.Reason)
			}
			said := v.Reason + "\n" + strings.Join(v.Logs, "\n")
			for _, n := range tc.needles {
				if !strings.Contains(said, n) {
					t.Errorf("reason and logs lack %q:\n%s", n, said)
				}
			}
		})
	}
}

func TestValueOf(t *testing.T) {
	if v, ok := valueOf("a: 1\n_commit: v1\n", "_commit"); !ok || v != "v1" {
		t.Errorf("%q %v", v, ok)
	}
	if _, ok := valueOf("# _commit: v1\nx_commit: v2\n", "_commit"); ok {
		t.Error("a comment or a longer key matched")
	}
}

func TestTheDeclaration(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(Def.ID) || Def.Toolchain != "none" {
		t.Errorf("%+v", Def)
	}
}
