package main

import (
	"context"

	"dagger/foundry-tools/internal/dagger"
)

// Compose is the host-stacks namespace: the checks that were the act-runner's
// `validate.yml` in nas01-stacks and llm01-stacks.
//
// THOSE REPOS ARE THE BOXES. Every compose spec, the Caddyfile, the runner
// config — and until that workflow landed, nothing validated any of it: a
// malformed spec or a committed credential could reach main unreviewed, on a
// delivery path measured in seconds. The runner it ran on is being removed, so
// these atoms are what "validated by the gate or not at all" means there.
//
// THE SURFACE IS THE CONDITION, not the repo name. A repository that tracks a
// compose spec gets all three; one that tracks none reports ABSENT and says so.
// That keeps the atoms honest about the fleet as it actually is rather than
// about a list of two repositories somebody would have to maintain — measured
// 2026-09-10 across the checkouts in custody, four repos track a compose.yaml
// and the rest do not.
type Compose struct {
	// +private
	Source *dagger.Directory
}

// Every tracked compose spec parses and its schema validates, with the
// gitignored env_file targets stubbed first and --no-interpolate set so a
// deploy-time ${VAR:?} is not read as a CI failure.
//
// +check
func (c *Compose) Config(ctx context.Context) (string, error) {
	return check(ctx, c.Source, "compose:config")
}

// No credential-shaped file is tracked. The .gitignore states the rule; this
// asserts the outcome, because an ignore rule only protects files it was
// written before.
//
// +check
func (c *Compose) NoTrackedSecrets(ctx context.Context) (string, error) {
	return check(ctx, c.Source, "compose:no-tracked-secrets")
}

// Zero ${PIN_} image interpolations anywhere — the BP6b ratchet, kept as a
// COUNT gate because a count stays closed where a list reopens.
//
// +check
func (c *Compose) ThirdPartyPins(ctx context.Context) (string, error) {
	return check(ctx, c.Source, "compose:third-party-pins")
}
