package diescontracts

import (
	"context"
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

// contractsTree is the policy die's source with what dies:contracts needs.
func contractsTree() map[string]string {
	return atomtest.DieTree(map[string]string{
		checker:             "# checker",
		fixtures:            "# fixtures",
		checks.DoorManifest: "# manifest",
	})
}

// fixtureAnswer is a checker that detects what it should: the nine fixtures
// fail (exit 1) and the four controls pass; the live run answers live.
func fixtureAnswer(live int, liveOut string) func(atoms.Cmd) (string, int) {
	expectFail := map[string]bool{}
	for _, f := range checks.ContractFixtures {
		expectFail[f.Name] = f.ExpectFail
	}
	return func(c atoms.Cmd) (string, int) {
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

func TestProvesDetectionThenTheLiveCheckIsTheVerdict(t *testing.T) {
	in, f := atomtest.UVTree(t, contractsTree(), fixtureAnswer(0, "contracts: every copy agrees"))
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass, "contracts: every copy agrees")
	// The probe, thirteen fixtures, the live run.
	if len(f.Calls) != 15 {
		t.Fatalf("%d calls: %v", len(f.Calls), f.Ran())
	}
	for i, fx := range checks.ContractFixtures {
		atomtest.WantUV(t, f.Calls[i+1], in.Root, dieskit.WithTOML, checker, "--manifest", fixtures, "--contract", fx.Name)
	}
	atomtest.WantUV(t, f.Last(), in.Root, dieskit.WithTOML, checker)
}

func TestSettlesOnTheLiveChecksExitOrTheProof(t *testing.T) {
	for _, tc := range []struct {
		name    string
		answer  func(atoms.Cmd) (string, int)
		state   checks.State
		result  string
		needles []string
	}{
		{"a live check that finds a defect is a finding", fixtureAnswer(1, "copy X lags"), checks.StateFindings, atomtest.Findings, []string{"copy X lags"}},
		{"a live check that could not run is a 2", fixtureAnswer(2, "no manifest"), checks.StateCannotRun, atomtest.Cannot, []string{"no manifest"}},
		{"a fixture the checker no longer detects fails the proof", func(c atoms.Cmd) (string, int) {
			if strings.Contains(strings.Join(c.Args, " "), "--contract lagging") {
				return "", 0
			}
			return fixtureAnswer(0, "")(c)
		}, checks.StateFindings, atomtest.Findings, []string{"the fixtures no longer prove the gate detects", "::error::fixture 'lagging' PASSED"}},
		{"a control the checker invents divergence for fails the proof", func(c atoms.Cmd) (string, int) {
			if strings.Contains(strings.Join(c.Args, " "), "--contract agreeing") {
				return "", 1
			}
			return fixtureAnswer(0, "")(c)
		}, checks.StateFindings, atomtest.Findings, []string{"::error::control 'agreeing' FAILED"}},
		{"a fixture run that would not start never ran", func(c atoms.Cmd) (string, int) {
			if strings.Contains(strings.Join(c.Args, " "), "--contract undeclared") {
				return "uv: gone", -1
			}
			return fixtureAnswer(0, "")(c)
		}, checks.StateCannotRun, atomtest.Cannot, []string{"the undeclared fixture never ran: uv: gone"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, _ := atomtest.UVTree(t, contractsTree(), tc.answer)
			atomtest.Expect(t, check(context.Background(), in), tc.state, tc.result, tc.needles...)
		})
	}
}

func TestRefusesWhatItCannotRunOrFind(t *testing.T) {
	t.Run("a container without uv is a 2 before any fixture", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, contractsTree(), func(atoms.Cmd) (string, int) { return "gone", 127 })
		f.Free = []string{"uv"}
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "exit code: 127", "failed: uv --version")
		if len(f.Calls) != 1 {
			t.Errorf("a fixture ran on an unprovisioned container: %v", f.Ran())
		}
	})
	for path, why := range map[string]string{
		checker:             "tools/check_contracts.py is absent, so there is no checker to run",
		fixtures:            "a gate that cannot prove it detects is a gate that is not there",
		checks.DoorManifest: "contracts/contracts.toml is absent, so there is no live manifest to check",
	} {
		t.Run(path+" missing", func(t *testing.T) {
			files := contractsTree()
			delete(files, path)
			in, f := atomtest.UVTree(t, files, nil)
			atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, why)
			if len(f.Calls) != 0 {
				t.Errorf("python ran with an input missing: %v", f.Ran())
			}
		})
	}
	t.Run("off the die's source it is absent", func(t *testing.T) {
		in, f := atomtest.UVTree(t, map[string]string{"main.go": "x"}, nil)
		atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Absent, "this tree is not the policy die's source")
		if len(f.Calls) != 0 {
			t.Errorf("an absent atom touched %v", f.Ran())
		}
	})
	t.Run("a manifest that will not read never reached the door", func(t *testing.T) {
		files := contractsTree()
		delete(files, checks.DoorManifest)
		files[checks.DoorManifest+"/x"] = "x"
		in, _ := atomtest.UVTree(t, files, fixtureAnswer(0, ""))
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the door reachability probe never ran")
	})
}

func TestProbesTheDoorBeforeTheLiveCheck(t *testing.T) {
	manifest := "[[contracts.a.copies]]\nsource = { repo = \"rob/x\", path = \"a.json\" }\n"
	t.Run("a door nothing answers at is a 2, not a copy that disagrees", func(t *testing.T) {
		files := contractsTree()
		files[checks.DoorManifest] = manifest
		in, f := atomtest.UVTree(t, files, fixtureAnswer(0, ""))
		in.Door = atomtest.DeadDoor(t)
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the door's archive read is unreachable", "A copy that could not be fetched is not a copy that agrees")
		if last := f.Last(); !strings.Contains(strings.Join(last.Args, " "), "--contract") {
			t.Errorf("the live check ran past a dead door: %v", last.Args)
		}
	})
	t.Run("a door that answers lets the live check be the verdict", func(t *testing.T) {
		files := contractsTree()
		files[checks.DoorManifest] = manifest
		in, _ := atomtest.UVTree(t, files, fixtureAnswer(1, "copy X lags"))
		in.Door = atomtest.DoorOf(t, map[string]string{"rob/x a.json": "{}"})
		atomtest.Expect(t, check(context.Background(), in), checks.StateFindings, atomtest.Findings, "copy X lags")
	})
}
