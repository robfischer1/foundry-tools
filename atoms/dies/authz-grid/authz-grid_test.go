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
		Def.Toolchain != "python" || Def.Surface != "any:**" || strings.Join(Def.Needs, ",") != "tool:git,tool:opa,tool:uv" {
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
			if c.Name == "git" && len(c.Args) > 3 && c.Args[2] == "log" {
				return "fatal: bad revision", 128
			}
			if c.Name == "git" {
				return "abc123", 0
			}
			return atomtest.PinnedOpa(), 0
		}, checks.StateCannotRun, atomtest.Cannot, "could not be read for a Authz-Semantics-Change trailer", "opa|git|git"},
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

// A base the history does not reach is a could-not-run, and the checker never
// runs: a comparison nobody made is not one that passed.
func TestABaseTheHistoryDoesNotReachCouldNotRun(t *testing.T) {
	in, f := atomtest.UVTree(t, files(), func(c atoms.Cmd) (string, int) {
		if c.Name == "git" {
			return "", 1
		}
		return atomtest.PinnedOpa(), 0
	})
	in.Base = "origin/main"
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot,
		"the base origin/main is not in this history (git merge-base exited 1)")
	if got := strings.Join(f.Ran(), "|"); got != "opa|git|git" {
		t.Errorf("ran %q, want the opa probe, the merge-base and the shallow probe alone", got)
	}
}

// shallowClone answers git as a depth-1 clone: the merge base (or the tip's
// parent) resolves only once fetched lands, every fetch answers fetchCode, and
// opa and the checker pass.
func shallowClone(fetchCode int) func(atoms.Cmd) (string, int) {
	fetched := false
	return func(c atoms.Cmd) (string, int) {
		if c.Name != "git" {
			if c.Name == "opa" {
				return atomtest.PinnedOpa(), 0
			}
			return "grid said so", 0
		}
		switch flagged := strings.Join(c.Args, " "); {
		case strings.Contains(flagged, "--is-shallow-repository"):
			return "true\n", 0
		case strings.Contains(flagged, " fetch "):
			fetched = fetchCode == 0
			return "fetch said so", fetchCode
		case strings.Contains(flagged, "rev-parse HEAD") && !strings.Contains(flagged, "HEAD~1"):
			return strings.Repeat("e", 40) + "\n", 0
		case strings.Contains(flagged, "merge-base"), strings.Contains(flagged, "HEAD~1"):
			if fetched {
				return "abc123", 0
			}
			return "", 1
		}
		return "", 0
	}
}

// A shallow clone still runs (stark42 #40): the base it lacks is fetched from
// origin, at absolute depths, the tip and a sha base by name; and a fetch that
// fails says the clone was shallow rather than that the base is not history.
func TestAShallowCloneIsDeepenedBeforeTheBaseIsGivenUp(t *testing.T) {
	base := strings.Repeat("b", 40)
	in, f := atomtest.UVTree(t, files(), shallowClone(0))
	in.Base = base
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass)
	atomtest.WantUV(t, f.Last(), in.Root, "", checker, "--first", listed, "--base", "abc123")
	var fetch string
	for _, c := range f.Calls {
		if c.Name == "git" && strings.Contains(strings.Join(c.Args, " "), " fetch ") {
			fetch = strings.Join(c.Args, " ")
		}
	}
	if !strings.HasSuffix(fetch, "fetch --no-tags --depth=50 origin "+strings.Repeat("e", 40)+" "+base) {
		t.Errorf("fetched %q", fetch)
	}

	in, _ = atomtest.UVTree(t, files(), shallowClone(0))
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass)

	in, _ = atomtest.UVTree(t, files(), shallowClone(128))
	in.Base = base
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot,
		"this clone is shallow and the base "+base+" is not in it; deepening it from origin failed: git fetch --depth=50 exited 128")
}

func TestTheRunnerIsAskedForTheWholeHistory(t *testing.T) {
	if Def.History != "full" {
		t.Errorf("history %q: the merge base is not reached by a fixed depth", Def.History)
	}
}

// gitAs answers git by the first of its rules whose needle the argv carries,
// opa as the pin and the checker as passing.
func gitAs(rules ...[3]string) func(atoms.Cmd) (string, int) {
	return func(c atoms.Cmd) (string, int) {
		if c.Name == "opa" {
			return atomtest.PinnedOpa(), 0
		}
		if c.Name != "git" {
			return "grid said so", 0
		}
		argv := strings.Join(c.Args, " ")
		for _, r := range rules {
			if strings.Contains(argv, r[0]) {
				code := 0
				if r[2] != "0" {
					code = 1
				}
				return r[1], code
			}
		}
		return "", 0
	}
}

// What the base resolution says when it cannot: a tip with no parent in a whole
// history, a shallow clone whose tip will not resolve, a fetch that deepens to
// the root and still does not reach the base, and a shallow tip-only run whose
// fetch fails.
func TestEveryWayTheBaseStaysOutOfReachSaysWhy(t *testing.T) {
	sha := strings.Repeat("b", 40)
	for name, tc := range map[string]struct {
		base  string
		rules [][3]string
		want  string
	}{
		"a whole history with no parent": {"", [][3]string{{"HEAD~1", "fatal: no parent", "1"}, {"--is-shallow-repository", "false", "0"}},
			"the tip has no parent in this history (git rev-parse HEAD~1 exited 1): fatal: no parent"},
		"a shallow tip that will not resolve": {sha, [][3]string{{"merge-base", "", "1"}, {"--is-shallow-repository", "true", "0"}, {"rev-parse HEAD", "fatal: bad HEAD", "1"}},
			"deepening it from origin failed: the tip did not resolve (git rev-parse exited 1): fatal: bad HEAD"},
		"deepened to the root and still unrelated": {sha, [][3]string{{"merge-base", "", "1"}, {"--is-shallow-repository", "true", "0"}, {"rev-parse HEAD", strings.Repeat("e", 40), "0"}, {"fetch", "", "0"}},
			"the base " + sha + " is not in this history (git merge-base exited 1)"},
		"a shallow run with no base whose fetch fails": {"", [][3]string{{"HEAD~1", "", "1"}, {"--is-shallow-repository", "true", "0"}, {"rev-parse HEAD", strings.Repeat("e", 40), "0"}, {"fetch", "refused", "1"}},
			"this clone is shallow and the base HEAD~1 is not in it; deepening it from origin failed: git fetch --depth=50 exited 1: refused"},
	} {
		t.Run(name, func(t *testing.T) {
			in, f := atomtest.UVTree(t, files(), gitAs(tc.rules...))
			in.Base = tc.base
			atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, tc.want)
			for _, c := range f.Calls {
				if c.Name != "git" && c.Name != "opa" {
					t.Errorf("the checker ran: %v", c)
				}
			}
		})
	}
}
