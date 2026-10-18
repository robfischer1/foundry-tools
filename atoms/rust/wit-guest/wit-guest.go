// Package rustwitguest is the atom rust:wit-guest: every wit-guest-<world>
// feature of the crate builds for wasm32, lifts to a component that validates,
// and passes its native tests. It is a logic atom: it builds in the target dir
// it is given and leaves nothing for any other atom.
package rustwitguest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It builds in a target dir and dates the tree
// (atoms.RustStamp), so it says it writes. The tools it needs are wasm-tools
// (tools.toml) and the wasm32 target, which it adds itself.
//
// IT RUNS ON THE rust TOOLCHAIN, 1.97, NOT rust-wasm (1.90). The chain it
// replaces ran on checks.ImageRust and every verdict the gate has recorded for
// it was produced there, so R20 keeps that toolchain (DEF-B4-wit-2).
var Def = atomkit.Def{
	ID: "rust:wit-guest", Stage: checks.StagePrepush, Lane: checks.LaneRust,
	Group:         checks.GroupLanguage,
	Desc:          "Each wit-guest-<world> feature (discovered from Cargo.toml) builds for wasm32-unknown-unknown, lifts to a component that validates, weighs no more than its byte budget in wit-guest-budgets.toml when the tree carries one, and passes its native cargo test; absent where the manifest declares none.",
	Surface:       "root:Cargo.toml",
	Toolchain:     "rust",
	Needs:         []string{"tool:wasm-tools"},
	WritesTree:    true,
	NeedsManifest: true,
	Timeout:       45 * time.Minute,
	Check:         check,
}

const id = "rust:wit-guest"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneRust}

// Every wit-guest-<world> feature of the crate builds for wasm32-unknown-unknown,
// the module lifts to a component, the component validates, and the world's
// own tests pass natively.
//
// DISCOVERED, NOT NAMED (rob/stellar-core-rust#14205): checks.WitGuestWorlds
// reads the [features] table, so a new world is graded with no lane edit and a
// manifest with none is ABSENT, decided in Go before any program runs. The
// default-features test run never compiles a guest's WIT export, so a guest that
// stopped building - or whose WIT drifted - read green everywhere; this is the
// one atom that compiles each world, and its native `cargo test` is where the
// crossing-type round trips run, because the types exist only under the feature.
//
// FIVE STEPS PER WORLD, EACH ITS OWN VERDICT: the build, `wasm-tools component
// new`, `wasm-tools validate`, `wasm-tools component wit` (read-back), and
// `cargo test --no-default-features --features <world>`. The first failure
// returns, and its reason and first log line NAME THE WORLD, so a red says
// which one. A pass carries every world's lines, each prefixed with its name.
//
// AND ONE CHECK OVER ALL WORLDS between the builds and the test: each module's
// bytes against the tree's wit-guest-budgets.toml (budget, below). Every pass
// logs every world's size, budget or not.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	manifest, err := os.ReadFile(filepath.Join(in.Root, "Cargo.toml"))
	if err != nil {
		return checks.VerdictOf(row, 2, fmt.Sprintf("%s: CANNOT RUN - Cargo.toml would not read: %v", id, err))
	}
	worlds := checks.WitGuestWorlds(string(manifest))
	if len(worlds) == 0 {
		return checks.VerdictOf(row, 0, id+": ABSENT - Cargo.toml declares no "+checks.WitGuestPrefix+"<world> feature, so this crate exports no WIT guest to build")
	}
	artifact := checks.WitGuestArtifact(string(manifest))
	if artifact == "" {
		return checks.VerdictOf(row, 2, id+": CANNOT RUN - Cargo.toml declares wit-guest features but names no [package] or [lib] name, so the guest's file name is unknown")
	}

	// UNDER THE UNSTALE STAMP, for both halves. The guest builds into a target
	// that outlives a tree (the repository's release volume on Dagger): unstamped,
	// cargo would call the crate Fresh and hand back a guest an older tree built.
	// The native test needs it for the same reason: WITHOUT IT cargo ran the test
	// binary an older tree had left behind, and a test module added since (a new
	// file, which the old dep-info never listed) was neither compiled nor run
	// (MEASURED on stellar-core-rust#14205: the identity world stayed at 197
	// tests with 26 round-trip tests in the tree).
	if v, ok := atoms.RustStamp(ctx, in, row); !ok {
		return v
	}
	if v := atoms.WitProvision(ctx, in, row, checks.WitGuestTarget, "wasm-tools"); v != nil {
		return *v
	}

	target := atoms.CargoTargetDir()
	work := atoms.WorkDir()
	built := filepath.Join(target, checks.WitGuestTarget, "release", artifact)
	env := checks.CargoBoundedEnv()

	var logs []string
	sizes := map[string]int64{}
	for _, world := range worlds {
		// The module as this world's build left it, carried out of the target
		// by the atom: the next world's build overwrites the one there.
		module := filepath.Join(work, world+".wasm")
		component := filepath.Join(work, world+".component.wasm")
		v := buildWorld(ctx, in, env, world, target, built, module)
		if v.State != int(checks.StatePass) {
			return namedWorld(v, world, "build")
		}
		for _, step := range []struct {
			name string
			args []string
		}{
			{"component new", []string{"wasm-tools", "component", "new", module, "-o", component}},
			{"validate", []string{"wasm-tools", "validate", component}},
			{"component wit", []string{"wasm-tools", "component", "wit", component}},
		} {
			if v = atoms.SettleTool(ctx, in, row, atoms.Cmd{Dir: in.Root, Name: step.args[0], Args: step.args[1:], Env: env}); v.State != int(checks.StatePass) {
				return namedWorld(v, world, step.name)
			}
		}
		logs = append(logs, prefixLines(world, v.Logs)...)
		// A module the build did not leave is the lift's to fail on (buildWorld);
		// one that is there is weighed.
		switch st, err := os.Stat(module); {
		case err == nil:
			sizes[world] = st.Size()
		case !errors.Is(err, fs.ErrNotExist):
			return checks.VerdictOf(row, 2, fmt.Sprintf("%s: CANNOT RUN - world %s's module would not stat: %v", id, world, err))
		}
	}

	// THE SIZE BUDGET, before the native test: it is the cheap answer, and a
	// guest over budget needs no test run to say so (checks.WitGuestBudgetsFile).
	sizeLines, v := budget(in.Root, sizes)
	if v != nil {
		return *v
	}
	logs = append(logs, sizeLines...)

	// THE NATIVE TEST RUNS ONCE, WITH EVERY WORLD'S FEATURE ON. Each world
	// gates only its own `pub mod wit;`, so the worlds are additive and their
	// bindings live in sibling modules (`<core>::wit::bindings`). The component
	// exports are wasm32-only (`#[cfg(target_arch = "wasm32")] export!`), so
	// natively nothing emits cabi_realloc or an export symbol to clash.
	// MEASURED on stellar-core-rust (16 worlds): the combined lib-test binary
	// builds, links and passes, and lists exactly the union of the sixteen
	// per-world lists (1021 = 1021, names compared). One run replaces N, each
	// of which compiled the crate's test binary again and re-ran every
	// ungated lib test.
	testDir := filepath.Join(target, checks.WitGuestTestDir)
	tested := atoms.CargoRun(ctx, in, row, env, testArgv(strings.Join(worlds, ","), testDir)...)
	if tested.State == int(checks.StatePass) {
		return checks.VerdictOf(row, 0, strings.Join(logs, "\n"))
	}
	return attribute(ctx, in, env, worlds, testDir, tested)
}

// budget holds each world's module to the tree's budget file, when it carries
// one, and answers the size lines the pass logs. A breach is a finding that
// lists every world over (the delta and the budget), not only the first.
func budget(root string, sizes map[string]int64) ([]string, *checks.Verdict) {
	src, err := os.ReadFile(filepath.Join(root, checks.WitGuestBudgetsFile))
	var budgets map[string]int64
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		v := checks.VerdictOf(row, 2, fmt.Sprintf("%s: CANNOT RUN - %s would not read: %v", id, checks.WitGuestBudgetsFile, err))
		return nil, &v
	default:
		if budgets, err = checks.ParseWitGuestBudgets(src); err != nil {
			v := checks.VerdictOf(row, 1, fmt.Sprintf("%s [size budget]: %s: %v", id, checks.WitGuestBudgetsFile, err))
			return nil, &v
		}
	}
	var lines []string
	for _, w := range slices.Sorted(maps.Keys(sizes)) {
		lines = append(lines, checks.WitGuestSizeLine(w, sizes[w], budgets[w]))
	}
	if budgets == nil {
		return lines, nil
	}
	if found := checks.WitGuestBudgetFindings(sizes, budgets); len(found) > 0 {
		v := checks.VerdictOf(row, 1, fmt.Sprintf("%s [size budget]: %d of %d worlds breach %s\n%s\n\nevery world:\n%s",
			id, len(found), len(sizes), checks.WitGuestBudgetsFile, strings.Join(found, "\n"), strings.Join(lines, "\n")))
		return nil, &v
	}
	return lines, nil
}

// buildWorld is one world's wasm32 build, and the module carried out of the target.
// The module is removed first, so a build that did not produce it leaves it
// absent and the lift fails on it, never on an older tree's copy.
func buildWorld(ctx context.Context, in atomkit.In, env []string, world, target, built, module string) checks.Verdict {
	for _, p := range []string{built, module} {
		if err := atoms.ClearOutput(p); err != nil {
			return checks.VerdictOf(row, 2, fmt.Sprintf("%s: CANNOT RUN - %v", id, err))
		}
	}
	v := atoms.CargoRun(ctx, in, row, env,
		"cargo", "build", "--locked", "--release",
		"--no-default-features", "--features", world,
		"--target", checks.WitGuestTarget, "--target-dir", target)
	if v.State != int(checks.StatePass) {
		return v
	}
	if err := atoms.CarryOut(built, module); err != nil {
		return checks.VerdictOf(row, 126, fmt.Sprintf("%s: the module would not copy out of the target: %v", id, err))
	}
	return v
}

// attribute names the world a red native test belongs to. The combined run
// cannot say which world's tests failed (a world's directory is not always its
// name: rank is src/ranked), so a red re-runs the worlds one at a time and names
// the first that fails, as the per-world loop did. Only a red pays for this. A
// red that no single world reproduces (the combination itself fails) is named
// for the combination.
func attribute(ctx context.Context, in atomkit.In, env, worlds []string, testDir string, combined checks.Verdict) checks.Verdict {
	for _, world := range worlds {
		if wv := atoms.CargoRun(ctx, in, row, env, testArgv(world, testDir)...); wv.State != int(checks.StatePass) {
			return namedWorld(wv, world, "test")
		}
	}
	return namedWorld(combined, "all "+strings.Join(worlds, "+"), "test")
}

// testArgv is the native lib test for a feature list (one world, or every
// world comma-joined), in the test target dir (checks.WitGuestTestDir says why
// it is apart from the build's). --lib ONLY: tests/*.rs and doctests take no
// world feature and rust:cargo-test already runs them once; without --lib every
// world re-ran the whole suite and wit-guest reached 26m40s (gate run 737,
// deadline hit in run 775, main b5f9a63, 2026-10-12).
//
// --workspace, BECAUSE A WORLD'S TESTS LIVE IN ITS CRATE. stellar-core-rust was
// one package whose `--lib` held every world's round-trip tests; split per core
// (F26a), the root package is a facade with no test of its own, and `--lib`
// alone would pass running none of the 1,042 the combined run listed. The
// root's wit-guest-<world> features forward to the member crates' own, so one
// `--workspace --features` run still turns every world on; a member that
// defines no such feature is simply not asked for it. A single-package repo is
// its own workspace and reads the flag as it always read the bare form.
func testArgv(features, targetDir string) []string {
	return []string{
		"cargo", "test", "--locked", "--workspace", "--lib", "--no-default-features", "--features", features,
		"--target-dir", targetDir,
	}
}

// namedWorld stamps a failing step's verdict with the world and step that
// produced it, in the reason and as the first log line.
func namedWorld(v checks.Verdict, world, step string) checks.Verdict {
	v.Reason = fmt.Sprintf("%s [world %s, %s]%s", id, world, step, strings.TrimPrefix(v.Reason, id))
	v.Logs = slices.Insert(v.Logs, 0, fmt.Sprintf("world %s failed at %s", world, step))
	return v
}

func prefixLines(world string, lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = "[" + world + "] " + l
	}
	return out
}
