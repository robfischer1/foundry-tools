package opschezmoi

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

func TestOpsChezmoi(t *testing.T) {
	source := map[string]string{"dot_zshrc": "x", "dot_gitconfig.tmpl": "{{ .a }}", "scripts/run.sh.tmpl": "echo {{ .b }}"}
	version := func(c atoms.Cmd) bool { return c.Args[0] == "--version" }
	for _, tc := range []struct {
		name    string
		answer  func(c atoms.Cmd) (string, int)
		state   int
		result  string
		needles []string
	}{
		{"every template renders", func(atoms.Cmd) (string, int) { return "", 0 }, 0, atomtest.Pass, []string{"chezmoi: 2 template(s) checked"}},
		{"a template that will not execute is a finding with its error", func(c atoms.Cmd) (string, int) {
			if !version(c) && c.Stdin == "{{ .a }}" {
				return "template: bad\nline 2", 1
			}
			return "", 0
		}, 1, atomtest.Findings, []string{"FAIL dot_gitconfig.tmpl\n    template: bad\n    line 2", "chezmoi failed (rc=1) — findings"}},
		{"a fault of the substrate is a 2", func(c atoms.Cmd) (string, int) {
			if !version(c) {
				return "dial tcp: lookup", 1
			}
			return "", 0
		}, 2, atomtest.Cannot, []string{"failed on a fault of the substrate"}},
		{"a chezmoi that would not start never ran", func(c atoms.Cmd) (string, int) {
			if !version(c) {
				return "chezmoi: gone", -1
			}
			return "", 0
		}, 2, atomtest.Cannot, []string{"the atom never ran: chezmoi: gone"}},
		{"a chezmoi that fails its probe is unprovisioned", func(atoms.Cmd) (string, int) { return "gone", 127 }, 2, atomtest.Cannot,
			[]string{"the phase's tool could not be provisioned", "chezmoi --version exited 127"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := atomtest.ToolTree(t, source, tc.answer)
			atomtest.Expect(t, run(in), atomtest.StateOf(tc.state), tc.result, tc.needles...)
			if tc.state == 0 {
				if len(f.Calls) != 3 || atomtest.Flagged(f.Calls[1]) != "--source . execute-template" || f.Calls[1].Stdin != "{{ .a }}" || f.Calls[2].Stdin != "echo {{ .b }}" {
					t.Errorf("chezmoi ran as %+v", f.Calls)
				}
				if f.Calls[1].Dir != in.Root || !f.Calls[1].Both {
					t.Errorf("chezmoi ran in %q (both streams %v)", f.Calls[1].Dir, f.Calls[1].Both)
				}
			}
		})
	}
	for name, files := range map[string]map[string]string{
		"a tree that is not an ops tree":     {"a.tmpl": "x"},
		"an ops tree that is not chezmoi's":  {"flux/x.yaml": "a: 1\n", "a.tmpl": "x"},
		"a chezmoi source with no templates": {"dot_zshrc": "x"},
	} {
		t.Run(name+" is absent and chezmoi is not touched", func(t *testing.T) {
			in, f := atomtest.ToolTree(t, files, nil)
			atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Absent)
			if len(f.Calls) != 0 {
				t.Errorf("an absent atom touched %v", f.Ran())
			}
		})
	}
	t.Run("a template that will not read never ran", func(t *testing.T) {
		in, _ := atomtest.ToolTree(t, source, nil)
		in.Committable = append(in.Committable, "ghost.tmpl")
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the atom never ran")
	})
	t.Run("a population that failed", func(t *testing.T) {
		in, _ := atomtest.ToolTree(t, source, nil)
		in.FilesErr = atomtest.ErrBoom
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the tree would not enumerate")
	})
}
