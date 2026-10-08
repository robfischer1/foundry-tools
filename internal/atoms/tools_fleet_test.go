package atoms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

func TestFleetOpengrepSast(t *testing.T) {
	const id = "fleet:opengrep-sast"
	ruled := map[string]string{"rules/sast/a.yml": "rules: []\n", "main.go": "x"}
	for _, tc := range []struct {
		name    string
		files   map[string]string
		scan    string
		code    int
		state   int
		result  string
		needles []string
	}{
		{"a clean scan passes", ruled, "Ran 4 rules on 12 files: 0 findings.", 0, 0, pass, []string{"Ran 4 rules on 12 files"}},
		{"a finding is the tool's exit 1", ruled, "main.go: injection", 1, 1, findings, []string{"main.go: injection"}},
		{"a zero-file scan exits 0 and is refused", ruled, "Ran 4 rules on 0 files: 0 findings.", 0, 2, cannot,
			[]string{"REFUSING a zero-file scan", "Ran 4 rules on 0 files"}},
		{"any other exit is a could-not-run", ruled, "boom", 7, 2, cannot, []string{"boom"}},
		{"a program that would not start never ran", ruled, "opengrep: executable file not found in $PATH", -1, 2, cannot,
			[]string{"the atom never ran: opengrep: executable file not found"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := toolTree(t, tc.files, func(Cmd) (string, int) { return tc.scan, tc.code })
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
			c := f.call("opengrep")
			if got := flagged(c); got != "scan --config rules/sast --error ." {
				t.Errorf("opengrep was run as %q", got)
			}
			if c.Dir != in.Root || !c.Both || !strings.Contains(strings.Join(c.Env, " "), "LANG=C.UTF-8") || !strings.Contains(strings.Join(c.Env, " "), "LC_ALL=C.UTF-8") {
				t.Errorf("opengrep ran in %q (both streams %v) with env %v", c.Dir, c.Both, c.Env)
			}
		})
	}
	for name, tc := range map[string]struct {
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		"no rules is absent when nobody stamped it":       {map[string]string{"main.go": "x"}, 0, absent, []string{"no rules/sast in this tree"}},
		"a stamped tree without the ruleset never had it": {map[string]string{".copier-answers.yml": stamped}, 2, cannot, []string{"go-repo-template stamped it"}},
		"rules without sast is absent too":                {map[string]string{"rules/other.txt": "x"}, 0, absent, nil},
	} {
		t.Run(name+", and no program ran", func(t *testing.T) {
			in, f := toolTree(t, tc.files, nil)
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
			if len(f.calls) != 0 {
				t.Errorf("an atom that is absent touched %v", f.ran())
			}
		})
	}
	t.Run("the scan's output is trimmed before it is judged and recorded", func(t *testing.T) {
		const line = "Ran 4 rules on 12 files: 0 findings."
		in, _ := toolTree(t, ruled, func(Cmd) (string, int) { return "\n  " + line + "  \n\n", 0 })
		if v := runAtom(t, id, in); v.OriginalBytes != len(line) {
			t.Errorf("the record holds %d bytes of output, want the trimmed %d", v.OriginalBytes, len(line))
		}
	})
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		in := missingRoot(t)
		in.Exec = (&toolFake{t: t}).exec
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the tree would not enumerate")
	})
	t.Run("rules that is a file will not list", func(t *testing.T) {
		in, _ := toolTree(t, map[string]string{"rules": "x"}, nil)
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the tree would not enumerate")
	})
}

func TestFleetHadolint(t *testing.T) {
	const id = "fleet:hadolint"
	pinned := "Haskell Dockerfile Linter " + checks.HadolintVersion
	docker := map[string]string{"Dockerfile": "FROM x\n", "img/Containerfile": "FROM y\n"}
	for _, tc := range []struct {
		name     string
		version  string
		vcode    int
		lint     string
		lcode    int
		state    int
		result   string
		needles  []string
		wantLint bool
	}{
		{"a clean lint passes", pinned, 0, "", 0, 0, pass, nil, true},
		{"a finding at warning or above is exit 1", pinned, 0, "Dockerfile:1 DL3006", 1, 1, findings, []string{"DL3006"}, true},
		{"a Dockerfile that does not parse is also exit 1", pinned, 0, "Dockerfile:1 DL1000 unexpected", 1, 1, findings, nil, true},
		{"any other exit is a could-not-run", pinned, 0, "oom", 137, 2, cannot, nil, true},
		{"a hadolint that is not the pinned one", "Haskell Dockerfile Linter 1.0.0", 0, "", 0, 2, cannot,
			[]string{"does not answer \"Haskell Dockerfile Linter " + checks.HadolintVersion + "\"", "A Dockerfile that was never linted"}, false},
		{"a version probe that fails", "hadolint: not found", -1, "", 0, 2, cannot, []string{"the hadolint version probe never ran: hadolint: not found"}, false},
		{"a lint that would not start", pinned, 0, "hadolint: gone", -1, 2, cannot, []string{"the atom never ran: hadolint: gone"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var config string
			in, f := toolTree(t, docker, func(c Cmd) (string, int) {
				if c.Args[0] == "--version" {
					return tc.version, tc.vcode
				}
				// The ruleset must be on disk while hadolint runs.
				body, err := os.ReadFile(c.Args[2])
				if err != nil {
					t.Errorf("--config %q is not readable while hadolint runs: %v", c.Args[2], err)
				}
				config = string(body)
				return tc.lint, tc.lcode
			})
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
			if !tc.wantLint {
				if len(f.calls) != 1 {
					t.Errorf("a failed probe went on to run %v", f.ran())
				}
				return
			}
			c := f.calls[1]
			args := c.Args
			if args[0] != "--no-color" || args[1] != "--config" || strings.Join(args[3:], " ") != "-- Dockerfile img/Containerfile" {
				t.Errorf("hadolint was run as %q", flagged(c))
			}
			if config != checks.HadolintConfig {
				t.Errorf("the ruleset handed over is not the fleet's:\n%s", config)
			}
			if _, err := os.Stat(args[2]); err == nil {
				t.Errorf("the ruleset %s was left behind", args[2])
			}
			if !c.Both || c.Dir != in.Root {
				t.Errorf("hadolint ran in %q (both streams %v)", c.Dir, c.Both)
			}
		})
	}
	t.Run("no Dockerfile is absent, and hadolint is not touched", func(t *testing.T) {
		in, f := toolTree(t, map[string]string{"main.go": "x"}, nil)
		expect(t, runAtom(t, id, in), stateOf(0), absent, "tracks no Dockerfile or Containerfile")
		if len(f.calls) != 0 {
			t.Errorf("an absent atom touched %v", f.ran())
		}
	})
	t.Run("a population that failed is a could-not-run", func(t *testing.T) {
		in, _ := toolTree(t, docker, nil)
		in.FilesErr = errBoom
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the tree would not enumerate")
	})
	t.Run("a ruleset that cannot be written is a could-not-run", func(t *testing.T) {
		t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "gone"))
		in, f := toolTree(t, docker, func(Cmd) (string, int) { return pinned, 0 })
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the fleet's ruleset could not be written")
		if len(f.calls) != 1 {
			t.Errorf("hadolint linted without its ruleset: %v", f.ran())
		}
	})
}
