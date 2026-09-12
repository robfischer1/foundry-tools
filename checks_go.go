package main

import (
	"context"

	"dagger/foundry-tools/internal/dagger"
)

// GoLane is the Go namespace. Every atom here reports ABSENT and exits 0 on a
// repository with no go.mod at its root — and says so, rather than passing in
// silence.
type GoLane struct {
	// +private
	Source *dagger.Directory
}

// Every Go file is gofmt-clean.
//
// +check
func (g *GoLane) Gofmt(ctx context.Context) (string, error) {
	return check(ctx, g.Source, "go:gofmt")
}

// go vet ./... reports nothing.
//
// +check
func (g *GoLane) Vet(ctx context.Context) (string, error) {
	return check(ctx, g.Source, "go:vet")
}

// go build ./... succeeds.
//
// +check
func (g *GoLane) Build(ctx context.Context) (string, error) {
	return check(ctx, g.Source, "go:build")
}

// go test -race ./... passes.
//
// +check
func (g *GoLane) TestRace(ctx context.Context) (string, error) {
	return check(ctx, g.Source, "go:test-race")
}

// staticcheck ./... reports nothing. The toolchain is the module's, so an
// absent staticcheck is a provisioning failure — exit 2, never 0.
//
// +check
func (g *GoLane) Staticcheck(ctx context.Context) (string, error) {
	return check(ctx, g.Source, "go:staticcheck")
}

// govulncheck ./... reports no known vulnerability.
//
// +check
func (g *GoLane) Govulncheck(ctx context.Context) (string, error) {
	return check(ctx, g.Source, "go:govulncheck")
}

// This pull's changed Go survive no mutant: the mutation gate,
// diff-scoped against GATE_BASE, run as the door's `mutation` lane beside the
// gate rather than inside it. Reads critical_modules from .copier-answers.yml
// and is ABSENT where none are declared.
//
// +check
func (g *GoLane) Mutation(ctx context.Context) (string, error) {
	return check(ctx, g.Source, "go:mutation")
}
