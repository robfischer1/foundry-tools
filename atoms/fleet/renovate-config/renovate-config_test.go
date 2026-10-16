package fleetrenovateconfig

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

// flagged is an argv as one string, for a needle.
func flagged(c atoms.Cmd) string { return strings.Join(c.Args, " ") }

func init() { toolchainPrograms = []string{"renovate", "renovate-config-validator"} }

const configBody = "module.exports = {\n  platform: 'forgejo',\n};\n"

// renovateTree is toolTree with `renovate --version` answering the deployed tag
// unless the script says otherwise.
func renovateTree(t *testing.T, files map[string]string, answer func(atoms.Cmd) (string, int)) (atomkit.In, *toolFake) {
	t.Helper()
	return toolTree(t, files, func(c atoms.Cmd) (string, int) {
		if c.Name == "renovate" && answer == nil {
			return checks.RenovateTag, 0
		}
		if answer == nil {
			return "", 0
		}
		return answer(c)
	})
}

// THE VALIDATOR IS THE DEPLOYED IMAGE'S OWN: the pinned renovate image, strict, on config.js.
func TestRenovateConfigRunsTheDeployedImagesValidatorStrict(t *testing.T) {
	in, f := renovateTree(t, map[string]string{"config.js": configBody}, nil)
	expect(t, run(t, in), stateOf(0), pass)
	probe, validate := f.calls[0], f.calls[1]
	if probe.Name != "renovate" || flagged(probe) != "--version" || probe.Dir != in.Root {
		t.Errorf("the version probe: %+v", probe)
	}
	if validate.Name != "renovate-config-validator" || flagged(validate) != "--strict config.js" || validate.Dir != in.Root || !validate.Both {
		t.Errorf("the validator: %+v", validate)
	}
	if len(f.calls) != 2 {
		t.Errorf("ran %v", f.ran())
	}
}

// 0 valid, 1 invalid, anything else could not run: the validator's exit code is
// the verdict.
func TestRenovateConfigPassesTheValidatorsExitCodeThrough(t *testing.T) {
	for code, want := range map[int]int{0: 0, 1: 1, 2: 2} {
		in, _ := renovateTree(t, map[string]string{"config.js": configBody}, func(c atoms.Cmd) (string, int) {
			if c.Name == "renovate" {
				return checks.RenovateTag, 0
			}
			return "validator said so", code
		})
		if v := run(t, in); v.State != want {
			t.Errorf("exit %d settled %d: %s", code, v.State, v.Reason)
		}
	}
}

// An image that answers another version is not the deployed validator.
func TestRenovateConfigCannotRunOnAnImageOfAnotherVersion(t *testing.T) {
	in, f := renovateTree(t, map[string]string{"config.js": configBody}, func(atoms.Cmd) (string, int) { return "42.99.0", 0 })
	expect(t, run(t, in), stateOf(2), cannot, "CANNOT RUN", "42.99.0", checks.RenovateTag)
	if len(f.calls) != 1 {
		t.Errorf("the validator ran in the wrong image: %v", f.ran())
	}
}

func TestRenovateConfigIsAbsentWithoutAConfigJS(t *testing.T) {
	in, f := renovateTree(t, map[string]string{"main.go": "x"}, nil)
	expect(t, run(t, in), stateOf(0), absent, "ABSENT", "config.js")
	if len(f.calls) != 0 {
		t.Errorf("an absent atom touched %v", f.ran())
	}
}

// A config.js that is not a Renovate global config is some other repo's file.
func TestRenovateConfigIsAbsentOnAConfigJSThatIsNotRenovates(t *testing.T) {
	in, f := renovateTree(t, map[string]string{"config.js": "export default {};\n"}, nil)
	expect(t, run(t, in), stateOf(0), absent, "ABSENT", "not a Renovate")
	if len(f.calls) != 0 {
		t.Errorf("an absent atom touched %v", f.ran())
	}
}

func TestRenovateConfigRefusesATreeItCannotEnumerate(t *testing.T) {
	expect(t, run(t, missingRoot(t)), stateOf(2), cannot, "CANNOT RUN", "would not enumerate")
}

// A config.js that will not read is unknown, not absent and not valid.
func TestRenovateConfigCannotRunWhenConfigJSWillNotRead(t *testing.T) {
	in, _ := renovateTree(t, map[string]string{"config.js/x": "x"}, nil)
	expect(t, run(t, in), stateOf(2), cannot, "CANNOT RUN", "config.js could not be read")
}

// An image that will not run its version probe never graded anything, and a
// validator that will not start never ran.
func TestRenovateConfigNeverRanWhenTheImageWillNotAnswer(t *testing.T) {
	in, _ := renovateTree(t, map[string]string{"config.js": configBody}, func(atoms.Cmd) (string, int) { return "renovate: cannot execute", -1 })
	expect(t, run(t, in), stateOf(2), cannot, "the atom never ran", "cannot execute")
	in, _ = renovateTree(t, map[string]string{"config.js": configBody}, func(c atoms.Cmd) (string, int) {
		if c.Name == "renovate" {
			return checks.RenovateTag, 0
		}
		return "renovate-config-validator: gone", -1
	})
	expect(t, run(t, in), stateOf(2), cannot, "the atom never ran", "validator: gone")
}

// The declaration is the catalogue row it replaced: the same stage, group, text and
// fork lane, and the needs the split table gives it.
func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "renovate" || Def.NotOnForks != "" || Def.SubsumedBy != "" || Def.NeedsManifest {
		t.Errorf("%+v", Def)
	}
	if Def.Desc != "A root config.js that is a Renovate global config passes renovate-config-validator --strict, run from the Renovate image the fleet deploys." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if !slices.Equal(Def.Needs, nil) {
		t.Errorf("needs %v", Def.Needs)
	}
}
