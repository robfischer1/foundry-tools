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

// Every changed .py/.go source file is shown to the code witness (narcissus),
// never a test: a canonical-class or Standard match is a finding, a Convention
// is advisory, novel is clean. Reads GATE_BASE for the change set; a local run
// diffs the tip against its parent and needs the in-cluster port to answer at
// all.
//
// +check
func (f *Fleet) Witness(ctx context.Context) (string, error) {
	return check(ctx, f.Source, "fleet:witness")
}

// Every container in flux's prime/star-*.yaml that arms the stellar-core-go
// policy PEP (STELLAR_PEP_MODE not off) also sets STELLAR_PDP_URL. Unset, the
// PEP records would_consult{pdp_not_consulted}, consults nothing and, in
// witness mode, refuses nothing - which is how themis ran for days.
//
// +check
func (f *Fleet) PdpWired(ctx context.Context) (string, error) {
	return check(ctx, f.Source, "fleet:pdp-wired")
}

// The dagger CLI, engine and module pins in this tree agree with the engine
// foundry/flux runs. CLI and engine move in lockstep and the engine leads: in
// flux the four engine pins must agree, elsewhere a CLI pin must equal flux
// main's engine and a module's engineVersion must not be newer than it.
//
// +check
func (f *Fleet) DaggerLockstep(ctx context.Context) (string, error) {
	return check(ctx, f.Source, "fleet:dagger-lockstep")
}

// The Renovate image this repo pins (ImageRenovate, the validator behind
// fleet:renovate-config) equals the one foundry/flux deploys. Renovate cannot
// open one pull across two repositories, so flux leads and this follows: in
// flux both CronJob containers must carry one ref, elsewhere ImageRenovate
// must equal flux main's.
//
// +check
func (f *Fleet) RenovateLockstep(ctx context.Context) (string, error) {
	return check(ctx, f.Source, "fleet:renovate-lockstep")
}

// Every node kind this repo's Go code captures is declared in chaos's
// node_kinds. nodes.kind is a foreign key onto that table, so an undeclared
// kind is refused at runtime and only there: a star's own tests mint against a
// fake graph that accepts anything. Read from the DDL seed through the door.
//
// +check
func (f *Fleet) NodeKindsDeclared(ctx context.Context) (string, error) {
	return check(ctx, f.Source, "fleet:node-kinds-declared")
}

// Every event_type this repo consumes has at least one live emitter somewhere
// in the fleet. A reader of an event nobody writes goes silent without an
// error; the fleet is scanned through the door only for a type this repo does
// not itself emit.
//
// +check
func (f *Fleet) ConsumedEventsEmitted(ctx context.Context) (string, error) {
	return check(ctx, f.Source, "fleet:consumed-events-emitted")
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
