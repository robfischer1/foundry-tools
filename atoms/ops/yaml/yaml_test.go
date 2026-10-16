package opsyaml

import (
	"context"
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

func TestOpsYAML(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		{"a star is not an ops tree", map[string]string{"a.yaml": "a: 1\na: 2\n"}, 0, atomtest.Absent, []string{"has no ops shape"}},
		{"an ops tree with no yaml", map[string]string{"policy/a.rego": "x"}, 0, atomtest.Absent, []string{"ABSENT - no yaml in this tree"}},
		{"yaml that loads strictly passes", map[string]string{"flux/a.yaml": "a: 1\n---\nb: 2\n", "policy/a.rego": "x"}, 0, atomtest.Pass,
			[]string{"1 yaml file(s)", "0 with a duplicate key or a parse error"}},
		{"a duplicate key is the defect", map[string]string{"flux/a.yaml": "a: 1\na: 2\n", "flux/ok.yml": "b: 1\n"}, 1, atomtest.Findings,
			[]string{"flux/a.yaml: ", "already defined", "2 yaml file(s), 1 with a duplicate key", "yaml failed (rc=1) — findings"}},
		{"a parse error is a finding too", map[string]string{"flux/a.yaml": "a: [1\n"}, 1, atomtest.Findings, []string{"flux/a.yaml: "}},
		// The chain's other branch runs a repo's tools/yaml-strict. No repository
		// has one, the binary does not port the branch, and a tree that did carry
		// it is graded by the parse alone.
		{"a tools/yaml-strict is not run", map[string]string{"flux/a.yaml": "a: 1\n", "tools/yaml-strict": "#!/bin/sh\nexit 1\n"}, 0, atomtest.Pass, nil},
		// The raw tree, not the gate population: the chains' index was built over
		// everything the repository would commit.
		{"yaml under an excluded directory is still read", map[string]string{"flux/ok.yaml": "a: 1\n", "vendor/bad.yaml": "a: 1\na: 2\n"}, 1, atomtest.Findings,
			[]string{"vendor/bad.yaml: "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			atomtest.Expect(t, run(atomtest.TreeIn(t, tc.files)), atomtest.StateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a file that will not read never ran the atom", func(t *testing.T) {
		in := atomtest.TreeIn(t, map[string]string{"flux/a.yaml": "a: 1\n"})
		in.Committable = append(in.Committable, "flux/gone.yaml")
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the atom never ran: ")
	})
	t.Run("a scan that failed", func(t *testing.T) {
		in := atomtest.MissingRoot(t)
		in.FilesErr = atomtest.ErrBoom
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the tree would not enumerate")
	})
}

// A tree that holds a duplicate key in a nested mapping is a finding, and the
// raw tree (not the gate population) is read: compose.yml is a YAML too.
func TestADuplicateKeyInTheRawTreeIsTheFinding(t *testing.T) {
	in := atomtest.TreeIn(t, map[string]string{"flux/a.yaml": "a: 1\nb: 2\n", "compose.yml": "x: 1\nx: 2\n"})
	atomtest.Expect(t, run(in), atomtest.StateOf(1), atomtest.Findings,
		"compose.yml: ", "already defined", "2 yaml file(s), 1 with a duplicate key or a parse error")
}

func TestMultiDocumentYAMLThatLoadsStrictlyPasses(t *testing.T) {
	in := atomtest.TreeIn(t, map[string]string{"flux/a.yaml": "a: 1\n---\nb: [1, 2]\n"})
	atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass)
}
