package main

import (
	"context"

	"dagger/foundry-tools/internal/dagger"
)

// Fleet is the cross-lane namespace: atoms every repository runs, whatever it
// is written in.
type Fleet struct {
	// +private
	Source *dagger.Directory
}

// Every changed .py/.go file is shown to the code witness (narcissus): a
// canonical-class or Standard match is a finding, a Convention is advisory,
// novel is clean. Reads GATE_BASE for the change set; a local run diffs the
// tip against its parent and needs the in-cluster port to answer at all.
//
// +check
func (f *Fleet) Witness(ctx context.Context) (string, error) {
	return check(ctx, f.Source, "fleet:witness")
}

// This repo's declared seams still agree with the canonical contracts in
// foundry-dies/orbits. Compares a CONTENT DIGEST, not a version integer; an
// edge that pins nothing is a finding, and a door it could not reach is a
// cannot-run.
//
// +check
func (f *Fleet) OrbitDrift(ctx context.Context) (string, error) {
	return check(ctx, f.Source, "fleet:orbit-drift")
}

// Every YAML file in the tree parses.
//
// +check
func (f *Fleet) CheckYaml(ctx context.Context) (string, error) {
	return check(ctx, f.Source, "fleet:check-yaml")
}

// No file in the tree exceeds 500 KB.
//
// +check
func (f *Fleet) CheckAddedLargeFiles(ctx context.Context) (string, error) {
	return check(ctx, f.Source, "fleet:check-added-large-files")
}

// No conflict markers were committed.
//
// +check
func (f *Fleet) CheckMergeConflict(ctx context.Context) (string, error) {
	return check(ctx, f.Source, "fleet:check-merge-conflict")
}

// No silent suppression of any gate — a suppression is a claim that the tool is
// wrong, and it carries a tool-conflict line saying what disagrees.
//
// +check
func (f *Fleet) StopJustifications(ctx context.Context) (string, error) {
	return check(ctx, f.Source, "fleet:stop-justifications")
}

// The SAST ruleset declares every lane this repository actually builds — the
// partial-scan case the zero-file refusal structurally cannot see.
//
// +check
func (f *Fleet) SastRulesetLanes(ctx context.Context) (string, error) {
	return check(ctx, f.Source, "fleet:sast-ruleset-lanes")
}

// SAST scan that refuses a zero-file scan: 0 findings over 0 files means
// nothing was examined, not that the code is clean.
//
// +check
func (f *Fleet) OpengrepSast(ctx context.Context) (string, error) {
	return check(ctx, f.Source, "fleet:opengrep-sast")
}

// Every Dockerfile in the tree passes hadolint under the fleet's ruleset: the
// repository's own .hadolint.yaml is not read, inline `# hadolint ignore=`
// pragmas beside a reason are honoured, and a tree with no Dockerfile is
// ABSENT rather than a pass.
//
// +check
func (f *Fleet) Hadolint(ctx context.Context) (string, error) {
	return check(ctx, f.Source, "fleet:hadolint")
}
