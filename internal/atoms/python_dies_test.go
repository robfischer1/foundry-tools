package atoms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/scripts"
)

// contractsTree is the policy die's source with what dies:contracts needs.
func contractsTree() map[string]string {
	return dieTree(map[string]string{
		contractsChecker:    "# checker",
		contractsFixtures:   "# fixtures",
		checks.DoorManifest: "# manifest",
	})
}

// fixtureAnswer is a checker that detects what it should: the nine fixtures
// fail (exit 1) and the four controls pass; the live run answers live.
func fixtureAnswer(live int, liveOut string) func(Cmd) (string, int) {
	expectFail := map[string]bool{}
	for _, f := range checks.ContractFixtures {
		expectFail[f.Name] = f.ExpectFail
	}
	return func(c Cmd) (string, int) {
		for i, a := range c.Args {
			if a == "--contract" {
				if expectFail[c.Args[i+1]] {
					return "fixture: refused", 1
				}
				return "", 0
			}
		}
		return liveOut, live
	}
}

func TestDiesContracts(t *testing.T) {
	const id = "dies:contracts"
	t.Run("the fixtures are proved to detect, then the live check is the verdict", func(t *testing.T) {
		in, f := pyTree(t, contractsTree(), fixtureAnswer(0, "contracts: every copy agrees"))
		expect(t, runAtom(t, id, in), stateOf(0), pass, "contracts: every copy agrees")
		// The probe, thirteen fixtures, the live run.
		if len(f.calls) != 15 {
			t.Fatalf("%d calls: %v", len(f.calls), f.ran())
		}
		for i, fx := range checks.ContractFixtures {
			wantPython(t, f.calls[i+1], in.Root, contractsChecker, "--manifest", contractsFixtures, "--contract", fx.Name)
		}
		wantPython(t, f.lastCall(t), in.Root, contractsChecker)
	})
	for _, tc := range []struct {
		name    string
		answer  func(Cmd) (string, int)
		state   int
		result  string
		needles []string
	}{
		{"a live check that finds a defect is a finding", fixtureAnswer(1, "copy X lags"), 1, findings, []string{"copy X lags"}},
		{"a live check that could not run is a 2", fixtureAnswer(2, "no manifest"), 2, cannot, []string{"no manifest"}},
		{"a fixture the checker no longer detects fails the proof", func(c Cmd) (string, int) {
			if strings.Contains(strings.Join(c.Args, " "), "--contract lagging") {
				return "", 0
			}
			return fixtureAnswer(0, "")(c)
		}, 1, findings, []string{"the fixtures no longer prove the gate detects", "::error::fixture 'lagging' PASSED"}},
		{"a control the checker invents divergence for fails the proof", func(c Cmd) (string, int) {
			if strings.Contains(strings.Join(c.Args, " "), "--contract agreeing") {
				return "", 1
			}
			return fixtureAnswer(0, "")(c)
		}, 1, findings, []string{"::error::control 'agreeing' FAILED"}},
		{"a fixture run that would not start never ran", func(c Cmd) (string, int) {
			if strings.Contains(strings.Join(c.Args, " "), "--contract undeclared") {
				return "python3: gone", -1
			}
			return fixtureAnswer(0, "")(c)
		}, 2, cannot, []string{"the undeclared fixture never ran: python3: gone"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, _ := pyTree(t, contractsTree(), tc.answer)
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a container without the venv is a 2 before any fixture", func(t *testing.T) {
		in, f := toolTree(t, contractsTree(), func(Cmd) (string, int) { return "gone", 127 })
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "python3 --version exited 127")
		if len(f.calls) != 1 {
			t.Errorf("a fixture ran on an unprovisioned container: %v", f.ran())
		}
	})
	for path, why := range map[string]string{
		contractsChecker:    "tools/check_contracts.py is absent, so there is no checker to run",
		contractsFixtures:   "a gate that cannot prove it detects is a gate that is not there",
		checks.DoorManifest: "contracts/contracts.toml is absent, so there is no live manifest to check",
	} {
		t.Run(path+" missing", func(t *testing.T) {
			files := contractsTree()
			delete(files, path)
			in, f := pyTree(t, files, nil)
			expect(t, runAtom(t, id, in), stateOf(2), cannot, why)
			if len(f.calls) != 0 {
				t.Errorf("python ran with an input missing: %v", f.ran())
			}
		})
	}
	t.Run("off the die's source it is absent", func(t *testing.T) {
		in, f := pyTree(t, map[string]string{"main.go": "x"}, nil)
		expect(t, runAtom(t, id, in), stateOf(0), absent, "this tree is not the policy die's source")
		if len(f.calls) != 0 {
			t.Errorf("an absent atom touched %v", f.ran())
		}
	})
	t.Run("a manifest that will not read never reached the door", func(t *testing.T) {
		files := contractsTree()
		delete(files, checks.DoorManifest)
		files[checks.DoorManifest+"/x"] = "x"
		in, _ := pyTree(t, files, fixtureAnswer(0, ""))
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the door reachability probe never ran")
	})
	manifest := "[[contracts.a.copies]]\nsource = { repo = \"rob/x\", path = \"a.json\" }\n"
	t.Run("a door nothing answers at is a 2, not a copy that disagrees", func(t *testing.T) {
		files := contractsTree()
		files[checks.DoorManifest] = manifest
		in, f := pyTree(t, files, fixtureAnswer(0, ""))
		in.Door = deadDoor(t)
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the door's archive read is unreachable", "A copy that could not be fetched is not a copy that agrees")
		if last := f.lastCall(t); len(last.Args) == 1 {
			t.Errorf("the live check ran past a dead door: %v", last.Args)
		}
	})
	t.Run("a door that answers lets the live check be the verdict", func(t *testing.T) {
		files := contractsTree()
		files[checks.DoorManifest] = manifest
		in, _ := pyTree(t, files, fixtureAnswer(1, "copy X lags"))
		in.Door = doorOf(t, map[string]string{"rob/x a.json": "{}"})
		expect(t, runAtom(t, id, in), stateOf(1), findings, "copy X lags")
	})
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

// copiesDoor serves what dies:contract-copies fetches.
func copiesDoor(t *testing.T) checks.Door {
	answers := map[string]string{}
	for _, p := range checks.ContractCopyFiles() {
		answers[checks.RefusalRepo+" "+p] = "# " + p + "\n"
	}
	answers[checks.RefusalRepo+" "+checks.DoorManifest] = copiesManifest
	answers["rob/star-a vendor/a.json"] = "{}"
	return doorOf(t, answers)
}

func TestDiesContractCopies(t *testing.T) {
	const id = "dies:contract-copies"
	star := map[string]string{"vendor/a.json": "{}", "main.go": "x"}
	t.Run("a star grades the copy it holds, scoped to itself, with dies' checker off the door", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("TMPDIR", tmp)
		in, f := pyTree(t, star, func(c Cmd) (string, int) {
			// The fetched checker and manifest are where the argv says.
			for _, a := range c.Args[:3] {
				if strings.HasSuffix(a, ".py") {
					if body, err := os.ReadFile(a); err != nil || !strings.HasPrefix(string(body), "# tools/check_contracts.py") {
						t.Errorf("the checker at %s: %q, %v", a, body, err)
					}
				}
			}
			return "copies agree", 0
		})
		in.Door = copiesDoor(t)
		expect(t, runAtom(t, id, in), stateOf(0), pass, "copies agree")
		args := strings.Join(f.lastCall(t).Args, " ")
		for _, want := range []string{"check_contracts.py", "--manifest", checks.DoorManifest, "--door " + in.Door.Base + "/archive", "--tree " + checks.RefusalTreeFlag("star-a")} {
			if !strings.Contains(args, want) {
				t.Errorf("the checker ran as %q, lacking %q", args, want)
			}
		}
		if f.lastCall(t).Dir != in.Root || !f.lastCall(t).Both {
			t.Errorf("the checker ran %+v", f.lastCall(t))
		}
		if left, _ := os.ReadDir(tmp); len(left) != 0 {
			t.Errorf("the fetched files were left behind: %v", left)
		}
	})
	t.Run("a finding is the checker's exit 1", func(t *testing.T) {
		in, _ := pyTree(t, star, func(Cmd) (string, int) { return "star-a/a.json drifted", 1 })
		in.Door = copiesDoor(t)
		expect(t, runAtom(t, id, in), stateOf(1), findings, "star-a/a.json drifted")
	})
	t.Run("a container without the venv never ran the checker", func(t *testing.T) {
		in, _ := toolTree(t, star, func(Cmd) (string, int) { return "gone", 127 })
		in.Door = copiesDoor(t)
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "python3 --version exited 127")
	})
	t.Run("foundry-dies grades every copy whole under dies:contracts", func(t *testing.T) {
		in, f := pyTree(t, contractsTree(), nil)
		expect(t, runAtom(t, id, in), stateOf(0), absent, "this is foundry-dies")
		if len(f.calls) != 0 {
			t.Errorf("an absent atom touched %v", f.ran())
		}
	})
	t.Run("a tree that holds none of the copies is absent", func(t *testing.T) {
		in, f := pyTree(t, map[string]string{"main.go": "x"}, nil)
		in.Door = copiesDoor(t)
		expect(t, runAtom(t, id, in), stateOf(0), absent, "holds none of the copies contracts.toml declares")
		if len(f.calls) != 0 {
			t.Errorf("python ran for a tree with no copy: %v", f.ran())
		}
	})
	for _, tc := range []struct {
		name    string
		door    func(*testing.T) checks.Door
		needles []string
	}{
		{"a door nothing answers at is a 2 even where there is nothing to grade", deadDoor, []string{"the door's archive read is unreachable", "A manifest that could not be fetched is not a tree with no copies"}},
		{"a door that does not hold the checker", func(t *testing.T) checks.Door { return doorOf(t, nil) }, []string{"the door answered HTTP 404", "the checker's inputs are not whole"}},
		{"a manifest that does not parse", func(t *testing.T) checks.Door {
			answers := map[string]string{}
			for _, p := range checks.ContractCopyFiles() {
				answers[checks.RefusalRepo+" "+p] = "x"
			}
			answers[checks.RefusalRepo+" "+checks.DoorManifest] = "[[["
			return doorOf(t, answers)
		}, []string{"the manifest would not parse"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := pyTree(t, star, nil)
			in.Door = tc.door(t)
			expect(t, runAtom(t, id, in), stateOf(2), cannot, tc.needles...)
			if len(f.calls) != 0 {
				t.Errorf("python ran: %v", f.ran())
			}
		})
	}
	t.Run("a door that fails mid-way through the copy of the manifest", func(t *testing.T) {
		in, _ := pyTree(t, star, nil)
		in.Door = doorBreaking(t, map[string]string{
			checks.RefusalRepo + " tools/check_contracts.py": "x",
			checks.RefusalRepo + " tools/schema_stamp.py":    "x",
		}, checks.RefusalRepo+" "+checks.DoorManifest)
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the door's archive read is unreachable", checks.DoorManifest)
	})
	t.Run("a door that answers the manifest and then goes away is the probe's 2", func(t *testing.T) {
		in, f := pyTree(t, star, nil)
		in.Door = doorBreaking(t, map[string]string{
			checks.RefusalRepo + " tools/check_contracts.py": "x",
			checks.RefusalRepo + " tools/schema_stamp.py":    "x",
			checks.RefusalRepo + " " + checks.DoorManifest:   copiesManifest,
		}, "rob/star-a vendor/a.json")
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the door's archive read is unreachable, so the authority cannot be read")
		if len(f.calls) != 0 {
			t.Errorf("python ran past the probe: %v", f.ran())
		}
	})
	t.Run("the fetched files cannot be placed", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TMPDIR", blocker)
		in, _ := pyTree(t, star, nil)
		in.Door = copiesDoor(t)
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the checker's inputs could not be placed")
	})
	t.Run("a shape that could not be read is not an answer about its copies", func(t *testing.T) {
		in, _ := pyTree(t, star, nil)
		in.Door = copiesDoor(t)
		expect(t, runAtom(t, id, missingRootWith(in)), stateOf(2), cannot, "the repository root could not be read")
	})
}

// missingRootWith is the input over a root that is not there, keeping what else
// the input carries (its door, its seam).
func missingRootWith(in Input) Input {
	in.Root = filepath.Join(in.Root, "not-there")
	return in
}

func TestDiesSchema(t *testing.T) {
	const id = "dies:schema"
	files := dieTree(map[string]string{"schema/slag.schema.json": "{}", "schema/slag-v3.schema.json": "{}"})
	t.Run("the embedded validator is written for the run, run with python3, and removed", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("TMPDIR", tmp)
		var script string
		in, f := pyTree(t, files, func(c Cmd) (string, int) {
			if len(c.Args) == 1 && strings.HasSuffix(c.Args[0], ".py") {
				script = c.Args[0]
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
		expect(t, runAtom(t, id, in), stateOf(0), pass, "dies:schema: ok")
		// The probe, the import probe, the validator.
		if got := strings.Join(f.ran(), " "); got != "python3 python3 python3" {
			t.Errorf("ran %s", got)
		}
		wantPython(t, f.calls[1], in.Root, "-c", "import jsonschema")
		wantPython(t, f.lastCall(t), in.Root, script)
		if _, err := os.Stat(script); err == nil {
			t.Error("the validator was left behind")
		}
	})
	for _, tc := range []struct {
		name    string
		code    int
		state   int
		result  string
		needles []string
	}{
		{"a finding is the validator's exit 1", 1, 1, findings, []string{"slag-v3: a record fails"}},
		{"a validator that could not run is a 2", 2, 2, cannot, []string{"slag-v3: a record fails"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, _ := pyTree(t, files, func(c Cmd) (string, int) {
				if c.Args[0] == "-c" {
					return "", 0
				}
				return "slag-v3: a record fails", tc.code
			})
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a venv without jsonschema never ran the validator", func(t *testing.T) {
		in, f := pyTree(t, files, func(c Cmd) (string, int) {
			if c.Args[0] == "-c" {
				return "ModuleNotFoundError: No module named 'jsonschema'", 1
			}
			return "", 0
		})
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the atom never ran: python3 -c import jsonschema exited 1", "No module named 'jsonschema'")
		if len(f.calls) != 2 {
			t.Errorf("the validator ran without its package: %v", f.ran())
		}
	})
	t.Run("a container without python never ran it", func(t *testing.T) {
		in, _ := toolTree(t, files, func(Cmd) (string, int) { return "gone", 127 })
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "python3 --version exited 127")
	})
	t.Run("a validator that cannot be written never ran", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TMPDIR", blocker)
		in, _ := pyTree(t, files, nil)
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the validator could not be written for the run")
	})
	for path, why := range map[string]string{
		"schema/slag.schema.json":    "schema/slag.schema.json is absent, so there is no payload to validate",
		"schema/slag-v3.schema.json": "schema/slag-v3.schema.json is absent, so there is no schema to validate the records against",
	} {
		t.Run(path+" missing", func(t *testing.T) {
			f := dieTree(map[string]string{"schema/slag.schema.json": "{}", "schema/slag-v3.schema.json": "{}"})
			delete(f, path)
			in, fake := pyTree(t, f, nil)
			expect(t, runAtom(t, id, in), stateOf(2), cannot, why)
			if len(fake.calls) != 0 {
				t.Errorf("python ran: %v", fake.ran())
			}
		})
	}
	t.Run("off the die's source it is absent", func(t *testing.T) {
		expect(t, runAtom(t, id, treeIn(t, map[string]string{"main.go": "x"})), stateOf(0), absent, "not the policy die's source")
	})
}

// The four atoms whose checker is the tree's own, and the file each needs
// beside the die's shape.
func TestDiesCheckers(t *testing.T) {
	for _, tc := range []struct {
		id      string
		checker string
		extra   map[string]string
	}{
		{"dies:findings", "tools/check_findings.py", map[string]string{"schema/findings.schema.json": "{}"}},
		{"dies:schemas", "tools/check_schemas.py", nil},
		{"dies:wit-regenerated", "tools/check_wit_regenerated.py", nil},
		{"dies:schema-rendered", "tools/check_schema_rendered.py", nil},
		{"dies:wire-grammar", "tools/check_wire_grammar.py", nil},
	} {
		t.Run(tc.id, func(t *testing.T) {
			files := dieTree(tc.extra)
			files[tc.checker] = "# checker"
			for _, c := range []struct {
				name    string
				code    int
				state   int
				result  string
				needles []string
			}{
				{"a clean tree", 0, 0, pass, []string{"checked ok"}},
				{"a finding is the checker's exit 1, unmapped", 1, 1, findings, []string{"checked ok"}},
				{"a check that could not run is its exit 2, unmapped", 2, 2, cannot, []string{"checked ok"}},
				{"a python that would not start never ran", -1, 2, cannot, []string{"the atom never ran: checked ok"}},
			} {
				t.Run(c.name, func(t *testing.T) {
					in, f := pyTree(t, files, func(Cmd) (string, int) { return "checked ok", c.code })
					expect(t, runAtom(t, tc.id, in), stateOf(c.state), c.result, c.needles...)
					wantPython(t, f.lastCall(t), in.Root, tc.checker)
				})
			}
			t.Run("a container without python never ran the checker", func(t *testing.T) {
				in, f := toolTree(t, files, func(Cmd) (string, int) { return "gone", 127 })
				expect(t, runAtom(t, tc.id, in), stateOf(2), cannot, "python3 --version exited 127")
				if len(f.calls) != 1 {
					t.Errorf("the checker ran: %v", f.ran())
				}
			})
			t.Run("the checker missing is a could-not-run naming it", func(t *testing.T) {
				missing := dieTree(tc.extra)
				in, f := pyTree(t, missing, nil)
				expect(t, runAtom(t, tc.id, in), stateOf(2), cannot, tc.checker+" is absent, so there is no checker to run")
				if len(f.calls) != 0 {
					t.Errorf("python ran: %v", f.ran())
				}
			})
			t.Run("off the die's source it is absent", func(t *testing.T) {
				in, f := pyTree(t, map[string]string{tc.checker: "x"}, nil)
				expect(t, runAtom(t, tc.id, in), stateOf(0), absent, "not the policy die's source")
				if len(f.calls) != 0 {
					t.Errorf("an absent atom touched %v", f.ran())
				}
			})
		})
	}
	t.Run("dies:findings also needs its schema", func(t *testing.T) {
		files := dieTree(map[string]string{"tools/check_findings.py": "x"})
		in, _ := pyTree(t, files, nil)
		expect(t, runAtom(t, "dies:findings", in), stateOf(2), cannot, "schema/findings.schema.json is absent, so there is no schema to validate")
	})
}
