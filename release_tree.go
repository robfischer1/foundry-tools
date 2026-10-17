package main

import (
	"context"

	"dagger/foundry-tools/internal/dagger"
)

// runTree is the run's tree as releasekit reads it: the engine's directory
// and the fleet's records. It is the Dagger side of releasekit.Tree, whose pod
// side is releasekit.Disk; the question each release asks of the tree is in
// internal/releasekit and is the same for both.
type runTree struct{ r *run }

func (t runTree) Population(ctx context.Context, patterns ...string) ([]string, error) {
	return t.r.population(ctx, patterns...)
}

func (t runTree) File(ctx context.Context, rel string) (string, error) {
	return t.r.src.File(rel).Contents(ctx)
}

// Present is fileIfPresent: the SDK answers false alongside any error, so an
// unreadable file is an absent one, as the chains read it.
func (t runTree) Present(ctx context.Context, rel string) (string, bool) {
	body, present, _ := fileIfPresent(ctx, t.r.src, rel)
	return body, present
}

func (t runTree) Entries(ctx context.Context) ([]string, error) { return t.r.src.Entries(ctx) }

func (t runTree) IsDir(ctx context.Context, rel string) (bool, error) {
	return t.r.src.Exists(ctx, rel, dagger.DirectoryExistsOpts{ExpectedType: dagger.ExistsTypeDirectoryType})
}

func (t runTree) RecordPaths(ctx context.Context) ([]string, error) {
	return t.r.dies.Glob(ctx, "fleet/repos/*.json")
}

func (t runTree) Record(ctx context.Context, rel string) (string, error) {
	return t.r.dies.File(rel).Contents(ctx)
}
