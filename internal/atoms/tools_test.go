package atoms

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// toolFake stands in for the programs on PATH through Input.Exec: it answers
// each call from a script, records it, and fails the test for a program the
// tools container does not carry (Programs).
type toolFake struct {
	t      *testing.T
	calls  []Cmd
	answer func(c Cmd) (string, int)
}

func (f *toolFake) exec(_ context.Context, c Cmd) (string, int) {
	f.t.Helper()
	if !slices.Contains(Programs, c.Name) {
		f.t.Errorf("exec of %q: no layer of the tools container carries it", c.Name)
	}
	f.calls = append(f.calls, c)
	if f.answer == nil {
		return "", 0
	}
	return f.answer(c)
}

// ran is the program names the atom exec'd, in order.
func (f *toolFake) ran() []string {
	var names []string
	for _, c := range f.calls {
		names = append(names, c.Name)
	}
	return names
}

// call is the first call of a program, or a failed test.
func (f *toolFake) call(name string) Cmd {
	f.t.Helper()
	for _, c := range f.calls {
		if c.Name == name {
			return c
		}
	}
	f.t.Fatalf("%s was never exec'd; ran %v", name, f.ran())
	return Cmd{}
}

// toolTree is treeIn with the exec seam set to the script.
func toolTree(t *testing.T, files map[string]string, answer func(Cmd) (string, int)) (Input, *toolFake) {
	t.Helper()
	in := treeIn(t, files)
	f := &toolFake{t: t, answer: answer}
	in.Exec = f.exec
	return in, f
}

// flagged is an argv as one string, for a needle.
func flagged(c Cmd) string { return strings.Join(c.Args, " ") }

// EVERY PROGRAM A TOOL ATOM EXECS IS IN Programs: the list the module's
// container test holds the layers to. The atoms are run against a fake that
// says yes to everything, and each program they name must be listed.
func TestEveryToolAtomExecsAListedProgram(t *testing.T) {
	trees := map[string]map[string]string{
		"fleet:opengrep-sast":    {"rules/sast/a.yml": "x"},
		"fleet:hadolint":         {"Dockerfile": "FROM x\n"},
		"dies:opa-test":          dieTree(nil),
		"dies:admission-dogfood": dieTree(map[string]string{"policy/admission/a.rego": "x", "tests/fixtures/ouranos-self.json": "{}"}),
		"dies:canary-visibility": dieTree(nil),
		"ops:shell":              {"flux/x.yaml": "a: 1", "run.sh": "#!/bin/bash\n"},
		"ops:chezmoi":            {"dot_zshrc": "x", "dot_a.tmpl": "x"},
		"ops:flux":               {"flux/kustomization.yaml": "x", "flux/clusters/a/k.yaml": "x"},
		"compose:config":         {"compose.yaml": "services: {}\n"},
		"wit:validate":           {"wit/a.wit": "x", "justfile": "validate:\n  true\n"},
	}
	for id, files := range trees {
		t.Run(id, func(t *testing.T) {
			in, f := toolTree(t, files, func(c Cmd) (string, int) {
				switch {
				case c.Name == "opa" && len(c.Args) > 0 && c.Args[0] == "version":
					return "Version: 1.21.1", 0
				case c.Name == "hadolint" && c.Args[0] == "--version":
					return "Haskell Dockerfile Linter 2.15.1", 0
				}
				return "", 0
			})
			runAtom(t, id, in)
			for _, c := range f.calls {
				if !slices.Contains(Programs, c.Name) {
					t.Errorf("%s exec'd %q, which Programs does not list", id, c.Name)
				}
			}
		})
	}
}
