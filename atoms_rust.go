package main

import (
	"context"
	"strings"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE RUST LANE, AS TYPED CHAINS. Read runtime.go's eight rules and
// atoms_go.go's exemplar first; this file follows both.
//
// THE DEPENDENCIES ARE FETCHED ONCE, ON THEIR OWN (cargoDeps), and every
// atom that compiles branches from that layer. The first cut had no
// provisioning step — cargo resolves its registry as part of the command —
// and it was wrong in a way only the engine could show: MEASURED 2026-09-12 on
// tongs, rust:cargo-clippy and rust:cargo-test running concurrently against
// the shared registry volume, cargo-test answered "failed to download
// `deranged v0.5.8`" while clippy fetched the same crate. cargo's
// package-cache lock lived in CARGO_HOME, which the mounts did not cover, so
// two containers did not see each other's lock (checks.CargoHome is the fix:
// one volume holds the home, lock included). One fetch, cached as a layer
// keyed on the tree, and the parallel atoms only read.
//
// The fetch is also rule 1 of runtime.go: a registry outage is a could-not-run
// the door re-asks, not a finding the committer is told to fix.

func init() {
	register("rust:release", rustRelease)
}

// cargoVerdict is verdict() with CARGO'S EXIT VOCABULARY TRANSLATED FIRST
// (checks.CargoExit says why: cargo answers 101, not 1, when the command
// failed, and StateFor reads a 101 as could-not-run). The shell bodies this
// ports all ended `|| exit 1`, mapping every non-zero cargo code to FINDINGS;
// this is that mapping narrowed to the one code it existed for, in a pure
// function with a table under it, which is runtime.go's rule 2.
func cargoVerdict(ctx context.Context, a checks.AtomDef, ctr *dagger.Container) checks.Verdict {
	out, code, err := output(ctx, ctr)
	if err != nil {
		return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
	}
	return checks.VerdictOf(a, checks.CargoExit(code), out)
}

// cargoDeps is the rust lane's provisioned base: the lane container with the
// dependency graph fetched. See the file header for the measurement behind it.
//
// --locked, AND IT IS THE GATE'S ONLY VIEW OF THE LOCK. Every lock-dependent
// atom branches from here, so this one flag is where a stale Cargo.lock gets
// caught -- at the fetch, before a single crate compiles.
//
// WITHOUT IT THE GATE AND THE CAST DISAGREED ABOUT WHAT THEY WERE BUILDING.
// The cast's release build has always passed --locked
// (`cargo build --release --locked -p <pkg>`, checks.RustReleaseArgs),
// correctly: a release must build from the RECORDED graph, not from whatever
// resolves today. The gate passed nothing, so cargo silently re-resolved,
// wrote a lock the container then threw away, and reported green. A stale lock
// was therefore invisible to every pre-merge signal and fatal to the one lane
// that ships.
//
// MEASURED, 2026-09-29. renovate moves a MANIFEST's git dependency without
// regenerating the lock -- it regenerates for registry deps and not for these
// -- and the result landed on main three times before anyone looked: cerberus
// (rob/cerberus#96), anvil and bellows. cerberus's cast was red for every
// landing that day while `app/cerberus:stable` sat at one bundle behind, so
// main advanced and nothing reached a host.
//
// THE COST IS A TWO-STEP ON A GIT-DEP BUMP, and it is the right cost: such a
// PR now reds its own gate until the lock is regenerated, which is simply the
// gate noticing that the change is incomplete. Blast radius was measured
// before landing this -- `cargo fetch --locked` across all nine rust stars,
// two stale, both repaired first -- so no star reds on the change itself.
func (r *run) cargoDeps() *dagger.Container {
	return cargoFetch(r.lane(checks.ImageRust))
}

// cargoFetch sweeps stale git-db locks, then fetches. The sweep runs under
// cargo's own flock (checks.StaleCargoLocks), so it cannot race a live fetch;
// the fetch is the only step that takes the shared CARGO_HOME lock for long,
// so it is the only step that serialises across containers. Nothing here
// mounts LOCKED: that would hold the volume for whole build execs and queue
// gates past the 5m watchdog.
func cargoFetch(ctr *dagger.Container) *dagger.Container {
	return ctr.WithExec(checks.StaleCargoLocks()).WithExec([]string{"cargo", "fetch", "--locked"})
}

// cargoFresh is cargoDeps for the chains that build into a shared target — the
// release build, the wit guest and the dev surface — with the tree dated past
// every artifact in it (checks.Unstale), so cargo rebuilds the workspace's own
// crates from THIS tree. (Clippy and test, now files, date the tree themselves:
// atoms.RustStamp.) The mutation lane compiles into target dirs of its own and
// branches from cargoDeps.
func (r *run) cargoFresh() *dagger.Container {
	return stampFresh(cargoBounded(r.cargoDeps()))
}

// cargoBounded is the gate's memory and CPU bound for the chains that COMPILE
// (release, wit-guest), set AFTER the fetch so the fetch layer stays shared. Two
// knobs, both cargo's own environment (checks.CargoBoundedEnv carries the
// measurement; the atoms that moved into files set the same pair themselves).
// The mutation atom has its own caps (rustMutantCores) and sets them after this,
// so its four-job cap wins.
func cargoBounded(ctr *dagger.Container) *dagger.Container {
	return withEnvPairs(ctr, checks.CargoBoundedEnv())
}

// withEnvPairs sets each KEY=VALUE on the container.
func withEnvPairs(ctr *dagger.Container, pairs []string) *dagger.Container {
	for _, p := range pairs {
		k, v, _ := strings.Cut(p, "=")
		ctr = ctr.WithEnvVariable(k, v)
	}
	return ctr
}

// stampFresh dates the tree at /src past every artifact (checks.Unstale).
//
// IT MUST RUN ON THE /src THAT CARGO WILL READ. The stamp is an exec's write
// into the mounted tree, and a later WithMountedDirectory("/src") replaces
// that tree with the unstamped one: gitReady does exactly that for a linked
// worktree, which is where every author's `just check` comes from. Stamped
// before the swap, cargo-test built against files the engine hands over with
// normalised mtimes, found the shared per-repo target's artifact "newer", and
// linked a test binary whose include_str! manifest was the previous tree's
// (stellar-core-rust tests/claim_composition, 2026-10-12, three authors;
// touching a file cannot help, the upload drops the stamp). So a chain that
// re-mounts /src stamps AFTER it.
func stampFresh(ctr *dagger.Container) *dagger.Container {
	return ctr.WithExec(checks.Unstale())
}

// rustMutationLane is the rust lane with the tree mounted, plus the two tools
// only rust:mutation runs (provisionRustMutation): the gate's other rust atoms
// never wait on, or pay for, cargo-mutants and cargo-nextest. It is the
// atom's environment (atom_env.go), with the shared cargo target dropped: the
// copies cargo-mutants makes each build in a target of their own.
func (r *run) rustMutationLane() *dagger.Container {
	return r.laneBaseWith(checks.ImageRust, provisionRustMutation).
		WithMountedDirectory("/src", r.src).
		WithWorkdir("/src").
		WithoutEnvVariable("CARGO_TARGET_DIR").
		WithoutMount("/cache/cargo-target").
		WithExec(checks.StaleCargoLocks())
}

// THE RELEASE BUILD, AND IT IS THE ONE THE IMAGE CARRIES — go:release's
// contract for the Rust lane (atoms_go.go carries the reasoning; this file
// follows it). The Gate compiles the star's binary with the release profile,
// the engine caches that exec by its inputs, and F14's Build asks Release()
// for the same directory and gets the compile it already paid for.
//
// WHAT IT BUILDS IS DERIVED, NOT DECLARED: `cargo build --release -p <star>`
// from a workspace whose binary crate is named for the star, which is the one
// shape the fleet's Rust Dockerfiles and rust-repo-template pour
// (checks.RustReleasePlan carries the measurement). A record speaks only for
// a repo that ships more than its own name.
//
// THE THREE ABSENCES ARE go:release's, IN ITS ORDER: no tracked Dockerfile
// (no image, no release build); no star (a repo with Dockerfiles and no
// .copier-answers.yml builds through other lanes); a Dockerfile that compiles
// itself (buildlane.CopiesRelease is false, so the build lane builds it as
// before and the fleet flips one star at a time).
//
// THE EXIT IS READ AFTER EACH BINARY, in cargo's vocabulary (cargoVerdict:
// 101 is findings, not could-not-run), so a binary that did not compile is
// reported with the compiler's own words rather than by the one after it.
func rustRelease(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("rust:release")
	files, err := r.population(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the tree could not be read: "+err.Error())
	}
	if len(checks.DockerfilePopulation(files)) == 0 {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - this repository tracks no Dockerfile or Containerfile, so it ships no image and has no release build")
	}
	star, err := r.starName(ctx)
	if err != nil && !isNotAStar(err) {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - "+err.Error())
	}
	if err != nil {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - "+err.Error()+", so this is not a star image and there is no star release build")
	}
	asking, dockerfile, err := r.releaseDockerfile(ctx, checks.DockerfilePopulation(files))
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - "+err.Error())
	}
	if asking == "" {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - no tracked Dockerfile copies from "+buildlane.ReleaseDir+"/, so this image compiles itself: its build is the build lane's, and there is no release build to make here")
	}
	plan, why := rustReleasePlan(star, dockerfile)
	if why != "" {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - "+why)
	}
	_, v := r.rustReleaseBuild(ctx, a, plan)
	v.Reason = checks.ReleaseScope(plan) + "\n" + v.Reason
	return v
}

// rustReleasePlan derives what the star's release build produces: the
// binaries its Dockerfile copies out of release/, each the workspace package
// of that name. A COPY with no workspace package behind it fails `cargo build
// -p` as a finding about the tree. Every refusal is about the repository and
// settles as a could-not-run.
func rustReleasePlan(star, dockerfile string) (checks.ReleasePlan, string) {
	plan, err := checks.RustReleasePlan(star, buildlane.ReleaseCopies(dockerfile))
	if err != nil {
		return checks.ReleasePlan{}, err.Error()
	}
	return plan, ""
}

// rustReleaseBuild is the compile itself: the provisioned lane under the
// Unstale stamp (cargoFresh), the target directory on the repository's release
// cache volume (checks.RustReleaseTarget says why), one exec per binary that
// copies the binary out of the volume as it finishes. It answers the container
// the binaries are in and the verdict of the last exec that ran — the first
// failure stops the chain, so the verdict names the binary that did not build.
//
// THE STAMP IS WHAT MAKES THE VOLUME SAFE. Every tree of a repo mounts at
// /src, so the workspace's crates have one artifact each in the volume, and
// cargo judges them by mtime: without Unstale a later tree links what an
// earlier one built (checks.Unstale has the measurement). Under it the
// workspace rebuilds and only the registry crates come back Fresh.
func (r *run) rustReleaseBuild(ctx context.Context, a checks.AtomDef, plan checks.ReleasePlan) (*dagger.Container, checks.Verdict) {
	ctr := r.withReleaseCache(r.cargoFresh()).WithEnvVariable("CARGO_TARGET_DIR", checks.RustReleaseTarget)
	v := checks.VerdictOf(a, 0, "")
	for _, b := range plan.Binaries {
		ctr = ctr.WithExec(copiedOut(checks.RustReleaseArgs(b), checks.RustReleaseBuilt(b)+"="+checks.RustReleaseBinary(b)), anyExit)
		if v = cargoVerdict(ctx, a, ctr); v.State != 0 {
			return ctr, v
		}
	}
	return ctr, v
}

// rustReleaseDir is the release build's binaries as a directory — what F14
// copies onto the base image: each binary read from where its build copied
// it out of the volume (checks.RustReleaseBinary), under its own name at the
// root. Lazy, as the
// Go path's ctr.Directory(ReleaseOut) is: a binary the build did not leave
// (a record naming a package that builds no [[bin]]) surfaces where the
// directory is first used — the image build's context — as the engine's own
// "no such file" for that path, which the build lane files as findings.
func rustReleaseDir(ctr *dagger.Container, plan checks.ReleasePlan) *dagger.Directory {
	out := dag.Directory()
	for _, b := range plan.Binaries {
		out = out.WithFile(b.Name, ctr.File(checks.RustReleaseBinary(b)))
	}
	return out
}
