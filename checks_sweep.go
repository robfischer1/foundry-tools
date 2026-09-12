package main

import (
	"context"

	"dagger/foundry-tools/internal/dagger"
)

// Sweep is the repo-cadence namespace — the checks that describe a REPOSITORY
// rather than a change.
//
// NOTHING HERE BELONGS IN A PULL'S PATH, and that is the acceptance criterion
// of CA F9 rather than a stylistic preference. Two properties make it true:
//
//   - Every atom below carries checks.StageSweep, and `Verdicts` with no stage
//     answers the PULL-PATH vector — precommit and prepush — so a gate that
//     asks for "the vector" cannot be handed one of these by omission.
//   - The namespace is its own, so `dagger check go: python:` and
//     `dagger check --stage=prepush` reach none of it. Only `dagger check
//     sweep:` does, and the one caller that runs it is the ca-sweep CronJob.
//
// The reason they run on a clock is measured, not aesthetic: these answers
// cannot differ between two pulls against the same repo, and a repository
// nobody opens a PR against is never evaluated at all. digest-pins is the
// worked example — both outages it exists for were caused by an image being
// REBUILT, with no merge anywhere near the repo that broke.
type Sweep struct {
	// +private
	Source *dagger.Directory
}

// Every image digest this repo's workflows pin still resolves in the registry.
// A pin the registry has collected fails every star that uses it, ~200ms into
// the job, with an error naming the registry rather than the pin.
//
// +check
func (s *Sweep) DigestPins(ctx context.Context) (string, error) {
	return check(ctx, s.Source, "sweep:digest-pins")
}

// A repository that builds an image builds it through the workflow that
// attests its SBOM — the weekly portfolio re-score reads attestations, so an
// unattested image is not scored badly, it is not scored at all.
//
// +check
func (s *Sweep) PortfolioSbom(ctx context.Context) (string, error) {
	return check(ctx, s.Source, "sweep:portfolio-sbom")
}

// Every case in this template's ci-matrix.toml still renders. A template bug
// does not break the template; it breaks the next repo stamped from it.
//
// +check
func (s *Sweep) TemplateRenderMatrix(ctx context.Context) (string, error) {
	return check(ctx, s.Source, "sweep:template-render-matrix")
}

// Every manifest under flux/ validates against its Kubernetes schema, with the
// CRD catalogue probed first so an unreachable catalogue is a CANNOT RUN
// rather than four hundred silent skips.
//
// +check
func (s *Sweep) Kubeconform(ctx context.Context) (string, error) {
	return check(ctx, s.Source, "sweep:kubeconform")
}

// Every workload under flux/ passes kube-linter's default checks.
//
// +check
func (s *Sweep) KubeLinter(ctx context.Context) (string, error) {
	return check(ctx, s.Source, "sweep:kube-linter")
}
