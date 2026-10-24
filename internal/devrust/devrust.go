// Package devrust is the shared body of the dev lane's cargo verbs (dev:rust-test,
// -check, -build, -clippy and their twins -lint and -typecheck): the one place
// that decides WHAT a dev cargo run grades and how it is bounded.
//
// WHY THE VERBS SCOPE. After the monorepo crate split a bare `cargo test
// --workspace` builds and runs every member's targets (236 on 10-23): the
// rust container's working set reached 13-14 GiB against a 3.9 GiB request.
// Measured on dev-stellar-core-747fda9 (10-24, memory.stat every 5 s): anon
// peaked at 0.79 GiB; the rest was the page cache of the ~15 GiB of artifacts
// the whole workspace writes. So the lever is how much is built, not how many
// rustc run at once: a change scoped to its crates writes a fraction of it.
//
// THE SCOPE IS THE GATE'S (atoms.RustScope over the repo's tools/scope/scope.py),
// asked of the change the dev commit carries: the working tree against the HEAD
// it was asked from, the commit's parent. Every doubt is the whole workspace, as
// before: no change, a workspace-level file, a repo that did not opt in.
package devrust

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/devlane"
)

// Stage is the dev lane's stage: the gate never grades these atoms.
const Stage = "dev"

// Parent is the base a dev run measures its change from when the door names
// none: the dev commit's parent, the HEAD the working tree was committed over
// (foundry-toolbox ask dev: commit-tree -p HEAD). The pod fetches the commit at
// depth 2, so the parent is there.
const Parent = "HEAD~1"

// Verb is one cargo verb of the dev lane.
type Verb struct {
	// ID is the atom's id, dev:rust-<verb>.
	ID string
	// Cargo is the verb as devlane.RustArgv spells it: check, build, test, clippy.
	Cargo string
	// Scope is the scope script's verb: "test" (the crates, then their
	// conformance tests as a second run) or "clippy" (one run).
	Scope string
	// Link is true for a verb whose product is linked binaries (test, build):
	// it links with mold under the gate's thread cap, as rust:cargo-test does.
	Link bool
}

// preflight is the rust toolchain's preflight (tools.toml [toolchain.rust]),
// which the command atoms these replace ran first. A logic atom gets none from
// the pod, so the body runs it: a stale Cargo.lock stays a could-not-run.
var preflight = []string{"cargo", "fetch", "--locked"}

// Env is the bound every dev cargo run carries: the GATE'S knobs, one home
// (checks/rustgate.go). CARGO_BUILD_JOBS is the shape's cores held to its
// memory at 1 GiB a job (checks.RustGateJobsNow), and 6 only where the executor
// names no shape (no ATOM_CPUS), so a full-workspace run is no wider than the
// pod it runs in; CARGO_INCREMENTAL=0, since the target is per pod and an
// incremental cache is a second copy of every crate's codegen; line-tables
// debuginfo, which keeps file:line in a backtrace and shrinks every test
// binary the page cache holds. A linking verb adds mold's thread cap.
func Env(v Verb) []string {
	env := slices.Concat(checks.CargoBoundedEnv(), checks.CargoLineTablesEnv())
	if v.Link {
		env = append(env, checks.CargoMoldLinkEnv()...)
	}
	return env
}

// Plan is the argument sets of the run, one per cargo invocation, and the line
// that says why. A caller who named the packages (devlane.CargoScoped) runs as
// asked; otherwise the change's scope, else the whole workspace (one empty set,
// which devlane.RustArgv widens to --workspace).
func Plan(ctx context.Context, in atoms.Input, v Verb) (sets [][]string, why string) {
	if devlane.CargoScoped(in.Args) {
		return [][]string{nil}, "as asked: the arguments name the packages"
	}
	at := in
	if at.Base == "" {
		at.Base = Parent
	}
	if scoped, ok := atoms.RustScope(ctx, at, v.Scope); ok && len(scoped) > 0 {
		parts := make([]string, len(scoped))
		for i, s := range scoped {
			parts[i] = strings.Join(s, " ")
		}
		return scoped, "scoped to the change against " + at.Base + ": " + strings.Join(parts, " | ")
	}
	return [][]string{nil}, "the whole workspace: the change names no crate to scope to (no change, a workspace-level file, or no " + checks.RustScopeScript + ")"
}

// Check is the verb's verdict: the preflight, then one cargo run per set of the
// plan with the caller's arguments after the scope, stopping at the first that
// is not green. The logs open with the plan.
func Check(ctx context.Context, in atoms.Input, v Verb) checks.Verdict {
	row := checks.AtomDef{ID: v.ID, Stage: Stage, Lane: checks.LaneRust}
	env := Env(v)
	if out, code := atoms.CargoExec(ctx, in, env, preflight...); code != 0 {
		if code < 0 {
			return atoms.CouldNotStart(row, out)
		}
		return checks.VerdictOf(row, 2, fmt.Sprintf("%s: CANNOT RUN - preflight `%s` exited %d: %s", v.ID, strings.Join(preflight, " "), code, out))
	}
	sets, why := Plan(ctx, in, v)
	log := []string{v.ID + ": " + why}
	for _, set := range sets {
		argv, err := devlane.RustArgv(v.Cargo, slices.Concat(set, in.Args))
		if err != nil {
			return checks.VerdictOf(row, 2, v.ID+": "+err.Error())
		}
		if v.Link {
			argv = checks.MoldRun(argv)
		}
		out, code := atoms.CargoExec(ctx, in, env, argv...)
		log = append(log, "$ "+strings.Join(argv, " "), out)
		if code != 0 {
			if code < 0 {
				return atoms.CouldNotStart(row, out)
			}
			return atoms.CargoVerdict(row, strings.Join(log, "\n"), code)
		}
	}
	return atoms.CargoVerdict(row, strings.Join(log, "\n"), 0)
}
