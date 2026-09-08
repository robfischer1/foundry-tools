package main

import (
	"context"

	"dagger/foundry-tools/internal/dagger"
)

// RustLane is the Rust namespace. Every atom here reports ABSENT and exits 0 on
// a repository with no Cargo.toml at its root — and says so.
type RustLane struct {
	// +private
	Source *dagger.Directory
}

// cargo fmt --all --check is clean.
//
// +check
func (r *RustLane) CargoFmt(ctx context.Context) (string, error) {
	return run(ctx, r.Source, "rust:cargo-fmt")
}

// cargo clippy is clean with warnings denied.
//
// +check
func (r *RustLane) CargoClippy(ctx context.Context) (string, error) {
	return run(ctx, r.Source, "rust:cargo-clippy")
}

// cargo test --workspace passes.
//
// +check
func (r *RustLane) CargoTest(ctx context.Context) (string, error) {
	return run(ctx, r.Source, "rust:cargo-test")
}

// cargo audit reports no known vulnerability. An absent cargo-audit is a
// provisioning failure — exit 2, never 0.
//
// +check
func (r *RustLane) CargoAudit(ctx context.Context) (string, error) {
	return run(ctx, r.Source, "rust:cargo-audit")
}
