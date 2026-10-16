package main

import (
	"context"
	"strconv"
	"time"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE ENVIRONMENTS OF THE FLEET ATOMS that are not the plain lane or the tools
// container (atom_env.go holds the table). Each says which container and nothing
// more: the question is in the atom's own file.

// renovateLane is the Renovate image the fleet deploys, as root. The image runs
// as a user who can make neither /work (where the atom writes its verdict) nor a
// repository in /src; the validator reads the config and writes nothing.
func renovateLane(r *run) *dagger.Container {
	return r.lane(checks.ImageRenovate).WithUser("root")
}

// witnessRun keys the witness's exec to this run: an ask is never answered from
// the engine's cache, and an earlier verdict never stands in for a later one.
func witnessRun(_ context.Context, _ *run, ctr *dagger.Container) *dagger.Container {
	return ctr.WithEnvVariable("WITNESS_RUN", strconv.FormatInt(time.Now().UnixNano(), 10))
}
