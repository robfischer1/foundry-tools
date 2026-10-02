package main

import (
	"context"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// sourceAt is the tree New binds when the door names a repo and a commit: the
// commit checked out, detached, with its WHOLE history in .git and origin set
// to repo — what GitRef.tree(depth: -1) answered, fetched so that the second
// commit of a repository costs what moved rather than the repository again.
//
// WHAT READS THE HISTORY, AND HOW MUCH OF IT. This list is the specification
// for what the fetch must carry; a change to any atom's history needs changes
// it.
//
//   - fleet:witness, go/python/rust/ts:mutation, ops:immutable — the change
//     set: run.withBase fetches the door's base sha into the checkout, then
//     run.changeBase asks `git merge-base <base> HEAD`. HEAD's history must
//     reach the merge base, which is as deep as the pull's branch point — any
//     number of commits, so no fixed depth is correct.
//   - fleet:witness on a tip (no base) — HEAD^ (rev-parse HEAD^, diff
//     HEAD^..HEAD): two commits.
//   - build's detect (build.go detectWhere) — the last published build's
//     commit, read off the image label: present, an ancestor of HEAD
//     (merge-base --is-ancestor), and diffed to HEAD. As deep as the last
//     build, which for a quiet base image is months. Absent, the lane builds:
//     safe, but every skipped build becomes a real one.
//   - build's publishTip — HEAD's commit time (log -1): one commit.
//   - bundle's changed — Sha^1: two commits. Absent, both dies publish.
//   - Tree, the dies bundle — HEAD^{tree}, rev-parse HEAD: one commit.
//   - stop-justifications, fleet:witness — `git remote get-url origin`, which
//     is repo by construction here.
//   - sweep:template-render-matrix — copier at --vcs-ref=HEAD: one commit.
//   - Tags: nothing reads one (no describe, no rev-list --count); none are
//     fetched, as before.
//
// SO THE HISTORY STAYS WHOLE, AND WHAT CHANGES IS HOW IT ARRIVES. Measured
// 2026-10-02 against the door from the dev box and against the cluster engine
// (the CI pipeline audit's lever D):
//
//   - A depth-bounded fetch is SLOWER at this door, not faster: a shallow pack
//     is hand-encoded and never cached, and a shallow client's haves buy it
//     nothing (ourea gittransport v2.go, ourea#2645) — chaos depth 2 on the
//     engine 8.6-9.0s every time, against 0.6s for a commit already held.
//     And every diff-scoped atom above would need a deepen-to-merge-base step
//     whose failure is exactly the silent "no base" this must never become.
//   - The engine's own git mirror fetches each new commit as a bare sha into
//     a repository that keeps no refs, so git has nothing to negotiate from
//     and the door sends the whole repository every time: chaos 17.9, 19.4,
//     19.5s for three consecutive commits on one warm mirror. That is the
//     heavy tail (p90 13.8s gate, 24.9s build).
//   - The same fetch from a history that keeps ONE ref: chaos 2.6s against
//     18.1s cold, ourea 3.6 against 34.9, mnemosyne 1.7 against 13.7, infra
//     11.8 against 48.0 (eight commits apart, from the dev box).
//
// So the history lives in a cache volume per repository (checks.SourceCacheKey)
// that keeps checks.SourceFetchRef, the fetch announces it, and the checkout is
// a local copy out of it.
//
// LOCKED, because the volume is one git repository and two lanes of one repo
// can fetch at once (a gate and a build of the same coordinate): git's own
// locks cover a ref, not a gc racing a fetch. The engine holds the lock for
// one exec at a time, so a second lane waits seconds, as it already waited on
// the engine's own per-remote mirror lock.
//
// THE CHECKOUT NEVER POINTS INTO THE VOLUME. --no-hardlinks copies the objects
// out (a linked or alternate-borrowing checkout would carry a path that does
// not exist in the snapshot the atoms mount); the volume is a cache, and the
// tree the atoms grade must not change when the cache does.
//
// A FAILED FETCH IS THE ENGINE'S ERROR, as GitRef.tree's was: the chain never
// evaluates, every atom reads it as could-not-run and Tree refuses. Nothing
// here answers an empty tree.
//
// The engine caches the chain by its arguments, so a second lane on the same
// commit and engine — the mutation lane behind its gate, the build beside it —
// copies nothing; the fetch inside is a no-op when the commit is already held.
func sourceAt(repo, sha string) *dagger.Directory {
	const cache, out = "/cache/src.git", "/out"
	return (&run{}).laneBase(checks.ImageFleet).
		WithMountedCache("/cache", dag.CacheVolume(checks.SourceCacheKey(repo)), dagger.ContainerWithMountedCacheOpts{
			Sharing: dagger.CacheSharingModeLocked,
		}).
		// Idempotent: on a warm volume this reinitialises nothing it holds.
		WithExec([]string{"git", "init", "-q", "--bare", cache}).
		// The volume is never pruned by anyone else, so the fetch keeps it:
		// its own auto-maintenance repacks when loose objects or packs pile up,
		// and objects no longer reachable from the one ref expire after git's
		// two weeks. IN THE FOREGROUND, so the engine's lock covers it — a gc
		// detached past the end of the exec would race the next lane's fetch.
		WithExec([]string{"git", "-C", cache, "-c", "gc.autoDetach=false", "-c", "maintenance.autoDetach=false",
			"fetch", "--quiet", "--no-tags", repo, "+" + sha + ":" + checks.SourceFetchRef}).
		WithExec([]string{"git", "clone", "--quiet", "--no-checkout", "--no-hardlinks", "--no-tags", cache, out}).
		WithExec([]string{"git", "-C", out, "-c", "advice.detachedHead=false", "checkout", "--quiet", "--detach", sha}).
		WithExec([]string{"git", "-C", out, "remote", "set-url", "origin", repo}).
		Directory(out)
}

// missingBase is how a diff-scoped atom settles when the door named a base and
// changeBase found it nowhere in the history: the state and the reason.
//
// ON A TREE THE DOOR HAD FETCHED, IT IS A COULD-NOT-RUN. The checkout carries
// the commit's whole history and withBase fetched the base into it by sha, so
// a base still missing is a broken fetch — and an atom that read it as "no
// base, nothing to scope to" would pass the pull having checked nothing. Only
// a local snapshot with no history to reach the base (a hook that named no
// origin) stands down, as it always has, under the atom's own words.
func (r *run) missingBase(standDown string) (int, string) {
	if r.repo != "" {
		return 2, "CANNOT RUN - the base " + r.base + " is not in this history, though the door named it and it was fetched — a change set with no origin is not one nothing touched"
	}
	return 0, standDown
}

// originURL answers `git remote get-url origin` for the tree under check, as
// output() would: the URL, git's exit code, and the engine's error.
//
// THE DOOR'S TREE NEEDS NO EXEC TO SAY IT. sourceAt sets origin to the repo
// it fetched from, by construction, so r.repo IS the answer — and the exec
// that asked ran once per atom per tree (71 times in 63 sampled gates, 102
// exec-seconds — the CI pipeline audit, lever F). A caller's own tree, or a
// snapshot whose origin gitReady reconstructed, is still asked.
func (r *run) originURL(ctx context.Context, ctr *dagger.Container) (string, int, error) {
	if r.repo != "" {
		return r.repo, 0, nil
	}
	return output(ctx, ctr.WithExec([]string{"git", "remote", "get-url", "origin"}, anyExit))
}

// gitSystemConfig is git's system-scope config file in every lane image —
// all four are Debian builds of git (golang:bookworm and rust:bookworm ship
// it, python:slim and bun:slim get it from apt in provision), whose sysconfdir
// is /etc. System scope is "protected configuration", the only kind git reads
// safe.directory from besides global and the command line.
const gitSystemConfig = "/etc/gitconfig"

// safeDirectoryConfig is `git config --global --add safe.directory '*'`, as a
// file the engine writes rather than a process it starts. The exec was
// keyed on the tree mounted before it, so it RAN — container and all — once
// per git-reading atom per tree: 122 times in 63 sampled gates, 200
// exec-seconds, up to 11.6s for one (the CI pipeline audit, lever F). A file
// write is a layer, not a container. The effect is the one the exec had:
// every git in the container trusts every directory.
const safeDirectoryConfig = "[safe]\n\tdirectory = *\n"
