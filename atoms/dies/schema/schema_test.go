package diesschema

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
	"dagger/foundry-tools/internal/checks/scripts"
)

// TestUT_485_TheDeclarationIsTheRowItReplaced: the Def carries the identity the
// catalogue row did, and validates.
func TestUT_485_TheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "python" || Def.Surface != "any:**" || strings.Join(Def.Needs, ",") != "tool:uv" {
		t.Errorf("%+v", Def)
	}
}

func TestWritesTheEmbeddedValidatorRunsItAndRemovesIt(t *testing.T) {
	files := atomtest.DieTree(map[string]string{"schema/fleet-star.schema.json": "{}"})
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	var script string
	in, f := atomtest.UVTree(t, files, func(c atoms.Cmd) (string, int) {
		if last := c.Args[len(c.Args)-1]; strings.HasSuffix(last, ".py") {
			script = last
			body, err := os.ReadFile(script)
			if err != nil || string(body) != scripts.DiesSchema {
				t.Errorf("the script at %s is not the embedded validator: %v", script, err)
			}
			if filepath.Dir(script) != tmp {
				t.Errorf("the script is at %s, not in the temp directory", script)
			}
		}
		return "dies:schema: ok", 0
	})
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass, "dies:schema: ok")
	// The probe, the import probe, the validator.
	if got := strings.Join(f.Ran(), " "); got != "uv uv uv" {
		t.Errorf("ran %s", got)
	}
	atomtest.WantUV(t, f.Calls[1], in.Root, dieskit.WithJSONSchema, "-c", "import jsonschema")
	atomtest.WantUV(t, f.Last(), in.Root, dieskit.WithJSONSchema, script)
	if _, err := os.Stat(script); err == nil {
		t.Error("the validator was left behind")
	}
}

func TestSettlesOnTheValidatorsExit(t *testing.T) {
	files := atomtest.DieTree(map[string]string{"schema/fleet-star.schema.json": "{}"})
	for _, tc := range []struct {
		name    string
		code    int
		state   checks.State
		result  string
		needles []string
	}{
		{"a finding is the validator's exit 1", 1, checks.StateFindings, atomtest.Findings, []string{"slag-v3: a record fails"}},
		{"a validator that could not run is a 2", 2, checks.StateCannotRun, atomtest.Cannot, []string{"slag-v3: a record fails"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, _ := atomtest.UVTree(t, files, func(c atoms.Cmd) (string, int) {
				if c.Args[len(c.Args)-2] == "-c" {
					return "", 0
				}
				return "slag-v3: a record fails", tc.code
			})
			atomtest.Expect(t, check(context.Background(), in), tc.state, tc.result, tc.needles...)
		})
	}
}

func TestRefusesWhatItCannotRunOrFind(t *testing.T) {
	files := atomtest.DieTree(map[string]string{"schema/fleet-star.schema.json": "{}"})
	t.Run("an environment without jsonschema never ran the validator", func(t *testing.T) {
		in, f := atomtest.UVTree(t, files, func(c atoms.Cmd) (string, int) {
			if c.Args[len(c.Args)-2] == "-c" {
				return "ModuleNotFoundError: No module named 'jsonschema'", 1
			}
			return "", 0
		})
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the atom never ran: exit code: 1", "failed: uv run --no-project --quiet --with jsonschema>=4.20 python3 -c import jsonschema", "No module named 'jsonschema'")
		if len(f.Calls) != 2 {
			t.Errorf("the validator ran without its package: %v", f.Ran())
		}
	})
	t.Run("a container without uv never ran it", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, files, func(atoms.Cmd) (string, int) { return "gone", 127 })
		f.Free = []string{"uv"}
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "exit code: 127", "failed: uv --version")
	})
	t.Run("a validator that cannot be written never ran", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TMPDIR", blocker)
		in, _ := atomtest.UVTree(t, files, nil)
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the validator could not be written for the run")
	})
	t.Run("the catalog schema missing is a could-not-run naming it", func(t *testing.T) {
		in, f := atomtest.UVTree(t, atomtest.DieTree(nil), nil)
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot,
			"schema/fleet-star.schema.json is absent, so there is no schema to validate the catalog entries against")
		if len(f.Calls) != 0 {
			t.Errorf("python ran: %v", f.Ran())
		}
	})
	t.Run("off the die's source it is absent", func(t *testing.T) {
		atomtest.Expect(t, check(context.Background(), atomtest.TreeIn(t, map[string]string{"main.go": "x"})), checks.StatePass, atomtest.Absent, "not the policy die's source")
	})
}
