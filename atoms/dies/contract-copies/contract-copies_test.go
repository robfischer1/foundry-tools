package diescontractcopies

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

// copiesManifest declares one vendored copy in star-a, authority in dies.
const copiesManifest = `[contracts.a]
authority = "dies/a.json"

[[contracts.a.copies]]
name = "dies/a.json"
source = { local = "schema/a.json" }

[[contracts.a.copies]]
name = "star-a/a.json"
source = { repo = "rob/star-a", path = "vendor/a.json" }
`

// copiesDoor serves what the atom fetches.
func copiesDoor(t atomtest.TB) checks.Door {
	answers := map[string]string{}
	for _, p := range checks.ContractCopyFiles() {
		answers[checks.RefusalRepo+" "+p] = "# " + p + "\n"
	}
	answers[checks.RefusalRepo+" "+checks.DoorManifest] = copiesManifest
	answers["rob/star-a vendor/a.json"] = "{}"
	return atomtest.DoorOf(t, answers)
}

func TestGradesTheCopiesATreeHolds(t *testing.T) {
	star := map[string]string{"vendor/a.json": "{}", "main.go": "x"}
	t.Run("a star grades the copy it holds, scoped to itself, with dies' checker off the door", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("TMPDIR", tmp)
		in, f := atomtest.UVTree(t, star, func(c atoms.Cmd) (string, int) {
			// The fetched checker and manifest are where the argv says.
			for _, a := range c.Args {
				if strings.HasSuffix(a, ".py") {
					if body, err := os.ReadFile(a); err != nil || !strings.HasPrefix(string(body), "# tools/check_contracts.py") {
						t.Errorf("the checker at %s: %q, %v", a, body, err)
					}
				}
			}
			return "copies agree", 0
		})
		in.Door = copiesDoor(t)
		atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass, "copies agree")
		last := f.Last()
		args := strings.Join(last.Args, " ")
		for _, want := range []string{"--with " + dieskit.WithTOML, "check_contracts.py", "--manifest", checks.DoorManifest, "--door " + in.Door.Base + "/archive", "--tree " + checks.RefusalTreeFlag("star-a")} {
			if !strings.Contains(args, want) {
				t.Errorf("the checker ran as %q, lacking %q", args, want)
			}
		}
		if last.Dir != in.Root || !last.Both {
			t.Errorf("the checker ran %+v", last)
		}
		if left, _ := os.ReadDir(tmp); len(left) != 0 {
			t.Errorf("the fetched files were left behind: %v", left)
		}
	})
	t.Run("a finding is the checker's exit 1", func(t *testing.T) {
		in, _ := atomtest.UVTree(t, star, func(atoms.Cmd) (string, int) { return "star-a/a.json drifted", 1 })
		in.Door = copiesDoor(t)
		atomtest.Expect(t, check(context.Background(), in), checks.StateFindings, atomtest.Findings, "star-a/a.json drifted")
	})
	t.Run("a container without uv never ran the checker", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, star, func(atoms.Cmd) (string, int) { return "gone", 127 })
		f.Free = []string{"uv"}
		in.Door = copiesDoor(t)
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "exit code: 127", "failed: uv --version")
	})
	t.Run("foundry-dies grades every copy whole under dies:contracts", func(t *testing.T) {
		in, f := atomtest.UVTree(t, atomtest.DieTree(nil), nil)
		atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Absent, "this is foundry-dies")
		if len(f.Calls) != 0 {
			t.Errorf("an absent atom touched %v", f.Ran())
		}
	})
	t.Run("a tree that holds none of the copies is absent", func(t *testing.T) {
		in, f := atomtest.UVTree(t, map[string]string{"main.go": "x"}, nil)
		in.Door = copiesDoor(t)
		atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Absent, "holds none of the copies contracts.toml declares")
		if len(f.Calls) != 0 {
			t.Errorf("uv ran for a tree with no copy: %v", f.Ran())
		}
	})
}

func TestRefusesWhatTheDoorCannotAnswer(t *testing.T) {
	star := map[string]string{"vendor/a.json": "{}", "main.go": "x"}
	for _, tc := range []struct {
		name    string
		door    func(atomtest.TB) checks.Door
		needles []string
	}{
		{"a door nothing answers at is a 2 even where there is nothing to grade", atomtest.DeadDoor, []string{"the door's archive read is unreachable", "A manifest that could not be fetched is not a tree with no copies"}},
		{"a door that does not hold the checker", func(t atomtest.TB) checks.Door { return atomtest.DoorOf(t, nil) }, []string{"the door answered HTTP 404", "the checker's inputs are not whole"}},
		{"a manifest that does not parse", func(t atomtest.TB) checks.Door {
			answers := map[string]string{}
			for _, p := range checks.ContractCopyFiles() {
				answers[checks.RefusalRepo+" "+p] = "x"
			}
			answers[checks.RefusalRepo+" "+checks.DoorManifest] = "[[["
			return atomtest.DoorOf(t, answers)
		}, []string{"the manifest would not parse"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := atomtest.UVTree(t, star, nil)
			in.Door = tc.door(t)
			atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, tc.needles...)
			if len(f.Calls) != 0 {
				t.Errorf("uv ran: %v", f.Ran())
			}
		})
	}
	t.Run("a door that fails mid-way through the copy of the manifest", func(t *testing.T) {
		in, _ := atomtest.UVTree(t, star, nil)
		in.Door = atomtest.DoorBreaking(t, map[string]string{
			checks.RefusalRepo + " tools/check_contracts.py": "x",
			checks.RefusalRepo + " tools/schema_stamp.py":    "x",
		}, checks.RefusalRepo+" "+checks.DoorManifest)
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the door's archive read is unreachable", checks.DoorManifest)
	})
	t.Run("a door that answers the manifest and then goes away is the probe's 2", func(t *testing.T) {
		in, f := atomtest.UVTree(t, star, nil)
		in.Door = atomtest.DoorBreaking(t, map[string]string{
			checks.RefusalRepo + " tools/check_contracts.py": "x",
			checks.RefusalRepo + " tools/schema_stamp.py":    "x",
			checks.RefusalRepo + " " + checks.DoorManifest:   copiesManifest,
		}, "rob/star-a vendor/a.json")
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the door's archive read is unreachable, so the authority cannot be read")
		if len(f.Calls) != 0 {
			t.Errorf("uv ran past the probe: %v", f.Ran())
		}
	})
	t.Run("the fetched files cannot be placed", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TMPDIR", blocker)
		in, _ := atomtest.UVTree(t, star, nil)
		in.Door = copiesDoor(t)
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the checker's inputs could not be placed")
	})
	t.Run("a shape that could not be read is not an answer about its copies", func(t *testing.T) {
		in := atomtest.MissingRoot(t)
		in.Door = copiesDoor(t)
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the repository root could not be read")
	})
}
