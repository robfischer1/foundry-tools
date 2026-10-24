// Package gitextra is `git` on gomutants' PATH for a nightly mutation run: the
// real git for every question, and for the one diff gomutants scopes its run by,
// that diff plus the extra line ranges the runner named.
//
// WHY A HELPER AND NOT A FLAG. gomutants (v0.6.1) takes its scope only as a git
// ref: --changed-since runs `git diff --unified=0 --no-color --no-ext-diff
// --merge-base <ref>` and parses the hunk headers (main.go, discover/diff.go).
// It has no way to be handed line ranges. So the atom's own binary answers as git
// through a link first on PATH for that one exec (atoms.LinkHelper, as `ps`
// does), runs the real git, and appends one `@@ -0,0 +start,count @@` hunk per
// extra range (checks.RenderMutationRanges). Every other git question is the
// real git's, untouched.
package gitextra

import (
	"io"
	"os"
	"os/exec"
	"slices"

	"dagger/foundry-tools/internal/checks"
)

const (
	// EnvRealGit is the real git's absolute path, set by the atom.
	EnvRealGit = "GOMUT_REAL_GIT"
	// EnvRanges is the extra ranges, in checks.EnvMutationRanges' form, named
	// from the directory gomutants runs in (its diff is diff.relative).
	EnvRanges = "GOMUT_EXTRA_RANGES"
)

// Run is the helper as a process; args are git's own, without the program name.
func Run(args []string) int {
	return answer(args, os.Getenv, runReal, os.Stdout, os.Stderr)
}

// runReal runs the real git with this process's stdin and environment.
func runReal(name string, args []string, stdout, stderr io.Writer) int {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		_, _ = io.WriteString(stderr, "git (gitextra): "+name+": "+err.Error()+"\n")
		return 127
	}
	return 0
}

// answer is Run over its seams.
func answer(args []string, getenv func(string) string, real func(string, []string, io.Writer, io.Writer) int, stdout, stderr io.Writer) int {
	git := getenv(EnvRealGit)
	if git == "" {
		_, _ = io.WriteString(stderr, "git (gitextra): "+EnvRealGit+" is not set, so there is no real git to run\n")
		return 127
	}
	scoped := len(args) > 0 && args[0] == "diff" && slices.Contains(args, "--merge-base")
	if !scoped {
		return real(git, args, stdout, stderr)
	}
	extra, err := checks.ParseMutationRanges(getenv(EnvRanges))
	if err != nil {
		_, _ = io.WriteString(stderr, "git (gitextra): "+err.Error()+"\n")
		return 2
	}
	if code := real(git, args, stdout, stderr); code != 0 {
		return code
	}
	_, _ = io.WriteString(stdout, checks.RenderMutationRanges(extra, nil))
	return 0
}
