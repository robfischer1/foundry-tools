package main

import (
	"context"
	"errors"
	"fmt"

	"dagger/foundry-tools/internal/checks"
)

// kafkaSources reads every file the flow extraction reads (checks.
// KafkaSourceFile). A tree that would not enumerate, or a file that would not
// read, is the error: a file never read is a flow never seen.
func (r *run) kafkaSources(ctx context.Context) (map[string]string, error) {
	paths, err := r.population(ctx, "**")
	if err != nil {
		return nil, fmt.Errorf("the tree would not enumerate: %w", err)
	}
	var wanted []string
	for _, p := range paths {
		if checks.KafkaSourceFile(p) {
			wanted = append(wanted, p)
		}
	}
	files, bad := readOK(readFiles(ctx, r.src, wanted))
	if bad != "" {
		return nil, errors.New(bad)
	}
	return files, nil
}
