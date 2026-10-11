package atoms

import (
	"context"

	"dagger/foundry-tools/internal/checks"
)

// kafkaFlows: the Kafka topics the tree produces to and consumes from, recorded
// as findings (checks.KafkaFlowsVerdict). It passes on every tree it can read;
// only a tree that would not enumerate or read is a could-not-run.
func kafkaFlows(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if in.FilesErr != nil {
		return cannotEnumerate(a, in.FilesErr)
	}
	var wanted []string
	for _, p := range in.Files {
		if checks.KafkaSourceFile(p) {
			wanted = append(wanted, p)
		}
	}
	files, bad := readAll(in.tree(), wanted)
	if bad != "" {
		return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - "+bad)
	}
	known, knownErr := checks.LazyKnownTopics(ctx, in.door())
	return checks.KafkaFlowsVerdict(a, files, known, knownErr)
}
