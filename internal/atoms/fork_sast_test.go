package atoms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A FORK WITH NO rules/sast IS SCANNED WITH THE FLEET'S BUILT-IN RULESETS, laid
// in a scratch directory beside the tree, and its findings do not fail it.
func TestFleetOpengrepOnAForkScansWithTheBuiltInRules(t *testing.T) {
	const id = "fleet:opengrep-sast"
	gravity := map[string]string{"Cargo.toml": "[workspace]\n", "cmd/g/main.rs": "fn main() {}\n", "examples/a/z.go": "package a\n"}
	for _, tc := range []struct {
		name   string
		fork   string
		scan   string
		code   int
		state  int
		result string
		needle string
	}{
		{"a fork with findings passes and says so", "gravity", "z.go: ssrf\nRan 8 rules on 90 files: 1 finding.", 1, 0, pass, "ADVISORY"},
		{"a clean fork scan passes", "gravity", "Ran 8 rules on 90 files: 0 findings.", 0, 0, pass, "no findings"},
		{"a fork scan that examined nothing is refused", "gravity", "Ran 8 rules on 0 files: 0 findings.", 0, 2, cannot, "built-in rulesets matched no file"},
		{"a fork whose scan failed could not run", "gravity", "boom", 7, 2, cannot, "boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var laid []string
			in, f := toolTree(t, gravity, func(c Cmd) (string, int) {
				dir := c.Args[2]
				if filepath.Dir(dir) == c.Dir {
					t.Errorf("the rulesets were laid inside the tree: %s", dir)
				}
				ents, _ := os.ReadDir(dir)
				for _, e := range ents {
					laid = append(laid, e.Name())
				}
				return tc.scan, tc.code
			})
			in.Fork = tc.fork
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needle)
			c := f.call("opengrep")
			if got := flagged(c); !strings.HasPrefix(got, "scan --config ") || !strings.HasSuffix(got, " --error .") || strings.Contains(got, "rules/sast") {
				t.Errorf("opengrep was run as %q", got)
			}
			if strings.Join(laid, " ") != "go.yml rust.yml" {
				t.Errorf("rulesets laid: %v", laid)
			}
			if _, err := os.Stat(c.Args[2]); err == nil {
				t.Errorf("the scratch rulesets were left behind: %s", c.Args[2])
			}
		})
	}

	t.Run("a fork holding no covered language is absent and runs nothing", func(t *testing.T) {
		in, f := toolTree(t, map[string]string{"README.md": "x"}, nil)
		in.Fork = "gravity"
		expect(t, runAtom(t, id, in), stateOf(0), absent, "no file in a language")
		if len(f.calls) != 0 {
			t.Errorf("touched %v", f.ran())
		}
	})
	t.Run("a fork that carries rules/sast keeps the blocking scan", func(t *testing.T) {
		in, f := toolTree(t, map[string]string{"rules/sast/a.yml": "rules: []\n", "main.go": "x"}, func(Cmd) (string, int) { return "main.go: injection", 1 })
		in.Fork = "gravity"
		expect(t, runAtom(t, id, in), stateOf(1), findings, "main.go: injection")
		if got := flagged(f.call("opengrep")); got != "scan --config rules/sast --error ." {
			t.Errorf("opengrep was run as %q", got)
		}
	})
	t.Run("not a fork, no rules: unchanged, nothing runs", func(t *testing.T) {
		in, f := toolTree(t, gravity, nil)
		expect(t, runAtom(t, id, in), stateOf(0), absent, "no rules/sast in this tree")
		if len(f.calls) != 0 {
			t.Errorf("touched %v", f.ran())
		}
	})
	t.Run("a fork whose files would not list", func(t *testing.T) {
		in, _ := toolTree(t, gravity, nil)
		in.Fork, in.FilesErr = "gravity", os.ErrPermission
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "would not enumerate")
	})
	t.Run("the scan would not start", func(t *testing.T) {
		in, _ := toolTree(t, gravity, func(Cmd) (string, int) { return "opengrep: not found", -1 })
		in.Fork = "gravity"
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the atom never ran")
	})
}
