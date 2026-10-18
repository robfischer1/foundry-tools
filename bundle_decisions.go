package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// decisions derives foundry-dies' decision table (stark42 A1 S4) and lays it
// into the tree the policy die is built from, as data.grants.decisions. It runs
// after the policy's own suite — the suite mocks data.grants piecemeal, and a
// table derived from the real grants would answer those mocks wrongly — and
// before the die is built, so both twins and every gate after read the same
// table.
//
// THE ROSTER is the catalog's, staged exactly as the fleet die stages it:
// fleet/data.json, fleet/derive.rego and the catalog entries as
// data.fleet.declared. The table reads only each star's verb_prefix from it,
// records the prefix it was derived under, and is not consulted for a star
// whose live row names another; flux decides only which stars are deployed,
// which decide checks live. So the table is a function of this commit alone,
// and a fleet or flux change can never make it wrong, only unused for a star.
//
// A tree without the derive ships no table and nothing changes: decide answers
// by the rules. A table someone committed is refused: it would be served as if
// derived.
func (l *bundleLane) decisions(ctx context.Context, tools *dagger.Container) (*dagger.Container, gateResult) {
	src := l.m.Source
	present, g := probe(ctx, src, bundlelane.DecisionsDerive, bundlelane.DecisionsPath, "fleet/data.json", "fleet/derive.rego")
	if g.code != buildlane.Clean {
		return nil, g
	}
	if !present[bundlelane.DecisionsDerive] {
		bundleSay("policy: no %s — the die carries no decision table; decide answers by the rules", bundlelane.DecisionsDerive)
		return tools, clean()
	}
	if present[bundlelane.DecisionsPath] {
		return nil, findings(bundlelane.DecisionsPath + " is committed, and the lane derives it — a committed table would be served as if derived from this commit")
	}
	for _, f := range []string{"fleet/data.json", "fleet/derive.rego"} {
		if !present[f] {
			return nil, findings(f + " is absent — the decision table's roster cannot be staged")
		}
	}
	bundleSay("── policy: derive the decision table ──")
	roster, g := l.decisionsRoster(ctx)
	if g.code != buildlane.Clean {
		return nil, g
	}
	stage := workDir + "/" + bundlelane.DecisionsStage
	key, err := l.decisionsKey(ctx, roster)
	if err != nil {
		return nil, couldNotRun("the decision table's inputs could not be digested: %v", err)
	}
	ctr := tools.WithDirectory(stage, roster).
		WithMountedCache(bundlelane.DecisionsCacheDir, dag.CacheVolume(bundlelane.DecisionsCacheVolume), dagger.ContainerWithMountedCacheOpts{Sharing: dagger.CacheSharingModeShared}).
		WithEnvVariable("DECISIONS_CACHE", bundlelane.DecisionsCacheDir).
		WithEnvVariable("DECISIONS_KEY", key)
	began := time.Now()
	out, code, g := exec(ctx, ctr, "the decision table's derive", bundlelane.DecisionsCachedArgs(stage)...)
	took := time.Since(began)
	if g.code != buildlane.Clean {
		return nil, g
	}
	if code != 0 {
		return nil, findings("the decision table did not derive:\n" + out)
	}
	stats, err := bundlelane.Decisions(out)
	if err != nil {
		return nil, findings(err.Error())
	}
	bundleSay("policy: decision table derived in %.1fs — %s", took.Seconds(), stats)
	return tools.WithMountedDirectory("/src", src.WithNewFile(bundlelane.DecisionsPath, out)), clean()
}

// decisionsKey is the content digest the derived table is cached by: the
// directories the derive loads (the policy, the derive, the staged roster), the
// query it asks, and the pinned opa that evaluates them. The table is a function of those alone, so
// a commit that changes none of them (the base side of a pull, a docs commit)
// reuses the table an earlier run derived.
func (l *bundleLane) decisionsKey(ctx context.Context, roster *dagger.Directory) (string, error) {
	h := sha256.New()
	h.Write([]byte(checks.OpaURL + "\n"))
	h.Write([]byte(strings.Join(bundlelane.DecisionsArgs(bundlelane.DecisionsStage), "\x00") + "\n"))
	for _, d := range []*dagger.Directory{l.m.Source.Directory("policy"), l.m.Source.Directory(bundlelane.DecisionsDeriveDir), roster} {
		dg, err := d.Digest(ctx)
		if err != nil {
			return "", err
		}
		h.Write([]byte(strings.TrimSpace(dg) + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// decisionsRoster is the catalog roster the derive reads: fleet/data.json and
// fleet/derive.rego as committed, and the catalog entries staged as
// data.fleet.declared the way inject stages them for the fleet die.
func (l *bundleLane) decisionsRoster(ctx context.Context) (*dagger.Directory, gateResult) {
	src := l.m.Source
	entries, g := readGlob(ctx, src, bundlelane.DeclaredGlob)
	if g.code != buildlane.Clean {
		return nil, g
	}
	docs, err := bundlelane.Declared(entries)
	if err != nil {
		return nil, findings(err.Error())
	}
	return dag.Directory().
		WithFile("fleet/data.json", src.File("fleet/data.json")).
		WithFile("fleet/derive.rego", src.File("fleet/derive.rego")).
		WithNewFile(bundlelane.DeclaredPath, bundlelane.Stage(docs)), clean()
}

// probe answers which of paths the tree carries; a tree that cannot be read
// is a could-not-run.
func probe(ctx context.Context, src *dagger.Directory, paths ...string) (map[string]bool, gateResult) {
	present := map[string]bool{}
	for _, p := range paths {
		found, err := src.Glob(ctx, p)
		if err != nil {
			return nil, couldNotRun("%s could not be probed: %v", p, err)
		}
		present[p] = len(found) > 0
	}
	return present, clean()
}
