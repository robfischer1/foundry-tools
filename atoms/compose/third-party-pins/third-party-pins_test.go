package composethirdpartypins

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

func TestComposeThirdPartyPins(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		{"no compose spec is absent", map[string]string{"a.yml": "image: x"}, 0, atomtest.Absent, []string{"tracks no compose.yaml/compose.yml"}},
		{"no interpolation keeps the ratchet closed", map[string]string{"compose.yaml": "image: x:1\n"}, 0, atomtest.Pass, []string{"the pin era stays closed"}},
		{"an image interpolation is a red merge with its line", map[string]string{"compose.yaml": "services:\n  a:\n    image: r/x:${PIN_X}\n"}, 1, atomtest.Findings,
			[]string{"1 ${PIN_} interpolation(s) found", "compose.yaml:3:    image: r/x:${PIN_X}"}},
		{"a pin under .forgejo is not scanned", map[string]string{"compose.yaml": "x", ".forgejo/w.yml": "image: ${PIN_X}"}, 0, atomtest.Pass, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			atomtest.Expect(t, run(atomtest.TreeIn(t, tc.files)), atomtest.StateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a body that will not read is a ratchet not counted", func(t *testing.T) {
		in := atomtest.TreeIn(t, map[string]string{"compose.yaml": "x"})
		in.Committable = append(in.Committable, "gone.yml") // listed, and not on disk
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "could not read gone.yml", "closed ratchet on a scan that did not run")
	})
	t.Run("a scan that failed", func(t *testing.T) {
		in := atomtest.TreeIn(t, map[string]string{"compose.yaml": "x"})
		in.FilesErr = atomtest.ErrBoom
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the compose-surface scan itself failed")
	})
}

// A workflow file may legitimately MENTION ${PIN_} while describing the era that
// closed; the gate is about what the stack DEPLOYS.
func TestComposeThirdPartyPinsReportsFileLineAndText(t *testing.T) {
	in := atomtest.TreeIn(t, map[string]string{"compose.yaml": "services:\n  x:\n    image: ${PIN_CADDY}\n"})
	v := run(in)
	atomtest.Expect(t, v, atomtest.StateOf(1), atomtest.Findings, "1 ${PIN_} interpolation(s) found", "the pin era does not reopen",
		"compose.yaml:3:    image: ${PIN_CADDY}")
}

func TestComposeThirdPartyPinsIgnoresForgejoAndPassesClean(t *testing.T) {
	in := atomtest.TreeIn(t, map[string]string{"compose.yaml": "x", ".forgejo/workflows/ci.yml": "# the pin era: image: ${PIN_OLD}\n"})
	atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass)
}
