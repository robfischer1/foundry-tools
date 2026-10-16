package atomtest

import (
	"context"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// UVTree is ToolTree for the atoms that run python under uv: `uv --version`
// (the provisioning probe) is answered by the image, and every other call by
// the script. A nil script answers 0 and nothing.
func UVTree(t TB, files map[string]string, answer func(atoms.Cmd) (string, int)) (atomkit.In, *Fake) {
	t.Helper()
	in, f := ToolTree(t, files, func(c atoms.Cmd) (string, int) {
		if c.Name == "uv" && len(c.Args) == 1 && c.Args[0] == "--version" {
			return "uv 0.12.13", 0
		}
		if answer == nil {
			return "", 0
		}
		return answer(c)
	})
	f.Free = []string{"uv"}
	return in, f
}

// WantUV holds a call to the shape every python atom runs in: python3 under
// `uv run`, from the root, both streams, bytecode off, resolved with the one
// requirement (none for "") and given args.
func WantUV(t TB, c atoms.Cmd, root, with string, args ...string) {
	t.Helper()
	Assert(t, c.Name == "uv" && c.Dir == root && c.Both && slices.Contains(c.Env, "PYTHONDONTWRITEBYTECODE=1"),
		"call %+v: want uv in %s with both streams and no bytecode", c, root)
	want := []string{"run", "--no-project", "--quiet"}
	if with != "" {
		want = append(want, "--with", with)
	}
	want = append(append(want, "python3"), args...)
	got := strings.Join(c.Args, " ")
	Assert(t, got == strings.Join(want, " "), "uv ran as %q, want %q", got, strings.Join(want, " "))
}

// CheckerSuite holds an atom whose checker is the tree's own to what they all
// do: run the checker under uv and pass its exit through unmapped, never run it
// on a container without uv, name a checker that is missing, and stand down
// ABSENT off the die's source. extra is the files the atom needs beside the
// checker.
func CheckerSuite(t *testing.T, check func(context.Context, atomkit.In) checks.Verdict, checker, with string, extra map[string]string) {
	t.Helper()
	files := DieTree(extra)
	files[checker] = "# checker"
	for _, c := range []struct {
		name    string
		code    int
		state   checks.State
		result  string
		needles []string
	}{
		{"a clean tree", 0, checks.StatePass, Pass, []string{"checked ok"}},
		{"a finding is the checker's exit 1, unmapped", 1, checks.StateFindings, Findings, []string{"checked ok"}},
		{"a check that could not run is its exit 2, unmapped", 2, checks.StateCannotRun, Cannot, []string{"checked ok"}},
		{"a uv that would not start never ran", -1, checks.StateCannotRun, Cannot, []string{"the atom never ran: checked ok"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			in, f := UVTree(t, files, func(atoms.Cmd) (string, int) { return "checked ok", c.code })
			Expect(t, check(context.Background(), in), c.state, c.result, c.needles...)
			WantUV(t, f.Last(), in.Root, with, checker)
		})
	}
	t.Run("a container without uv never ran the checker", func(t *testing.T) {
		in, f := ToolTree(t, files, func(atoms.Cmd) (string, int) { return "gone", 127 })
		f.Free = []string{"uv"}
		Expect(t, check(context.Background(), in), checks.StateCannotRun, Cannot, "exit code: 127", "failed: uv --version")
		Assert(t, len(f.Calls) == 1, "the checker ran: %v", f.Ran())
	})
	t.Run("the checker missing is a could-not-run naming it", func(t *testing.T) {
		in, f := UVTree(t, DieTree(extra), nil)
		Expect(t, check(context.Background(), in), checks.StateCannotRun, Cannot, checker+" is absent, so there is no checker to run")
		Assert(t, len(f.Calls) == 0, "uv ran: %v", f.Ran())
	})
	t.Run("off the die's source it is absent", func(t *testing.T) {
		in, f := UVTree(t, map[string]string{checker: "x"}, nil)
		Expect(t, check(context.Background(), in), checks.StatePass, Absent, "not the policy die's source")
		Assert(t, len(f.Calls) == 0, "an absent atom touched %v", f.Ran())
	})
}
