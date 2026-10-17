package diesauthzgrid

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
)

// The Def carries the identity the catalogue row will, and validates.
func TestTheDeclarationValidates(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrepush || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "python" || Def.Surface != "any:**" || strings.Join(Def.Needs, ",") != "tool:opa,tool:uv" {
		t.Errorf("%+v", Def)
	}
}

func files(without ...string) map[string]string {
	f := atomtest.DieTree(map[string]string{checker: "# checker"})
	for _, w := range without {
		delete(f, w)
	}
	return f
}

// script answers opa as the pin, git log with the trailers it is given (and the
// merge-base resolve with a sha), and the checker with its exit.
func script(version, trailers string, code int) func(atoms.Cmd) (string, int) {
	return func(c atoms.Cmd) (string, int) {
		switch {
		case c.Name == "opa":
			return version, 0
		case c.Name == "git" && len(c.Args) > 3 && c.Args[2] == "log":
			return trailers, 0
		case c.Name == "git":
			return "abc123", 0
		}
		return "grid said so", code
	}
}

// The checker's exit IS the verdict, unmapped; it is handed the base and the
// acknowledgements, run under uv on a toolchain image and in the venv in the
// tools container.
func TestRunsTheCheckerAndPassesItsExitThrough(t *testing.T) {
	for _, venv := range []bool{false, true} {
		t.Run(map[bool]string{false: "under uv", true: "in the venv"}[venv], func(t *testing.T) {
			atomtest.SetVenv(t, venv)
			for _, c := range []struct {
				name   string
				code   int
				state  checks.State
				result string
				words  string
			}{
				{"the Rego moved no answer", 0, checks.StatePass, atomtest.Pass, "grid said so"},
				{"a moved answer is the checker's exit 1", 1, checks.StateFindings, atomtest.Findings, "grid said so"},
				{"a grid that could not be asked is its exit 2", 2, checks.StateCannotRun, atomtest.Cannot, "grid said so"},
				{"a python that would not start never ran", -1, checks.StateCannotRun, atomtest.Cannot, "the atom never ran: grid said so"},
			} {
				t.Run(c.name, func(t *testing.T) {
					in, f := atomtest.UVTree(t, files(), script(atomtest.PinnedOpa(), "", c.code))
					in.Base = "origin/main"
					atomtest.Expect(t, check(context.Background(), in), c.state, c.result, c.words)
					atomtest.WantUV(t, f.Last(), in.Root, "", checker, "--first", listed, "--base", "abc123")
				})
			}
		})
	}
}

// A commit of the pull that carries Authz-Semantics-Change is handed to the
// checker, one --ack per trailer; the tip with no base is read against its parent.
func TestHandsTheCheckerTheAcknowledgements(t *testing.T) {
	atomtest.SetVenv(t, true)
	in, f := atomtest.UVTree(t, files(), script(atomtest.PinnedOpa(), "first why\x1fsecond why\n\nthird why\n", 0))
	in.Base = "origin/main"
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass)
	atomtest.WantUV(t, f.Last(), in.Root, "", checker, "--first", listed, "--base", "abc123",
		"--ack", "first why", "--ack", "second why", "--ack", "third why")
	var log atoms.Cmd
	for _, c := range f.Calls {
		if c.Name == "git" && len(c.Args) > 3 && c.Args[2] == "log" {
			log = c
		}
	}
	if got := atomtest.Flagged(log); !strings.Contains(got, "trailers:key=Authz-Semantics-Change") || !strings.HasSuffix(got, "abc123..HEAD") {
		t.Errorf("the trailers were read as %q", got)
	}

	in, f = atomtest.UVTree(t, files(), script(atomtest.PinnedOpa(), "", 0))
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass)
	atomtest.WantUV(t, f.Last(), in.Root, "", checker, "--first", listed, "--base", "HEAD~1")
}

// Nothing runs on a tree that cannot be graded: no checker, an opa that is not
// the pin, a trailer log that fails, or off the die's source.
func TestRefusesBeforeRunningAnythingItCannotGrade(t *testing.T) {
	for _, c := range []struct {
		name   string
		tree   map[string]string
		answer func(atoms.Cmd) (string, int)
		state  checks.State
		result string
		needle string
		calls  string
	}{
		{"no checker", files(checker), script(atomtest.PinnedOpa(), "", 0), checks.StateCannotRun, atomtest.Cannot, checker + " is absent", ""},
		{"an opa that is not the pin", files(), script("Version: 0.1.0", "", 0), checks.StateCannotRun, atomtest.Cannot, "does not answer", "opa"},
		{"a trailer log that fails", files(), func(c atoms.Cmd) (string, int) {
			if c.Name == "git" {
				return "fatal: bad revision", 128
			}
			return atomtest.PinnedOpa(), 0
		}, checks.StateCannotRun, atomtest.Cannot, "could not be read for a Authz-Semantics-Change trailer", "opa|git"},
		{"not the die's source", map[string]string{checker: "x"}, script(atomtest.PinnedOpa(), "", 0), checks.StatePass, atomtest.Absent, "not the policy die's source", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			in, f := atomtest.UVTree(t, c.tree, c.answer)
			atomtest.Expect(t, check(context.Background(), in), c.state, c.result, c.needle)
			if got := strings.Join(f.Ran(), "|"); got != c.calls {
				t.Errorf("ran %q, want %q", got, c.calls)
			}
		})
	}
}
