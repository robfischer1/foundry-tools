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

// Every YAML file in the tree parses.
//
// +check
func (f *Fleet) CheckYaml(ctx context.Context) (string, error) {
	return run(ctx, f.Source, "fleet:check-yaml")
}

// No file in the tree exceeds 500 KB.
//
// +check
func (f *Fleet) CheckAddedLargeFiles(ctx context.Context) (string, error) {
	return run(ctx, f.Source, "fleet:check-added-large-files")
}

// No conflict markers were committed.
//
// +check
func (f *Fleet) CheckMergeConflict(ctx context.Context) (string, error) {
	return run(ctx, f.Source, "fleet:check-merge-conflict")
}

// No new secret against the repository's .secrets.baseline. A missing baseline
// is a CANNOT RUN, never a pass.
//
// +check
func (f *Fleet) DetectSecrets(ctx context.Context) (string, error) {
	return run(ctx, f.Source, "fleet:detect-secrets")
}

// No silent suppression of any gate — a suppression is a claim that the tool is
// wrong, and it carries a tool-conflict line saying what disagrees.
//
// +check
func (f *Fleet) StopJustifications(ctx context.Context) (string, error) {
	return run(ctx, f.Source, "fleet:stop-justifications")
}

// The SAST ruleset declares every lane this repository actually builds — the
// partial-scan case the zero-file refusal structurally cannot see.
//
// +check
func (f *Fleet) SastRulesetLanes(ctx context.Context) (string, error) {
	return run(ctx, f.Source, "fleet:sast-ruleset-lanes")
}

// SAST scan that refuses a zero-file scan: 0 findings over 0 files means
// nothing was examined, not that the code is clean.
//
// +check
func (f *Fleet) OpengrepSast(ctx context.Context) (string, error) {
	return run(ctx, f.Source, "fleet:opengrep-sast")
}
