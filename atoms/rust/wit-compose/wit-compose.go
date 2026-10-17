// Package rustwitcompose is the atom rust:wit-compose: every wit-guest world
// composes into one component, and the tapes the crate carries replay through it
// with the answers the native core gave. It is a logic atom: the composer builds
// the guests it composes, so it needs no other atom's output.
package rustwitcompose

import (
	"context"
	"fmt"
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
// (atoms.RustStamp), so it says it writes. wasm-tools and wac are tools.toml
// url tools; the wasm32 target it adds itself.
//
// IT RUNS ON THE rust TOOLCHAIN, 1.97, as rust:wit-guest does, for the reason
// that atom's Def gives (DEF-B4-wit-2).
var Def = atomkit.Def{
	ID: "rust:wit-compose", Stage: checks.StagePrepush, Lane: checks.LaneRust,
	Group:         checks.GroupLanguage,
	Desc:          "Every wit-guest world composes into one valid component through tools/compose/compose.sh, and every tests/tapes/<world>.json replays through it with the native core's answers, and every tests/red/*.json replays as red (the answer is its `today`, never its prior-art response); absent where the tree carries no composer or replay host.",
	Surface:       "root:Cargo.toml",
	Toolchain:     "rust",
	Needs:         []string{"tool:wasm-tools", "tool:wac"},
	WritesTree:    true,
	NeedsManifest: true,
	Timeout:       45 * time.Minute,
	Check:         check,
}

const id = "rust:wit-compose"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneRust}

// Every guest composes into ONE component and every tape the crate carries for
// a world replays through it with the answers the native core gave.
//
// WHY A LANE (stellar-core-rust#15266). rust:wit-guest grades each world alone;
// the fleet ships them composed, and a composition has failure modes no single
// guest has (a world that `use`s another's types, two worlds sharing a package).
// The check was opt-in (COMPOSE_CHECK=1) behind tools the plain test lane does
// not carry, and the replay host lived in a scratch directory, so nothing ran
// either. This atom carries them: the wasm32 target, wasm-tools and wac.
//
// ABSENT UNLESS THE CRATE OWNS ALL THREE PIECES: a wit-guest world, the
// composer (tools/compose/compose.sh) and the replay host
// (tools/replay/Cargo.toml). A repository with a guest and no composer is
// graded by rust:wit-guest alone.
//
// THREE STEPS, EACH ITS OWN VERDICT: build the replay host (its own lockfile,
// its own target dir), compose every guest through the script, and replay the
// tapes named for the discovered worlds (tests/tapes/<world>.json, when the
// tree carries one), then every red tape (tests/red/*.json, judged as red). A
// tape the composition answers differently is a finding.
//
// NOTHING IS READ FROM rust:wit-guest. compose.sh builds every guest it
// composes into CARGO_TARGET_DIR and reads them in its own run, so this atom
// has no ordering against that one and no volume shared with it (DEF-B4-wit-1).
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	manifest, err := os.ReadFile(filepath.Join(in.Root, "Cargo.toml"))
	if err != nil {
		return checks.VerdictOf(row, 2, fmt.Sprintf("%s: CANNOT RUN - Cargo.toml would not read: %v", id, err))
	}
	if len(checks.WitGuestWorlds(string(manifest))) == 0 {
		return checks.VerdictOf(row, 0, id+": ABSENT - Cargo.toml declares no "+checks.WitGuestPrefix+"<world> feature, so there is nothing to compose")
	}
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	files := in.Files
	if !slices.Contains(files, checks.WitComposeScript) || !slices.Contains(files, checks.WitReplayManifest) {
		return checks.VerdictOf(row, 0, id+": ABSENT - the tree carries no "+checks.WitComposeScript+" and "+checks.WitReplayManifest+", so it declares no composition to check")
	}
	tapes := checks.WitComposeTapes(string(manifest), files)
	if len(tapes) == 0 {
		return checks.VerdictOf(row, 2, id+": CANNOT RUN - no tests/tapes/<world>.json for any wit-guest world, so a composition would be built and answer nothing")
	}

	// THE RED TAPES RIDE LAST (F18): the replay host holds each violated case to
	// its `today` and fails one that answers its prior-art response.
	tapes = append(tapes, checks.WitComposeRed(files)...)

	// UNDER THE STAMP, as rust:wit-guest builds: the script builds every guest
	// into CARGO_TARGET_DIR, which outlives a tree where the target is a volume.
	if v, ok := atoms.RustStamp(ctx, in, row); !ok {
		return v
	}
	if v := atoms.WitProvision(ctx, in, row, checks.WitGuestTarget, "wasm-tools", "wac"); v != nil {
		return *v
	}

	target := atoms.CargoTargetDir()
	work := atoms.WorkDir()
	host := filepath.Join(work, checks.WitReplayDir, "replay")
	composed := filepath.Join(work, checks.WitComposedName)
	env := append(checks.CargoBoundedEnv(), "CARGO_TARGET_DIR="+target)

	// The replay host is its own workspace: it builds in a directory of its own
	// under the target and is carried out of it, so the replay runs a copy no
	// other build can relink.
	hostBuilt := filepath.Join(target, checks.WitReplayDir, "release", "replay")
	for _, p := range []string{hostBuilt, host, composed} {
		if err := atoms.ClearOutput(p); err != nil {
			return checks.VerdictOf(row, 2, fmt.Sprintf("%s: CANNOT RUN - %v", id, err))
		}
	}
	var v checks.Verdict
	for _, step := range []struct {
		name string
		args []string
		then func() error
	}{
		{"replay host", []string{"cargo", "build", "--locked", "--release", "--manifest-path", checks.WitReplayManifest, "--target-dir", filepath.Join(target, checks.WitReplayDir)},
			func() error { return atoms.CarryOut(hostBuilt, host) }},
		{"compose", []string{checks.WitComposeScript, composed}, nil},
		{"replay", append([]string{host, composed}, tapes...), nil},
	} {
		v = atoms.SettleTool(ctx, in, row, atoms.Cmd{Dir: in.Root, Name: step.args[0], Args: step.args[1:], Env: env})
		if v.State == int(checks.StatePass) && step.then != nil {
			if err := step.then(); err != nil {
				v = checks.VerdictOf(row, 126, fmt.Sprintf("%s: the %s would not copy out of the target: %v", id, step.name, err))
			}
		}
		if v.State != int(checks.StatePass) {
			return namedStep(v, step.name)
		}
	}
	return v
}

// namedStep stamps a failing step's verdict with the step that produced it.
func namedStep(v checks.Verdict, step string) checks.Verdict {
	v.Reason = fmt.Sprintf("%s [%s]%s", id, step, strings.TrimPrefix(v.Reason, id))
	v.Logs = slices.Insert(v.Logs, 0, "failed at "+step)
	return v
}
