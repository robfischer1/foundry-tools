// Package fleetkafkaflows is the atom fleet:kafka-flows: the Kafka topics the tree produces to and
// consumes from, recorded as findings. One file holds the atom, and both runners execute it (lld.md FN-085).
package fleetkafkaflows

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It runs in lead: the tree it reads is the pod's, and
// nothing else needs a toolchain.
var Def = atomkit.Def{
	ID: "fleet:kafka-flows", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "Records the Kafka topics this tree produces to and consumes from (and each consumer group) as findings, for nyx; never fails a tree for having flows or an unresolved name.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "fleet:kafka-flows"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// RECORDS, NEVER GRADES (slag-by-kind F13): the topics a tree produces to and
// consumes from, as findings beside the head's result, for nyx to compare with
// the broker. It passes on every tree it can read.
//
// check records the Kafka topics the tree produces to and consumes from
// (checks.KafkaFlowsVerdict). It passes on every tree it can read; only a tree
// that would not enumerate or read is a could-not-run.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	var wanted []string
	for _, p := range in.Files {
		if checks.KafkaSourceFile(p) {
			wanted = append(wanted, p)
		}
	}
	files, bad := checks.ReadAll(in.Tree().Read, wanted)
	if bad != "" {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - "+bad)
	}
	known, knownErr := checks.LazyKnownTopics(ctx, in.DoorOf())
	return checks.KafkaFlowsVerdict(row, files, known, knownErr)
}
