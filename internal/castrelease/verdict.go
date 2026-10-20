package castrelease

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
)

// The three ways an atom ends before or instead of a compile. The reasons are
// Dagger's cast sentences: "findings: ..." and "could not run: ..." lead them,
// as they lead cast.go's returns.
func findings(a checks.AtomDef, why string) *checks.Verdict {
	v := checks.VerdictOf(a, buildlane.Findings, why)
	return &v
}

func cannot(a checks.AtomDef, why string) *checks.Verdict {
	v := checks.VerdictOf(a, buildlane.CouldNotRun, why)
	return &v
}

func absent(a checks.AtomDef, why string) *checks.Verdict {
	v := checks.VerdictOf(a, 0, a.ID+": ABSENT - "+why)
	return &v
}

// readOnly is a tool failing to write a file on a read-only filesystem. The
// pod's root is read-only and the lane lays every path a tool may write, so this
// is the lane's own fault, never a verdict on the tree. MEASURED 2026-10-20,
// forks/gravity 08a969e (DEF-B6-029): rustup, syncing the toolchain the tree
// pins, "could not create temp file /usr/local/rustup/tmp/...: Read-only file
// system (os error 30)", which settled FINDINGS on four casts.
var readOnly = regexp.MustCompile(`(?i)read-only file system`)

// ran is the tool's output when it exited zero and its verdict when it did not.
// A tool that would not start never ran (what did not run: Dagger's subject for
// it). One that exited non-zero is classified as the Dagger lane classifies it
// (buildlane.ToolFailed: could-not-run on a network fault, a contended cache or
// the lane's own refused arguments, findings otherwise) with the tool's own
// words under it, where Dagger leaves them in the progress log.
func ran(a checks.AtomDef, step, what, out string, code int) (string, *checks.Verdict) {
	switch {
	case code == 0:
		return out, nil
	case code < 0:
		return "", cannot(a, fmt.Sprintf("could not run: %s did not run: %s", what, out))
	}
	if hit := readOnly.FindString(out); hit != "" {
		v := checks.VerdictOf(a, buildlane.CouldNotRun, fmt.Sprintf("could not run: %s could not write the lane's filesystem (%s) - the lane's fault, not the tree's; the tool never looked\n%s", what, hit, out))
		return "", &v
	}
	cls, why := buildlane.ToolFailed(step, out)
	v := checks.VerdictOf(a, cls, why+"\n"+out)
	return "", &v
}

// carried checks that every binary reached the release directory: one the build
// left out is a finding, as it is when the Dagger cast reads it back (cast.go
// payload), so the capability is never handed a payload with a binary missing.
func carried(a checks.AtomDef, out string, binaries []string) *checks.Verdict {
	for _, b := range binaries {
		if fi, err := os.Stat(filepath.Join(out, b)); err != nil || fi.IsDir() {
			return findings(a, fmt.Sprintf("findings: the release build left no %q in %s", b, out))
		}
	}
	return nil
}

// built is the verdict of a compile that made every binary.
func built(a checks.AtomDef, how string, binaries []string) checks.Verdict {
	return checks.VerdictOf(a, 0, fmt.Sprintf("cast release build: %s; the payload carries %s", how, strings.Join(binaries, ", ")))
}
