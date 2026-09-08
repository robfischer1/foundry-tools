package main

import (
	"context"

	"dagger/foundry-tools/internal/dagger"
)

// TSLane is the TypeScript namespace. Every atom here reports ABSENT and exits
// 0 on a repository with no package.json at its root — and says so.
//
// lint-staged is deliberately not here. It is defined over the git INDEX — the
// set of staged files — and the engine receives a directory, not an index. An
// atom claiming to be lint-staged would be checking a different population than
// the hook it replaced, which is the silent-drift failure this module exists to
// end. It stays under pre-commit's runner at the commit boundary, where the
// index exists.
type TSLane struct {
	// +private
	Source *dagger.Directory
}

// bun run gate (format, lint, typecheck, test, build) passes.
//
// +check
func (t *TSLane) BunGateCommit(ctx context.Context) (string, error) {
	return run(ctx, t.Source, "ts:bun-gate-commit")
}

// bun run gate passes against a frozen lockfile. A lockfile that will not
// install frozen is a CANNOT RUN, not a finding.
//
// +check
func (t *TSLane) BunGate(ctx context.Context) (string, error) {
	return run(ctx, t.Source, "ts:bun-gate")
}

// bun audit reports nothing at high or above.
//
// +check
func (t *TSLane) BunAudit(ctx context.Context) (string, error) {
	return run(ctx, t.Source, "ts:bun-audit")
}
