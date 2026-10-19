// Package witreplay is the body rust:wit-compose and dev:rust-replay share: build
// the replay host, compose every wit-guest world through tools/compose/compose.sh,
// and replay the tapes the crate carries. The gate atom and the dev atom differ in
// their row (id, stage) and in how the dev one reports; the steps, their order and
// their reasons are this package's, so the two cannot disagree about what a
// replay is.
package witreplay

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Run is the body of rust:wit-compose and dev:rust-replay (R36: one body, two
// atoms). Every guest composes into ONE component and every tape the crate
// carries for a world replays through it with the answers the native core gave.
// row names the atom answering: its id leads every reason, and its stage and
// lane stamp the verdict.
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
func Run(ctx context.Context, in atomkit.In, row checks.AtomDef) checks.Verdict {
	id := row.ID
	// THE FACADE (checks.WitFacadeDir): the root crate, or the member of a
	// virtual workspace that declares the wit-guest features. Every path below
	// (composer, replay host, tapes) is resolved beside it and run from it.
	facade, bad := atoms.ResolveWitFacade(ctx, in, row)
	if bad != nil {
		return *bad
	}
	manifest := facade.Manifest
	if len(checks.WitGuestWorlds(manifest)) == 0 {
		if facade.Virtual {
			return checks.VerdictOf(row, 0, id+": ABSENT - Cargo.toml is a virtual workspace and no member declares a "+checks.WitGuestPrefix+"<world> feature, so there is nothing to compose")
		}
		return checks.VerdictOf(row, 0, id+": ABSENT - Cargo.toml declares no "+checks.WitGuestPrefix+"<world> feature, so there is nothing to compose")
	}
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	// Files the facade owns, named relative to it (the tree lists them relative to the root).
	var files []string
	for _, f := range in.Files {
		if rel, ok := facade.Within(f); ok {
			files = append(files, rel)
		}
	}
	if !slices.Contains(files, checks.WitComposeScript) || !slices.Contains(files, checks.WitReplayManifest) {
		return checks.VerdictOf(row, 0, id+": ABSENT - the tree carries no "+facade.Rel(checks.WitComposeScript)+" and "+facade.Rel(checks.WitReplayManifest)+", so it declares no composition to check")
	}
	tapes := checks.WitComposeTapes(manifest, files)
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

	crate := facade.Abs(in.Root)
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
		v = atoms.SettleTool(ctx, in, row, atoms.Cmd{Dir: crate, Name: step.args[0], Args: step.args[1:], Env: env})
		if v.State == int(checks.StatePass) && step.then != nil {
			if err := step.then(); err != nil {
				v = checks.VerdictOf(row, 126, fmt.Sprintf("%s: the %s would not copy out of the target: %v", id, step.name, err))
			}
		}
		if v.State != int(checks.StatePass) {
			return namedStep(v, id, step.name)
		}
	}
	return v
}

// namedStep stamps a failing step's verdict with the step that produced it.
func namedStep(v checks.Verdict, id, step string) checks.Verdict {
	v.Reason = fmt.Sprintf("%s [%s]%s", id, step, strings.TrimPrefix(v.Reason, id))
	v.Logs = slices.Insert(v.Logs, 0, "failed at "+step)
	return v
}
