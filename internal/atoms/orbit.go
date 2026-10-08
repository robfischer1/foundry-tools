package atoms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/orbitcompose"
	"dagger/foundry-tools/internal/orbitlane"
)

// THE ORBIT LANE'S ATOMS that read trees and not a code analyzer (orbit:surface
// runs narcissus's `narc` and stays a chain). The judgements are
// internal/orbitlane's; this file reads the two trees (the repo under test, and
// foundry-dies at main, Input.Dies) and hands them over. REPORT-ONLY until
// orbitlane.Enforce flips, exactly as the chains are.

// orbitVerdict settles an atom's findings into its vector element.
func orbitVerdict(a checks.AtomDef, found []checks.Finding) checks.Verdict {
	state, reason, settled := orbitlane.Settle(a.ID, found)
	v := checks.VerdictOf(a, state, reason)
	v.Findings = settled
	return v
}

// orbitAbsent is an atom with no surface in this tree, saying why.
func orbitAbsent(a checks.AtomDef, why string) checks.Verdict {
	return checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - "+why)
}

// orbitCannot is an atom that could not look.
func orbitCannot(a checks.AtomDef, err error) checks.Verdict {
	return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - "+err.Error())
}

// errNoDies is what an atom that needs foundry-dies says when the binary was not
// handed it: a contract nobody read agrees with nothing.
var errNoDies = errors.New("foundry-dies main was not supplied to the atoms binary (-dies)")

// dies is the foundry-dies checkout, or why there is none.
func (in Input) dies() (tree, error) {
	if in.Dies == "" {
		return tree{}, errNoDies
	}
	return tree{root: in.Dies}, nil
}

// filesByName reads every file a pattern matches, keyed by base name, as the
// composer reads a directory. A DIRECTORY IS NAMED, NOT READ: nil bytes make it
// a file the composer does not own, which CheckDrift reports by name, rather
// than a read that fails.
func filesByName(t tree, pattern string) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, p := range t.glob(pattern) {
		if strings.HasSuffix(p, "/") {
			out[path.Base(p)] = nil
			continue
		}
		raw, err := t.read(p)
		if err != nil {
			return nil, err
		}
		out[path.Base(p)] = []byte(raw)
	}
	return out, nil
}

// filesIn is filesByName naming the pattern it could not read.
func filesIn(t tree, pattern string) (map[string][]byte, error) {
	out, err := filesByName(t, pattern)
	if err != nil {
		return nil, fmt.Errorf("%s could not be read: %v", pattern, err)
	}
	return out, nil
}

// hasContract reports whether a directory's files hold any contract.
func hasContract(files map[string][]byte) bool {
	for name := range files {
		if strings.HasSuffix(name, ".toml") {
			return true
		}
	}
	return false
}

// roster answers each star on a tree's fleet roster and its verb prefix. A shard
// that is not JSON still names a star by its directory; its prefix is unknown,
// so no gateway name is attributed through it.
func roster(t tree) (map[string]string, error) {
	out := map[string]string{}
	for _, p := range t.glob("fleet/stars/*/data.json") {
		body, err := t.read(p)
		if err != nil {
			return nil, fmt.Errorf("%s could not be read: %v", p, err)
		}
		var star struct {
			VerbPrefix string `json:"verb_prefix"`
		}
		_ = json.Unmarshal([]byte(body), &star)
		out[path.Base(path.Dir(p))] = star.VerbPrefix
	}
	return out, nil
}

// starSet is a roster as the set of its star names; none at all is nil, the
// two-part file-name rule alone.
func starSet(prefixes map[string]string) map[string]bool {
	if len(prefixes) == 0 {
		return nil
	}
	out := make(map[string]bool, len(prefixes))
	for s := range prefixes {
		out[s] = true
	}
	return out
}

// starOf is the repository's name: the last path element of its origin.
func starOf(repo string) string { return path.Base(strings.TrimSuffix(repo, ".git")) }

// fleetContracts answers the contracts on foundry-dies main that parse, and a
// finding for each that does not: one bad file is a finding about that file,
// never the directory's could-not-run.
func fleetContracts(in Input) ([]orbitcompose.Contract, []checks.Finding, error) {
	dies, err := in.dies()
	if err != nil {
		return nil, nil, err
	}
	files, err := filesIn(dies, "orbits/*.toml")
	if err != nil {
		return nil, nil, fmt.Errorf("foundry-dies: %v", err)
	}
	prefixes, err := roster(dies)
	if err != nil {
		return nil, nil, fmt.Errorf("foundry-dies: %v", err)
	}
	cs, bad, err := orbitcompose.ParseAll(files, starSet(prefixes))
	if err != nil {
		return nil, nil, fmt.Errorf("foundry-dies/orbits does not compose: %v", err)
	}
	return cs, orbitlane.Unreadable(bad), nil
}

// orbitContracts: every contract parses, names two stars on the roster, carries
// a known status, and an approved one moved its version with its verbs (against
// foundry-dies main, the branch the pull merges into).
func orbitContracts(_ context.Context, a checks.AtomDef, in Input) checks.Verdict {
	const notHere = "this tree is not the contracts' repository (it needs orbits/*.toml beside fleet/stars/)"
	t := in.tree()
	files, err := filesIn(t, "orbits/*")
	if err != nil {
		return orbitCannot(a, err)
	}
	if !hasContract(files) {
		return orbitAbsent(a, notHere)
	}
	stars, err := roster(t)
	if err != nil {
		return orbitCannot(a, err)
	}
	if len(stars) == 0 {
		return orbitAbsent(a, notHere)
	}
	dies, err := in.dies()
	if err != nil {
		return orbitCannot(a, fmt.Errorf("foundry-dies main: %v", err))
	}
	base, err := filesIn(dies, "orbits/*.toml")
	if err != nil {
		return orbitCannot(a, fmt.Errorf("foundry-dies main: %v", err))
	}
	return orbitVerdict(a, orbitlane.Contracts(files, base, starSet(stars)))
}

// orbitRepo: a laid orbit.toml is what the contracts compose to for this star
// (fleet:orbit-drift's digest comparison, plus the edge set).
func orbitRepo(_ context.Context, a checks.AtomDef, in Input) checks.Verdict {
	t := in.tree()
	entries, err := t.entries(".")
	if err != nil {
		return orbitCannot(a, err)
	}
	if !checks.HasEntry(entries, "orbit.toml") {
		return orbitAbsent(a, "this repository carries no root orbit.toml (it is laid from the data/orbits die)")
	}
	laid, err := t.read("orbit.toml")
	if err != nil {
		return orbitCannot(a, err)
	}
	cs, bad, err := fleetContracts(in)
	if err != nil {
		return orbitCannot(a, err)
	}
	return orbitVerdict(a, append(orbitlane.Repo(starOf(in.Origin), []byte(laid), cs), bad...))
}

// oneLine is a reason folded onto one line, for a finding's detail.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// orbitSidecars: what the contracts compose to is what flux carries, and it
// parses with the reader a star refuses to boot without. On a flux tree it is
// the two gate atoms that already say so (ops:orbit-composed's -check and
// ops:orbit-sidecars' policy.ParseACL), run here as findings.
func orbitSidecars(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if len(in.tree().glob("prime/orbits/*"+orbitcompose.SidecarSuffix)) == 0 {
		return orbitAbsent(a, "this tree renders no orbit sidecars (no prime/orbits/*.orbit.toml)")
	}
	var found []checks.Finding
	for _, sub := range []struct {
		id  string
		run RunFunc
	}{{"ops:orbit-composed", opsOrbitComposed}, {"ops:orbit-sidecars", opsOrbitSidecars}} {
		v := sub.run(ctx, checks.AtomByID(sub.id), in)
		switch v.State {
		case int(checks.StateCannotRun):
			return orbitCannot(a, fmt.Errorf("%s could not run: %s", sub.id, v.Reason))
		case int(checks.StateFindings):
			found = append(found, checks.Finding{Verdict: checks.VerdictViolated, Subject: "prime/orbits", Cause: sub.id, Detail: oneLine(v.Reason)})
		default:
			found = append(found, checks.Finding{Verdict: checks.VerdictHolds, Subject: "prime/orbits", Cause: sub.id, Detail: oneLine(v.Reason)})
		}
	}
	return orbitVerdict(a, found)
}
