package opskubelinter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// run is the atom's check over an input, as the runner calls it.
func run(in atoms.Input) checks.Verdict { return check(context.Background(), in) }

func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	atomtest.DeclaresItsRow(t, Def, "precommit", "none")
	if Def.ID != id {
		t.Errorf("%+v", Def)
	}
}

// kubeLinter answers kube-linter from a script and records what it was asked.
func kubeLinter(t *testing.T, files map[string]string, answer func(atoms.Cmd) (string, int)) (atoms.Input, *atomtest.ToolFake) {
	t.Helper()
	return atomtest.ToolTree(t, files, answer)
}

// fluxTree is the infra shape: every manifest under flux/.
func fluxTree() map[string]string {
	return map[string]string{"flux/clusters/home/apps.yaml": "x", "flux/apps/kustomization.yaml": "resources: []\n"}
}

func TestOpsKubeLinterRunsItsLinterOverFluxAndMapsItsExitCodes(t *testing.T) {
	in, f := kubeLinter(t, fluxTree(), nil)
	atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass, "ops:kube-linter: clean")
	c := f.Call("kube-linter")
	if got := atomtest.Flagged(c); got != "lint --fail-if-no-objects-found flux/" || c.Dir != in.Root || !c.Both {
		t.Errorf("kube-linter ran as %+v: want lint --fail-if-no-objects-found flux/ in the root, both streams", c)
	}

	// Findings: the linter's own count leads (it is the line a human reads first,
	// and kube-linter prints it last), then the findings themselves.
	in, _ = kubeLinter(t, fluxTree(), func(atoms.Cmd) (string, int) {
		return "flux/a.yaml: (object: app apps/v1, Deployment) container \"app\" does not have a read-only root file system\nError: found 1 lint errors\n", 1
	})
	v := run(in)
	atomtest.Expect(t, v, atomtest.StateOf(1), atomtest.Findings, "Error: found 1 lint errors", "read-only root file system")
	if !strings.Contains(v.Reason, "Error: found 1 lint errors\nflux/a.yaml:") {
		t.Errorf("the tail must lead the head:\n%s", v.Reason)
	}
}

// --fail-if-no-objects-found IS kube-linter's own zero-population refusal, and it
// exits 1 for it - the same code it uses for a finding. Reading that as findings
// would be wrong in the direction that still looks like the check worked.
func TestOpsKubeLinterZeroObjectsIsACouldNotRunNotAFinding(t *testing.T) {
	in, _ := kubeLinter(t, fluxTree(), func(atoms.Cmd) (string, int) { return "Error: no valid objects found\n", 1 })
	atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "CANNOT RUN", "parsed no object under flux/")
}

func TestOpsKubeLinterIsAbsentWithoutFluxAndRefusesAnUnreadableRoot(t *testing.T) {
	in, f := kubeLinter(t, map[string]string{"main.go": "x"}, nil)
	v := run(in)
	atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Absent, "no Flux tree here: neither flux/ nor clusters/<name>/kustomization.yaml")
	if len(f.Calls) != 0 {
		t.Errorf("no Flux tree is decided in Go, before any program: %v", f.Ran())
	}

	in = atomtest.MissingRoot(t)
	in.FilesErr = atomtest.ErrBoom
	atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the tree would not enumerate", "boom")
}

func TestOpsKubeLinterThatCannotStartNeverRan(t *testing.T) {
	in, _ := kubeLinter(t, fluxTree(), func(atoms.Cmd) (string, int) { return "kube-linter: not found", -1 })
	atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the atom never ran", "kube-linter: not found")
}

// A REPOSITORY THAT IS THE FLUX TREE (foundry/flux since 2026-09-29): its
// clusters/ and one directory per Kustomization sit at the root. ops:kube-linter
// lints exactly the paths its CRs apply plus clusters/ - not the whole root,
// which carries docs and tooling.
func TestOpsKubeLinterRunsOnARootFluxTree(t *testing.T) {
	cr := "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nspec:\n  path: ./prime\n  sourceRef: {name: flux-system}\n"
	// clusters/ and the CR paths, from EVERY cluster manifest, and not a
	// kustomization.yaml no CR applies (data/ here).
	in, f := kubeLinter(t, map[string]string{
		"clusters/pantheon/kustomization.yaml": "resources: [prime.yaml, spire.yaml]\n",
		"clusters/pantheon/prime.yaml":         cr,
		"clusters/pantheon/spire.yaml":         strings.Replace(cr, "./prime", "./spire", 1),
		"prime/kustomization.yaml":             "resources: []\n",
		"spire/kustomization.yaml":             "resources: []\n",
		"data/kustomization.yaml":              "resources: []\n",
	}, nil)
	atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass)
	if got := atomtest.Flagged(f.Call("kube-linter")); got != "lint --fail-if-no-objects-found clusters/ prime spire" {
		t.Errorf("kube-linter must lint clusters/ and the CR paths of every cluster manifest, nothing else: %s", got)
	}

	// No CR at the root: every <dir>/kustomization.yaml but clusters/.
	in, f = kubeLinter(t, map[string]string{
		"clusters/pantheon/kustomization.yaml": "resources: []\n",
		"prime/kustomization.yaml":             "resources: []\n",
	}, nil)
	atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass)
	if got := atomtest.Flagged(f.Call("kube-linter")); got != "lint --fail-if-no-objects-found clusters/ prime" {
		t.Errorf("with no CR, kube-linter lints the fallback trees: %s", got)
	}

	// A cluster manifest that cannot be read is a CANNOT RUN, never a lint of a
	// guessed population.
	in, f = kubeLinter(t, map[string]string{
		"clusters/pantheon/kustomization.yaml": "resources: [prime.yaml]\n",
		"clusters/pantheon/prime.yaml":         cr,
		"prime/kustomization.yaml":             "resources: []\n",
	}, nil)
	if err := os.Remove(filepath.Join(in.Root, "clusters/pantheon/prime.yaml")); err != nil {
		t.Fatal(err)
	}
	atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "CANNOT RUN - clusters/pantheon/prime.yaml could not be read")
	if len(f.Calls) != 0 {
		t.Errorf("no lint may run on an unreadable cluster manifest: %v", f.Ran())
	}
}
