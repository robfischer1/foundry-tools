package main

import (
	"context"

	"dagger/foundry-tools/internal/dagger"
)

// PythonLane is the Python namespace. Every atom here reports ABSENT and exits
// 0 on a repository with no pyproject.toml at its root — and says so.
//
// pyright is deliberately not here. Two type checkers ran on every python
// pre-push with no incident behind the duplication; one type checker, mypy
// --strict, is the ratified end state.
type PythonLane struct {
	// +private
	Source *dagger.Directory
}

// ruff lint is clean over every .py in the tree.
//
// +check
func (p *PythonLane) RuffCheck(ctx context.Context) (string, error) {
	return run(ctx, p.Source, "python:ruff-check")
}

// ruff format --check is clean over the product python. It reports, never
// rewrites: a formatter that rewrites a tree mid-commit has aborted a commit in
// this fleet before.
//
// +check
func (p *PythonLane) RuffFormat(ctx context.Context) (string, error) {
	return run(ctx, p.Source, "python:ruff-format")
}

// No assertion-free test bodies (forge-testkit).
//
// +check
func (p *PythonLane) ForgeTestkitAssertionFree(ctx context.Context) (string, error) {
	return run(ctx, p.Source, "python:forge-testkit-assertion-free")
}

// Fake and Stub doubles live where they belong (forge-testkit).
//
// +check
func (p *PythonLane) ForgeTestkitFakePlacement(ctx context.Context) (string, error) {
	return run(ctx, p.Source, "python:forge-testkit-fake-placement")
}

// MCP verb descriptions stay inside the schema budget (forge-testkit).
//
// +check
func (p *PythonLane) ForgeTestkitSchemaBudget(ctx context.Context) (string, error) {
	return run(ctx, p.Source, "python:forge-testkit-schema-budget")
}

// mypy --strict is clean over src and tests. One type checker, not two.
//
// +check
func (p *PythonLane) Mypy(ctx context.Context) (string, error) {
	return run(ctx, p.Source, "python:mypy")
}

// pytest passes.
//
// +check
func (p *PythonLane) Pytest(ctx context.Context) (string, error) {
	return run(ctx, p.Source, "python:pytest")
}

// pip-audit reports no known vulnerability.
//
// +check
func (p *PythonLane) PipAudit(ctx context.Context) (string, error) {
	return run(ctx, p.Source, "python:pip-audit")
}

// This pull's changed critical modules survive no mutant: the mutation gate,
// diff-scoped against GATE_BASE, run as the door's `mutation` lane beside the
// gate rather than inside it. Reads critical_modules from .copier-answers.yml
// and is ABSENT where none are declared.
//
// +check
func (p *PythonLane) Mutation(ctx context.Context) (string, error) {
	return run(ctx, p.Source, "python:mutation")
}
