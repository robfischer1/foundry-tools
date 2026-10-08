package atoms

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// THE FLEET ATOMS THAT ASK THE DOOR. Each reads what the tree holds (a pin file,
// the Go it captures from) and hands it to the judgement in internal/checks,
// which asks the git door what the fleet holds. The door is Input.Door, so a test
// answers for it; a door that does not answer is 2 in the judgement itself.

// orbitDrift: this repo's declared seams agree with the canonical contracts. An
// orbit.toml is the surface; without one the repo declares no seams.
func orbitDrift(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	t := in.tree()
	entries, err := t.entries(".")
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(entries, "orbit.toml") {
		return checks.VerdictOf(a, int(checks.StatePass), "fleet:orbit-drift: ABSENT - no orbit.toml in this tree, so this repo declares no seams")
	}
	declared, err := t.read("orbit.toml")
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), "fleet:orbit-drift: CANNOT RUN - orbit.toml did not parse: "+err.Error())
	}
	state, report := checks.OrbitDrift(ctx, declared, in.door())
	return checks.VerdictOf(a, state, report)
}

// daggerLockstep: the dagger CLI, the engine and any dagger module in the tree
// are held to one version. The pin files are a literal list, so a tree that
// holds none reads nothing.
func daggerLockstep(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if in.FilesErr != nil {
		return cannotEnumerate(a, in.FilesErr)
	}
	paths := []string{checks.DaggerModuleManifest}
	for _, f := range checks.DaggerPinFiles {
		paths = append(paths, f.Path)
	}
	files := map[string]string{}
	for _, p := range paths {
		if !slices.Contains(in.Files, p) {
			continue
		}
		body, err := in.tree().read(p)
		if err != nil {
			return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("fleet:dagger-lockstep: CANNOT RUN - %s would not read: %v", p, err))
		}
		files[p] = body
	}
	state, report := checks.DaggerLockstep(ctx, files, in.door())
	return checks.VerdictOf(a, state, report)
}

// readAll reads every named file. A file that would not read is the answer: the
// first of them, sorted, with why, since a file never read is a file never
// judged.
func readAll(t tree, paths []string) (map[string]string, string) {
	out := make(map[string]string, len(paths))
	var failed []string
	for _, p := range paths {
		body, err := t.read(p)
		if err != nil {
			failed = append(failed, p+" ("+err.Error()+")")
			continue
		}
		out[p] = body
	}
	if len(failed) == 0 {
		return out, ""
	}
	sort.Strings(failed)
	return nil, failed[0] + " would not read"
}

// withSuffix is the files whose name ends in one of the suffixes, in order.
func withSuffix(files []string, suffixes ...string) []string {
	var out []string
	for _, f := range files {
		for _, s := range suffixes {
			if strings.HasSuffix(f, s) {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// nodeKindsDeclared: every node kind the tree's Go captures is declared in
// chaos. A tree that captures nothing never asks the door; a door that would
// not answer is CANNOT RUN, never a finding.
func nodeKindsDeclared(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if in.FilesErr != nil {
		return cannotEnumerate(a, in.FilesErr)
	}
	files, bad := readAll(in.tree(), withSuffix(in.Files, ".go", ".py", ".ts", ".tsx", ".rs"))
	if bad != "" {
		return checks.VerdictOf(a, int(checks.StateCannotRun), "fleet:node-kinds-declared: CANNOT RUN - "+bad)
	}
	note := func(state int) string {
		// The pass's honesty: this atom reads Go only, so a PASS names the
		// non-Go files that look like captures and were not read.
		if state != 0 {
			return ""
		}
		return checks.NonGoNote(checks.NonGoCaptureFiles(files))
	}
	if uses, _ := checks.CapturedKinds(files); len(uses) == 0 {
		state, report := checks.NodeKindsDeclared(files, "")
		return checks.VerdictOf(a, state, report+note(state))
	}
	schema, err := checks.FetchNodeKindsSchema(ctx, in.door())
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), "fleet:node-kinds-declared: CANNOT RUN - "+err.Error()+". A vocabulary that was not read declares nothing, and an undeclared kind is not a finding about this tree.")
	}
	state, report := checks.NodeKindsDeclared(files, schema)
	return checks.VerdictOf(a, state, report+note(state))
}

// consumedEventsEmitted: every event_type the tree consumes has an emitter
// somewhere in the fleet. The tree is read locally and the fleet only when it
// must be.
func consumedEventsEmitted(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if in.FilesErr != nil {
		return cannotEnumerate(a, in.FilesErr)
	}
	var wanted []string
	for _, p := range in.Files {
		if checks.EventSourceExt(p) {
			wanted = append(wanted, p)
		}
	}
	files, bad := readAll(in.tree(), wanted)
	if bad != "" {
		return checks.VerdictOf(a, int(checks.StateCannotRun), "fleet:consumed-events-emitted: CANNOT RUN - "+bad)
	}
	state, report := checks.ConsumedEventsEmitted(ctx, files, func(ctx context.Context, need []string) (map[string]string, error) {
		return checks.FleetEmitters(ctx, in.door(), "", need)
	})
	return checks.VerdictOf(a, state, report)
}
