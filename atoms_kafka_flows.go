package main

import (
	"context"
	"errors"
	"fmt"

	"dagger/foundry-tools/internal/checks"
)

func init() {
	register("fleet:kafka-flows", fleetKafkaFlows)
}

// fleetKafkaFlows is fleet:kafka-flows' chain, the binary's comparator: the
// same read and the same judgement (checks.KafkaFlowsVerdict) over the
// engine's copy of the tree.
func fleetKafkaFlows(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("fleet:kafka-flows")
	files, err := r.kafkaSources(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - "+err.Error())
	}
	known, knownErr := checks.LazyKnownTopics(ctx, oureaDoor)
	return checks.KafkaFlowsVerdict(a, files, known, knownErr)
}

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
