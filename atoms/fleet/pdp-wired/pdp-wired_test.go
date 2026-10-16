package fleetpdpwired

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
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

// treeIn writes files to a directory and answers the In an atom is handed over
// it, WITHOUT git: the population is the file list, as Collect would build it
// for a tree with nothing ignored.
func treeIn(t *testing.T, files map[string]string) atomkit.In {
	t.Helper()
	dir := t.TempDir()
	var names []string
	for name, body := range files {
		put(t, dir, name, body)
		names = append(names, name)
	}
	sort.Strings(names)
	return atomkit.In{
		Root: dir, Files: checks.GatePopulation(names), Committable: names, Tracked: names,
		Now: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
	}
}

// run runs the atom over an input.
func run(t *testing.T, in atomkit.In) checks.Verdict {
	t.Helper()
	return check(context.Background(), in)
}

// expect holds a verdict to its state, its result word (an ABSENT pass reads
// "absent") and the words it must carry. A pass's Reason is only "<id>: PASS" —
// what the atom said rides in its Logs — so the words are looked for in both.
func expect(t *testing.T, v checks.Verdict, state checks.State, result string, needles ...string) {
	t.Helper()
	if v.State != int(state) || v.Result != result {
		t.Errorf("%s: state %d (%s), want %d (%s)\n%s", v.Atom, v.State, v.Result, state, result, v.Reason)
	}
	said := v.Reason + "\n" + strings.Join(v.Logs, "\n")
	for _, n := range needles {
		if !strings.Contains(said, n) {
			t.Errorf("%s: reason and logs lack %q:\n%s", v.Atom, n, said)
		}
	}
}

var pass = checks.StatePass.String()

var findings = checks.StateFindings.String()

var cannot = checks.StateCannotRun.String()

func TestPDPWired(t *testing.T) {
	armed := "kind: Deployment\nspec:\n  template:\n    spec:\n      containers:\n        - name: s\n          env:\n            - {name: STELLAR_PEP_MODE, value: witness}\n"
	wired := armed + "            - {name: STELLAR_PDP_URL, value: \"http://opa/v1\"}\n"

	// ABSENT: not the flux tree.
	expect(t, run(t, treeIn(t, map[string]string{"main.go": "x"})), checks.StatePass, "absent", "not the fleet's flux tree")
	// Not absence: the flux tree with no star manifest in it.
	expect(t, run(t, treeIn(t, map[string]string{"prime/ourea-config.yaml": "x"})), checks.StateCannotRun, cannot, "star manifests moved")
	// Green.
	expect(t, run(t, treeIn(t, map[string]string{"prime/star-a.yaml": wired})), checks.StatePass, pass)
	// Red: names the star.
	expect(t, run(t, treeIn(t, map[string]string{"prime/star-a.yaml": wired, "prime/star-themis.yaml": armed})),
		checks.StateFindings, findings, "prime/star-themis.yaml (container s)")
	// A manifest in the population that will not read is a manifest not judged.
	in := treeIn(t, map[string]string{"prime/star-a.yaml": wired})
	in.Files = append(in.Files, "prime/star-gone.yaml")
	expect(t, run(t, in), checks.StateCannotRun, cannot, "prime/star-gone.yaml would not read")
	// A file list that would not enumerate is not a pass.
	in = treeIn(t, map[string]string{"prime/star-a.yaml": wired})
	in.FilesErr = os.ErrPermission
	expect(t, run(t, in), checks.StateCannotRun, cannot, "would not enumerate")
	// A root that will not list is a tree not examined.
	in = treeIn(t, map[string]string{"prime/star-a.yaml": wired})
	in.Root = filepath.Join(in.Root, "gone")
	expect(t, run(t, in), checks.StateCannotRun, cannot, "would not enumerate")
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
	if Def.Desc != "Every container in flux's prime/star-*.yaml that arms the stellar-core-go policy PEP (STELLAR_PEP_MODE not off) also sets STELLAR_PDP_URL - unset, the PEP consults nothing and, in witness mode, refuses nothing." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if !slices.Equal(Def.Needs, nil) {
		t.Errorf("needs %v", Def.Needs)
	}
}
