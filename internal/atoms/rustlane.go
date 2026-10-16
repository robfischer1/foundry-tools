package atoms

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// THE RUST LANE'S SHARED PARTS: what rust:cargo-clippy and rust:cargo-test (and
// the wit guest) each need. The container is the adapter's (the dependency
// graph fetched, the target volume mounted); what is here is the part of the
// chain that was an exec on it.

// CargoVerdict is a cargo exec's answer as the atom's verdict, WITH CARGO'S EXIT
// VOCABULARY TRANSLATED FIRST (checks.CargoExit): cargo answers 101, not 1, when
// the command failed, and StateFor reads a 101 as could-not-run. The shell
// bodies this ports all ended `|| exit 1`, mapping every non-zero cargo code to
// FINDINGS; this is that mapping narrowed to the one code it existed for. A
// program that would not start (code -1) never ran.
func CargoVerdict(a checks.AtomDef, out string, code int) checks.Verdict {
	if code < 0 {
		return CouldNotStart(a, out)
	}
	return checks.VerdictOf(a, checks.CargoExit(code), out)
}

// CargoExec is one cargo program run in the tree's root with the extra
// environment, its output as the chain's output() gave it: stdout, plus stderr
// when it exited non-zero, trimmed.
func CargoExec(ctx context.Context, in Input, env []string, argv ...string) (string, int) {
	return in.run(ctx, Cmd{Dir: in.Root, Name: argv[0], Args: argv[1:], Env: env})
}

// RustStamp dates the tree in the working directory past every artifact in the
// shared target (checks.Unstale), so cargo builds THIS tree. It runs on the /src
// cargo will read, in the atom, AFTER the adapter's linked-worktree swap: a
// stamp laid before a re-mount of /src is discarded with it (stellar-core-rust
// tests/claim_composition, 2026-10-12). A stamp that fails is the atom's
// could-not-run. ok is false with the verdict to return.
func RustStamp(ctx context.Context, in Input, a checks.AtomDef) (v checks.Verdict, ok bool) {
	return Provision(ctx, in, a, in.Root, checks.Unstale()...)
}

// RustScope is THE PER-CRATE SCOPE (checks.RustScope says why): the argument
// sets, one per cargo run, that grade the crates this pull reaches instead of
// `--workspace`, as the repo's own tools/scope/scope.py names them for verb
// ("test" or "clippy"). ok is false for the whole workspace, and EVERY doubt is
// false: a tree without the script (the repo did not opt in), no base, a base the
// history does not reach, a diff or metadata or script that would not run, a file
// in the root package. ok with no sets is a pull that touched nothing a crate can
// feel.
//
// WHY THE GATE ASKS THE REPO. Which conformance tests belong to which crate is a
// naming convention of the repo, and the script that knows it is under test there
// (tools/scope/test_scope.py); a second copy of that rule in Go would drift from
// it.
func RustScope(ctx context.Context, in Input, verb string) (sets [][]string, ok bool) {
	if in.Base == "" {
		return nil, false
	}
	if fi, err := os.Stat(filepath.Join(in.Root, checks.RustScopeScript)); err != nil || fi.IsDir() {
		return nil, false
	}
	since, err := ChangeBase(ctx, in, in.Root)
	if err != nil || since == "" {
		return nil, false
	}
	// --no-renames: a file moved from crate A to crate B lists only B's path by
	// default, and A, which lost it and may no longer compile, would go ungraded.
	files, code := in.run(ctx, Cmd{Dir: in.Root, Name: "git", Args: []string{"diff", "--relative", "--no-renames", "--name-only", "-z", since, "HEAD"}})
	if code != 0 {
		return nil, false
	}
	meta, code := in.run(ctx, Cmd{Dir: in.Root, Name: "cargo", Args: []string{"metadata", "--no-deps", "--format-version", "1"}})
	if code != 0 {
		return nil, false
	}
	crates, whole, err := checks.RustScope([]byte(meta), in.Root, strings.Split(files, "\x00"))
	if err != nil || whole {
		return nil, false
	}
	if len(crates) == 0 {
		return nil, true
	}
	out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "python3", Args: append([]string{checks.RustScopeScript, verb}, crates...)})
	if code != 0 {
		return nil, false
	}
	sets = checks.RustScopeArgs(out)
	if len(sets) == 0 {
		return nil, false
	}
	return sets, true
}
