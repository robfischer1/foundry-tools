package fleetopengrepsast

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
	"dagger/foundry-tools/internal/atoms"
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

const absent = "absent"

// stateOf is a table's plain int as the catalogue's State.
func stateOf(n int) checks.State { return checks.State(n) }

// missingRoot is an input over a directory that is not there: the atoms that
// open with the root's listing settle 2.
func missingRoot(t *testing.T) atomkit.In {
	t.Helper()
	return atomkit.In{Root: t.TempDir() + "/not-there", Now: time.Now()}
}

// toolFake stands in for the programs on PATH through In.Exec: it answers each
// call from a script, records it, and fails the test for a program the tools
// container or the atom's toolchain does not carry (atoms.Programs, and
// toolchainPrograms: what the toolchain image provides).
var toolchainPrograms []string

type toolFake struct {
	t      *testing.T
	calls  []atoms.Cmd
	answer func(c atoms.Cmd) (string, int)
}

func (f *toolFake) exec(_ context.Context, c atoms.Cmd) (string, int) {
	f.t.Helper()
	if !slices.Contains(atoms.Programs, c.Name) && !slices.Contains(toolchainPrograms, c.Name) {
		f.t.Errorf("exec of %q: no layer of the tools container carries it", c.Name)
	}
	f.calls = append(f.calls, c)
	if f.answer == nil {
		return "", 0
	}
	return f.answer(c)
}

// toolTree is treeIn with the exec seam set to the script.
func toolTree(t *testing.T, files map[string]string, answer func(atoms.Cmd) (string, int)) (atomkit.In, *toolFake) {
	t.Helper()
	in := treeIn(t, files)
	f := &toolFake{t: t, answer: answer}
	in.Exec = f.exec
	return in, f
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
func (f *toolFake) call(name string) atoms.Cmd {
	f.t.Helper()
	for _, c := range f.calls {
		if c.Name == name {
			return c
		}
	}
	f.t.Fatalf("%s was never exec'd; ran %v", name, f.ran())
	return atoms.Cmd{}
}

// flagged is an argv as one string, for a needle.
func flagged(c atoms.Cmd) string { return strings.Join(c.Args, " ") }

const stamped = "_src_path: https://git.notusmi.com/rob/go-repo-template.git\n"

func TestFleetOpengrepSast(t *testing.T) {
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
			in, f := toolTree(t, tc.files, func(atoms.Cmd) (string, int) { return tc.scan, tc.code })
			expect(t, run(t, in), stateOf(tc.state), tc.result, tc.needles...)
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
			expect(t, run(t, in), stateOf(tc.state), tc.result, tc.needles...)
			if len(f.calls) != 0 {
				t.Errorf("an atom that is absent touched %v", f.ran())
			}
		})
	}
	t.Run("the scan's output is trimmed before it is judged and recorded", func(t *testing.T) {
		const line = "Ran 4 rules on 12 files: 0 findings."
		in, _ := toolTree(t, ruled, func(atoms.Cmd) (string, int) { return "\n  " + line + "  \n\n", 0 })
		if v := run(t, in); v.OriginalBytes != len(line) {
			t.Errorf("the record holds %d bytes of output, want the trimmed %d", v.OriginalBytes, len(line))
		}
	})
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		in := missingRoot(t)
		in.Exec = (&toolFake{t: t}).exec
		expect(t, run(t, in), stateOf(2), cannot, "the tree would not enumerate")
	})
	t.Run("rules that is a file will not list", func(t *testing.T) {
		in, _ := toolTree(t, map[string]string{"rules": "x"}, nil)
		expect(t, run(t, in), stateOf(2), cannot, "the tree would not enumerate")
	})
}

// A FORK WITH NO rules/sast IS SCANNED WITH THE FLEET'S BUILT-IN RULESETS, laid
// in a scratch directory beside the tree, and its findings do not fail it.
func TestFleetOpengrepOnAForkScansWithTheBuiltInRules(t *testing.T) {
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
			in, f := toolTree(t, gravity, func(c atoms.Cmd) (string, int) {
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
			expect(t, run(t, in), stateOf(tc.state), tc.result, tc.needle)
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
		expect(t, run(t, in), stateOf(0), absent, "no file in a language")
		if len(f.calls) != 0 {
			t.Errorf("touched %v", f.ran())
		}
	})
	t.Run("a fork that carries rules/sast keeps the blocking scan", func(t *testing.T) {
		in, f := toolTree(t, map[string]string{"rules/sast/a.yml": "rules: []\n", "main.go": "x"}, func(atoms.Cmd) (string, int) { return "main.go: injection", 1 })
		in.Fork = "gravity"
		expect(t, run(t, in), stateOf(1), findings, "main.go: injection")
		if got := flagged(f.call("opengrep")); got != "scan --config rules/sast --error ." {
			t.Errorf("opengrep was run as %q", got)
		}
	})
	t.Run("not a fork, no rules: unchanged, nothing runs", func(t *testing.T) {
		in, f := toolTree(t, gravity, nil)
		expect(t, run(t, in), stateOf(0), absent, "no rules/sast in this tree")
		if len(f.calls) != 0 {
			t.Errorf("touched %v", f.ran())
		}
	})
	t.Run("a fork whose files would not list", func(t *testing.T) {
		in, _ := toolTree(t, gravity, nil)
		in.Fork, in.FilesErr = "gravity", os.ErrPermission
		expect(t, run(t, in), stateOf(2), cannot, "would not enumerate")
	})
	t.Run("the scan would not start", func(t *testing.T) {
		in, _ := toolTree(t, gravity, func(atoms.Cmd) (string, int) { return "opengrep: not found", -1 })
		in.Fork = "gravity"
		expect(t, run(t, in), stateOf(2), cannot, "the atom never ran")
	})
}

// A scratch directory that cannot be made, and a ruleset the disk refuses, are
// could-not-run: the scan never started, and nothing ran opengrep.
func TestFleetOpengrepOnAForkCannotRunWhenTheRulesetsCannotBeLaid(t *testing.T) {
	gravity := map[string]string{"cmd/g/main.rs": "fn main() {}\n"}
	t.Run("no scratch directory", func(t *testing.T) {
		in, f := toolTree(t, gravity, nil)
		in.Fork = "gravity"
		t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "gone"))
		expect(t, run(t, in), stateOf(2), cannot, "could not be written for the run")
		if len(f.calls) != 0 {
			t.Errorf("opengrep ran: %v", f.ran())
		}
	})
	t.Run("a ruleset the disk refuses", func(t *testing.T) {
		in, f := toolTree(t, gravity, nil)
		in.Fork = "gravity"
		orig := writeRuleset
		writeRuleset = func(string, []byte, os.FileMode) error { return os.ErrPermission }
		t.Cleanup(func() { writeRuleset = orig })
		expect(t, run(t, in), stateOf(2), cannot, "could not be written for the run")
		if len(f.calls) != 0 {
			t.Errorf("opengrep ran: %v", f.ran())
		}
	})
}

// A stamped tree without the ruleset names the template, with the name sliced
// exactly: an off-by-one would leave "/go-repo-template".
func TestAStampedTreeWithoutTheRulesetNamesTheTemplate(t *testing.T) {
	in, f := toolTree(t, map[string]string{".copier-answers.yml": "_src_path: https://forgejo.notusmi.com/rob/go-repo-template.git\n"}, nil)
	v := run(t, in)
	expect(t, v, stateOf(2), cannot, "CANNOT RUN", "go-repo-template", "never received the file", "copy go-repo-template's template/rules/sast/dataflow.yml")
	if strings.Contains(v.Reason, "/go-repo-template's") {
		t.Errorf("the template name kept its leading slash:\n%s", v.Reason)
	}
	if len(f.calls) != 0 {
		t.Errorf("an absent atom touched %v", f.ran())
	}
}

// An answers file pointing somewhere that is not one of our repo templates must
// not make the atom stricter; one that will not read stays a pass.
func TestAStampThatIsNotATemplateDoesNotMakeItStricter(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"a non-template":                     {".copier-answers.yml": "_src_path: https://forgejo.notusmi.com/rob/speckit-overrides.git\n"},
		"a template that ships no SAST":      {".copier-answers.yml": "_src_path: https://git.notusmi.com/config-repo-template.git\n"},
		"an answers file that will not read": {".copier-answers.yml/x": "x"},
	} {
		t.Run(name, func(t *testing.T) {
			expect(t, run(t, treeIn(t, files)), stateOf(0), absent, "no SAST-shipping fleet template stamped it")
		})
	}
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
	if Def.Desc != "SAST scan that refuses a zero-file scan." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if !slices.Equal(Def.Needs, []string{"tool:opengrep"}) {
		t.Errorf("needs %v", Def.Needs)
	}
}
