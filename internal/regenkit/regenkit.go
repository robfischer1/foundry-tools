// Package regenkit is what the two regeneration atoms share: ts:regen
// (internal/regenlane) and dev:go-regen (internal/goregen). Both rebuild a
// core's guest from the Rust kit (checks.RustKit) at a recorded commit, so both fetch that
// commit the same way, run programs and word their failures the same way, and
// download a pinned asset the same way (from its own URL, the sum held). The
// pieces live here once (R36) and carry no knowledge of either atom's steps.
package regenkit

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// ExecFunc runs a program: what it said and its exit code (negative when it
// never started).
type ExecFunc func(atoms.Cmd) (string, int)

// Runner runs argv in dir under env and answers its output, or an error naming
// the program, its exit and the tail of what it said.
type Runner func(dir string, env []string, argv []string) (string, error)

// GitEnv keeps git from asking a person for a credential nobody holds: the
// repositories are read without one.
var GitEnv = []string{"GIT_TERMINAL_PROMPT=0"}

// FetchFunc is the checksummed download (checks.FetchPinned); a test replaces it.
type FetchFunc func(ctx context.Context, client *http.Client, sources []string, sum, dest string) error

// Tail is the last lines of a program's output: enough to say why, one line.
func Tail(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-6):], " | ")
}

// Step runs one program in the temp directory; a failure names it and what it said.
func Step(exec ExecFunc, env []string, argv ...string) error {
	out, code := exec(atoms.Cmd{Dir: os.TempDir(), Name: argv[0], Args: argv[1:], Env: env})
	if code == 0 {
		return nil
	}
	return fmt.Errorf("%s exited %d: %s", strings.Join(argv, " "), code, Tail(out))
}

// Fetch downloads a pinned asset from its own URL, held to the pinned sum (checks.ToolSources).
func Fetch(ctx context.Context, fetch FetchFunc, url, dest string) error {
	sources, sum, err := checks.ToolSources(url, checks.ToolSHA256)
	if err != nil {
		return err
	}
	return fetch(ctx, nil, sources, sum, dest)
}

// Wrap names what a read was for.
func Wrap(what string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %v", what, err)
}

// CargoBin is where `cargo install` leaves a program, when CARGO_HOME is named.
func CargoBin() string {
	if h := os.Getenv("CARGO_HOME"); h != "" {
		return filepath.Join(h, "bin")
	}
	return ""
}

// PathWith is PATH with these directories in front (the empty ones skipped).
func PathWith(dirs ...string) string {
	var keep []string
	for _, d := range dirs {
		if d != "" {
			keep = append(keep, d)
		}
	}
	return strings.Join(append(keep, os.Getenv("PATH")), string(os.PathListSeparator))
}

// UnpackCore unpacks the Rust kit at one commit under base and answers where
// the kit is: a shallow fetch of that commit into an empty repository, then the
// WHOLE commit's tree as an archive, which is what the core is built from (never
// a checkout). The answer is the kit's own directory inside it (rust/ in the
// monorepo), so a caller builds from there as it did from the kit's root.
//
// THE WHOLE COMMIT, NOT THE SUBDIRECTORY. stellar-core's root Cargo.toml is the
// virtual workspace over rust/ and Cargo.lock sits beside it; an archive of
// <commit>:rust alone leaves the facade with no workspace and no lock, and
// `cargo build --locked` fails. A commit from before the import merge has its
// workspace and lock at rust/, and building in rust/ finds those the same way.
//
// A commit recorded against the repository the kit used to live in is fetched
// as the one it became (checks.KitSource.Commit); a failure names the commit
// recorded and, when it differs, the one fetched.
func UnpackCore(run Runner, base, commit string) (string, error) {
	kit := checks.RustKit
	fetch := kit.Commit(commit)
	// the repository is "kit", not the kit's name: ts:regen fetches the WIT pins of
	// rob/stellar-core into base/stellar-core, and the two stay apart
	repo, tar, dir := filepath.Join(base, "kit"), filepath.Join(base, "core.tar"), filepath.Join(base, "core")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, step := range []struct {
		dir  string
		env  []string
		argv []string
	}{
		{base, nil, checks.RegenGitInit(repo)},
		{repo, GitEnv, checks.RegenGitFetch(kit.GitURL(), fetch)},
		{repo, nil, checks.RegenGitArchive(tar, fetch)},
		{base, nil, checks.RegenTar(tar, dir, false, 0, "")},
	} {
		if _, err := run(step.dir, step.env, step.argv); err != nil {
			if fetch != commit {
				return "", fmt.Errorf("core @%s (%s %s): %v", commit, kit.Name(), fetch, err)
			}
			return "", fmt.Errorf("core @%s: %v", commit, err)
		}
	}
	at := filepath.Join(dir, filepath.FromSlash(kit.Subdir))
	if st, err := os.Stat(at); err != nil || !st.IsDir() {
		return "", fmt.Errorf("core @%s: %s %s has no %s/", commit, kit.Name(), fetch, kit.Subdir)
	}
	return at, nil
}
