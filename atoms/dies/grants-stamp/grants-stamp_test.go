package diesgrantsstamp

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
)

// TestUT_485_TheDeclarationIsTheRowItReplaced: the Def carries the identity the
// catalogue row did, and validates.
func TestUT_485_TheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrepush || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "python" || Def.Surface != "any:**" || strings.Join(Def.Needs, ",") != "tool:git,tool:uv" {
		t.Errorf("%+v", Def)
	}
}

// gitIn runs git in dir and answers its trimmed stdout; a failure is fatal.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// commitAll stages everything and commits, answering the new sha.
func commitAll(t *testing.T, dir, msg string) string {
	t.Helper()
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", msg)
	return gitIn(t, dir, "rev-parse", "HEAD")
}

// dies:grants-stamp hands the tree's checker the pull's MERGE BASE, not main's tip,
// so a pull that never touched the rendered documents is not refused because main
// rendered after it branched. The checker's four refusals (tx not risen,
// contracts_tx lowered, split tx, wrong rendered_from) are graded where it lives,
// foundry-dies tests/grants_stamp; this holds the seam: the argv, the base, and the
// exit passed through.
func TestHandsTheCheckerTheMergeBase(t *testing.T) {
	files := atomtest.DieTree(map[string]string{checker: "# checker", "policy/grants/data.json": "{}"})
	// repo commits files, then a branch point B, a pull commit C on main and a
	// later main commit D on side; the run is at C with main's tip D as the base.
	repo := func(t *testing.T) (in atomkit.In, f *atomtest.Fake, branchPoint, tip string) {
		in, f = atomtest.UVTree(t, files, func(atoms.Cmd) (string, int) { return "checked ok", 0 })
		t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
		t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
		gitIn(t, in.Root, "init", "-q", "-b", "main")
		gitIn(t, in.Root, "config", "user.email", "t@example.invalid")
		gitIn(t, in.Root, "config", "user.name", "t")
		gitIn(t, in.Root, "config", "commit.gpgsign", "false")
		branchPoint = commitAll(t, in.Root, "branch point")
		gitIn(t, in.Root, "checkout", "-q", "-b", "side")
		atomtest.Put(t, in.Root, "policy/grants/data.json", `{"tx": 12}`)
		tip = commitAll(t, in.Root, "main renders again")
		gitIn(t, in.Root, "checkout", "-q", "main")
		atomtest.Put(t, in.Root, "README.md", "the pull")
		commitAll(t, in.Root, "the pull")
		return in, f, branchPoint, tip
	}

	t.Run("a base is judged at the merge base, not at the tip", func(t *testing.T) {
		in, f, branchPoint, tip := repo(t)
		in.Base = tip
		atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass, "checked ok")
		atomtest.WantUV(t, f.Last(), in.Root, "", checker, "--base", branchPoint)
	})
	t.Run("no base still runs the tree half", func(t *testing.T) {
		in, f, _, _ := repo(t)
		atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass, "checked ok")
		atomtest.WantUV(t, f.Last(), in.Root, "", checker)
	})
	t.Run("the checker's exit is the verdict, unmapped", func(t *testing.T) {
		for code, c := range map[int]struct {
			state  checks.State
			result string
		}{1: {checks.StateFindings, atomtest.Findings}, 2: {checks.StateCannotRun, atomtest.Cannot}} {
			in, _, _, tip := repo(t)
			in.Base = tip
			in.Exec = (&atomtest.Fake{T: t, Free: []string{"uv"}, Answer: func(cmd atoms.Cmd) (string, int) {
				if cmd.Name == "uv" && len(cmd.Args) == 1 {
					return "uv 0.12.13", 0
				}
				return "refused: themis grants render is the only writer", code
			}}).Exec
			atomtest.Expect(t, check(context.Background(), in), c.state, c.result, "themis grants render is the only writer")
		}
	})
	t.Run("a base the history does not reach never compared", func(t *testing.T) {
		in, f, _, _ := repo(t)
		in.Base = strings.Repeat("a", 40)
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "CANNOT RUN - the base "+in.Base+" is not in this history")
		if len(f.Calls) != 0 {
			t.Errorf("uv ran: %v", f.Ran())
		}
	})
	t.Run("the checker missing is a could-not-run naming it", func(t *testing.T) {
		in, f := atomtest.UVTree(t, atomtest.DieTree(nil), nil)
		atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, checker+" is absent, so there is no checker to run")
		if len(f.Calls) != 0 {
			t.Errorf("uv ran: %v", f.Ran())
		}
	})
	t.Run("off the die's source it is absent", func(t *testing.T) {
		in, _ := atomtest.UVTree(t, map[string]string{checker: "x"}, nil)
		atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Absent, "not the policy die's source")
	})
}
