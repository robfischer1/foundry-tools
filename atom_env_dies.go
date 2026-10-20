package main

import (
	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE ENVIRONMENT OF THE DIES ATOMS (atoms/dies/<name>). Environment only, as
// atom_env.go says: which container, and what it carries. The question each
// atom asks is in its own file.
//
// The fleet lane image (python, with uv) is the default of the python atoms and
// of the ones that need no tool. The four that grade the policy with opa run in
// that image with the pinned opa laid on PATH, the way the chain that preceded
// them provisioned it; the atom itself proves the binary is the pinned one.
// dies:grants-stamp judges the change, so it is handed the pull's base, and so
// is dies:render-author, which reads who made the pull's commits since it.

func init() {
	opa := atomEnv{container: func(r *run) *dagger.Container { return withPinnedOpa(r.lane(checks.ImageFleet)) }}
	for _, id := range []string{"dies:opa-test", "dies:admission-dogfood", "dies:data-keys", "dies:canary-visibility"} {
		atomEnvironments[id] = opa
	}
	// dies:authz-grid judges the change too: its Rego against the base's, so it is handed the base.
	// It derives the decision table on both sides of a Rego pull, so it is handed the
	// table cache the bundle lane keeps (bundlelane.DecisionsCacheVolume): the checker
	// keeps each derived table under the digest of its inputs (tools/authz_table_stage.py,
	// AUTHZ_TABLE_CACHE), the base side reuses the table main's run derived, and a
	// pull derives each distinct input set once.
	atomEnvironments["dies:authz-grid"] = atomEnv{container: func(r *run) *dagger.Container {
		return opa.container(r).
			WithMountedCache(bundlelane.DecisionsCacheDir, dag.CacheVolume(bundlelane.DecisionsCacheVolume), dagger.ContainerWithMountedCacheOpts{Sharing: dagger.CacheSharingModeShared}).
			WithEnvVariable("AUTHZ_TABLE_CACHE", bundlelane.DecisionsCacheDir)
	}, facts: true}
	atomEnvironments["dies:grants-stamp"] = atomEnv{container: func(r *run) *dagger.Container { return r.lane(checks.ImageFleet) }, facts: true}
	// dies:render-author asks git only, as ops:immutable does: the tools container.
	atomEnvironments["dies:render-author"] = atomEnv{tools: true, facts: true}
}

// withPinnedOpa puts opa on PATH from its pinned release asset, VERIFIED BY THE
// ENGINE AT FETCH TIME (pinnedFile): the release URL, held to its sha256. IT FAILS CLOSED: a pin with no sum gets an exec
// that cannot succeed, never an unverified fetch.
func withPinnedOpa(ctr *dagger.Container) *dagger.Container {
	return withPinnedOpaSums(ctr, checks.ToolSHA256)
}

// withPinnedOpaSums is withPinnedOpa over a named set of pinned sums, so the
// fail-closed branch can be held by a test with a set that lacks the pin.
func withPinnedOpaSums(ctr *dagger.Container, sums map[string]string) *dagger.Container {
	f, ok := pinnedFile(sums, checks.OpaURL)
	if !ok {
		return ctr.WithExec([]string{"/bin/false"}) // no checksum pinned: the lane must not provision it
	}
	return ctr.WithFile("/usr/local/bin/opa", f, dagger.ContainerWithFileOpts{Permissions: 0o755})
}
