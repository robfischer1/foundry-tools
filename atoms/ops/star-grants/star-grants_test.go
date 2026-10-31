package opsstargrants

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// run is the atom's check over an input, as the runner calls it.
func run(in atoms.Input) checks.Verdict { return check(context.Background(), in) }

func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	atomtest.DeclaresItsRow(t, Def, "prepush", "none")
	if Def.ID != id {
		t.Errorf("%+v", Def)
	}
}

func deployment(name string) string {
	return "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: " + name + "\nspec: {}\n"
}

func grants(n string) string {
	return "apiVersion: cilium.io/v2\nkind: CiliumNetworkPolicy\nmetadata:\n  name: grants-" + n + "\n"
}

// primeRepo is flux as it stands on main: a granted star (aaa) and a star that
// has no grants (charon), which the ratchet must leave alone. It answers the
// repository and the base.
func primeRepo(t *testing.T) (dir, base string) {
	t.Helper()
	dir = atomtest.NewRepo(t)
	atomtest.Put(t, dir, "README.md", "one\n")
	atomtest.Put(t, dir, "prime/star-aaa.yaml", deployment("aaa"))
	atomtest.Put(t, dir, "prime/star-charon.yaml", deployment("charon"))
	atomtest.Put(t, dir, "prime/first-l7-enforcement.yaml", grants("aaa"))
	return dir, atomtest.CommitAll(t, dir, "base")
}

func inAt(dir, base string) atoms.Input {
	return atoms.Collect(context.Background(), dir, base, "", time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC))
}

func TestOpsStarGrants(t *testing.T) {
	for _, tc := range []struct {
		name    string
		edit    func(t *testing.T, dir string)
		mutate  func(in *atoms.Input)
		state   int
		result  string
		needles []string
	}{
		{"a new star's Deployment with no grants is a finding", func(t *testing.T, dir string) {
			atomtest.Put(t, dir, "prime/star-hecate.yaml", deployment("hecate"))
		}, nil, 1, atomtest.Findings, []string{"STAR prime/star-hecate.yaml", "grants-hecate"}},
		{"the same pull adding grants-<n> passes", func(t *testing.T, dir string) {
			atomtest.Put(t, dir, "prime/star-hecate.yaml", deployment("hecate"))
			atomtest.Put(t, dir, "prime/first-l7-enforcement.yaml", grants("aaa")+"---\n"+grants("hecate"))
		}, nil, 0, atomtest.Pass, []string{"grants-hecate is in prime/"}},
		{"the opt-out annotation passes", func(t *testing.T, dir string) {
			atomtest.Put(t, dir, "prime/star-batch.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: batch\n  annotations:\n    forge.notusmi.com/no-grants: \"no callers\"\n")
		}, nil, 0, atomtest.Pass, []string{"opted out of grants-batch"}},
		{"stars already on main without grants are not graded (the ratchet)", func(t *testing.T, dir string) {
			atomtest.Put(t, dir, "prime/star-charon.yaml", deployment("charon")+"# edited\n")
			atomtest.Put(t, dir, "prime/star-new.yaml", deployment("new"))
			atomtest.Put(t, dir, "prime/first-l7-enforcement.yaml", grants("aaa")+"---\n"+grants("new"))
		}, nil, 0, atomtest.Pass, []string{"grants-new is in prime/"}},
		{"a pull that edits an ungranted star and adds none passes", func(t *testing.T, dir string) {
			atomtest.Put(t, dir, "prime/star-charon.yaml", deployment("charon")+"# edited\n")
		}, nil, 0, atomtest.Pass, []string{"no prime/star-*.yaml added"}},
		{"a tip run with no base is absent", nil, func(in *atoms.Input) { in.Base = "" }, 0, atomtest.Absent, []string{"no base to compare against"}},
		{"a base the history does not reach cannot run", nil, func(in *atoms.Input) { in.Base = strings.Repeat("a", 40) }, 2, atomtest.Cannot, []string{"CANNOT RUN"}},
		{"a manifest that does not parse cannot run", func(t *testing.T, dir string) {
			atomtest.Put(t, dir, "prime/star-new.yaml", "kind: [\n")
		}, nil, 2, atomtest.Cannot, []string{"prime/star-new.yaml did not parse"}},
		{"a population that failed cannot run", nil, func(in *atoms.Input) { in.FilesErr = atomtest.ErrBoom }, 2, atomtest.Cannot, []string{"the tree would not enumerate"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, base := primeRepo(t)
			if tc.edit != nil {
				tc.edit(t, dir)
				atomtest.CommitAll(t, dir, "pull")
			}
			in := inAt(dir, base)
			if tc.mutate != nil {
				tc.mutate(&in)
			}
			atomtest.Expect(t, run(in), atomtest.StateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a tree with no star manifest is absent", func(t *testing.T) {
		dir := atomtest.NewRepo(t)
		atomtest.Put(t, dir, "README.md", "one\n")
		base := atomtest.CommitAll(t, dir, "base")
		atomtest.Expect(t, run(inAt(dir, base)), atomtest.StateOf(0), atomtest.Absent, "no prime/star-*.yaml in this tree")
	})
	t.Run("a tree git cannot read makes the added-file list a 2", func(t *testing.T) {
		dir, base := primeRepo(t)
		atomtest.Put(t, dir, "prime/star-new.yaml", deployment("new"))
		atomtest.CommitAll(t, dir, "pull")
		in := inAt(dir, base)
		tree := atomtest.GitIn(t, dir, "rev-parse", "HEAD^{tree}")
		if err := os.Remove(filepath.Join(dir, ".git", "objects", tree[:2], tree[2:])); err != nil {
			t.Fatal(err)
		}
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the added files against "+base+" would not list (exit ")
	})
	t.Run("a prime manifest that will not read is a 2, and says which", func(t *testing.T) {
		dir, base := primeRepo(t)
		atomtest.Put(t, dir, "prime/star-new.yaml", deployment("new"))
		atomtest.CommitAll(t, dir, "pull")
		in := inAt(dir, base)
		in.Files = append(in.Files, "prime/ghost.yaml")
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "prime/ghost.yaml could not be read")
	})
}
