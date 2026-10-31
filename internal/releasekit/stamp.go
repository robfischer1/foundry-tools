package releasekit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
)

// Stamp is the module path and version a Go release build links into its
// binaries (checks.ReleaseStamp), for the module in dir.
//
// ONLY A DELIVERY LANE STAMPS. The executor names a release directory
// (OutEnv) in the lanes that ship what the release atoms stage (build, publish,
// cast) and in no other, so that is the signal: the gate's compile is evidence
// that the tree compiles, runs on a branch tip no image will carry, and stays
// unstamped. Outside a delivery lane the answer is empty and so is the fault.
//
// The version is what the stamp tag ends in (buildlane.StampVersion): the
// landed commit's (ATOM_SHA) committer time and first seven characters, so the
// version a star reports is the tag Flux rolled it from (foundry-tools#18676).
// A delivery lane that cannot say it is a fault, and the caller settles it
// could-not-run: an unstamped binary must not ship as though it were stamped.
func Stamp(ctx context.Context, in atoms.Input, dir string) (module, version, fault string) {
	if os.Getenv(OutEnv) == "" {
		return "", "", ""
	}
	sha := os.Getenv("ATOM_SHA")
	if sha == "" {
		return "", "", "the release build is stamped with the landed commit and ATOM_SHA is not set"
	}
	out, code := atoms.RunCmd(ctx, in, atoms.Cmd{Dir: in.Root, Name: "git", Args: []string{"-c", "safe.directory=*", "log", "-1", "--format=%ct", sha}})
	if code != 0 {
		return "", "", fmt.Sprintf("the committer time of %s could not be read (exit %d): %s", sha, code, strings.TrimSpace(out))
	}
	v, err := buildlane.StampVersion(sha, out)
	if err != nil {
		return "", "", err.Error()
	}
	body, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", "", "the module path could not be read for the stamp: " + err.Error()
	}
	mod := modulePath(string(body))
	if mod == "" {
		return "", "", "go.mod has no module line to name in the stamp"
	}
	if _, err := checks.ReleaseStamp(mod, v); err != nil {
		return "", "", err.Error()
	}
	return mod, v, ""
}

// modulePath is the path on a go.mod's module line: comments dropped, a quoted
// path unquoted. Empty when there is none.
func modulePath(gomod string) string {
	for _, line := range strings.Split(gomod, "\n") {
		line, _, _ = strings.Cut(line, "//")
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "module" {
			continue
		}
		if len(f) > 2 {
			return strings.Join(f[1:], " ")
		}
		if p, err := strconv.Unquote(f[1]); err == nil {
			return p
		}
		return f[1]
	}
	return ""
}

// ToolchainLine is the line a delivery lane's Go release records about the
// toolchain it was built with: `toolchain: go image=<image> version=<goversion>`.
//
// THE IMAGE IS THE PIN THIS ATOM WAS COMPILED WITH, not something the pod
// reports. The atom binary and the index that names the lane's Go image come
// from one foundry-tools commit (TestToolsTomlMatchesGo holds the two equal), and
// the executor pulls the image that index names, so the pod that ran this atom
// ran that image. The version is asked of go itself (`go env GOVERSION`), because
// the image says what was pulled and go says what compiled; a version go will not
// say is written "unreadable", never guessed, and does not hold back the digest.
func ToolchainLine(ctx context.Context, in atoms.Input, image string) string {
	version := "unreadable"
	out, code := atoms.RunCmd(ctx, in, atoms.Cmd{Dir: in.Root, Name: "go", Args: []string{"env", "GOVERSION"}})
	if v := strings.TrimSpace(out); code == 0 && v != "" && !strings.ContainsAny(v, " \t\n") {
		version = v
	}
	return "toolchain: go image=" + image + " version=" + version
}
