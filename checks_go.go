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
	return run(ctx, g.Source, "go:gofmt")
}

// go vet ./... reports nothing.
//
// +check
func (g *GoLane) Vet(ctx context.Context) (string, error) {
	return run(ctx, g.Source, "go:vet")
}

// go build ./... succeeds.
//
// +check
func (g *GoLane) Build(ctx context.Context) (string, error) {
	return run(ctx, g.Source, "go:build")
}

// go test -race ./... passes.
//
// +check
func (g *GoLane) TestRace(ctx context.Context) (string, error) {
	return run(ctx, g.Source, "go:test-race")
}

// staticcheck ./... reports nothing. The toolchain is the module's, so an
// absent staticcheck is a provisioning failure — exit 2, never 0.
//
// +check
func (g *GoLane) Staticcheck(ctx context.Context) (string, error) {
	return run(ctx, g.Source, "go:staticcheck")
}

// govulncheck ./... reports no known vulnerability.
//
// +check
func (g *GoLane) Govulncheck(ctx context.Context) (string, error) {
	return run(ctx, g.Source, "go:govulncheck")
}
