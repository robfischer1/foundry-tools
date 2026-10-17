package main

import (
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE ENVIRONMENT OF THE DIES ATOMS (atoms/dies/<name>). Environment only, as
// atom_env.go says: which container, and what it carries. The question each
// atom asks is in its own file.
//
// The fleet lane image (python, with uv) is the default of the python atoms and
// of the ones that need no tool. The five that grade the policy with opa run in
// that image with the pinned opa laid on PATH, the way the chain that preceded
// them provisioned it; the atom itself proves the binary is the pinned one.
// dies:grants-stamp judges the change, so it is handed the pull's base.

func init() {
	opa := atomEnv{container: func(r *run) *dagger.Container { return withPinnedOpa(r.lane(checks.ImageFleet)) }}
	for _, id := range []string{"dies:opa-test", "dies:admission-dogfood", "dies:data-keys", "dies:canary-visibility", "dies:authz-grid"} {
		atomEnvironments[id] = opa
	}
	atomEnvironments["dies:grants-stamp"] = atomEnv{container: func(r *run) *dagger.Container { return r.lane(checks.ImageFleet) }, facts: true}
}

// withPinnedOpa puts opa on PATH from its pinned release asset, VERIFIED BY THE
// ENGINE AT FETCH TIME (pinnedFile): the mirror first, the release URL second,
// both held to the one sha256. IT FAILS CLOSED: a pin with no sum gets an exec
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
