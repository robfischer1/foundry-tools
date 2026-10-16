// Package orbitkit is what the ported orbit atoms (atoms/orbit/<name>) and the
// two ops atoms that render the orbit sidecars (ops:orbit-composed and
// ops:orbit-sidecars) share: the trees they read, the foundry-dies contracts and
// roster, and the verdicts they settle.
package orbitkit

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/orbitcompose"
	"dagger/foundry-tools/internal/orbitlane"
)

// THE ORBIT ATOMS that read trees: the repo under test, and foundry-dies at main
// (Input.Dies). The judgements are internal/orbitlane's and internal/orbitcompose's;
// this file reads the two trees and hands them over. REPORT-ONLY until
// orbitlane.Enforce flips.

// OrbitVerdict settles an atom's findings into its vector element.
func OrbitVerdict(a checks.AtomDef, found []checks.Finding) checks.Verdict {
	state, reason, settled := orbitlane.Settle(a.ID, found)
	v := checks.VerdictOf(a, state, reason)
	v.Findings = settled
	return v
}

// OrbitAbsent is an atom with no surface in this tree, saying why.
func OrbitAbsent(a checks.AtomDef, why string) checks.Verdict {
	return checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - "+why)
}

// OrbitCannot is an atom that could not look.
func OrbitCannot(a checks.AtomDef, err error) checks.Verdict {
	return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - "+err.Error())
}

// errNoDies is what an atom that needs foundry-dies says when it was not handed
// one: a contract nobody read agrees with nothing.
var errNoDies = errors.New("foundry-dies main was not supplied to the atoms binary (-dies)")

// Dies is the foundry-dies checkout, or why there is none.
func Dies(in atoms.Input) (atoms.Tree, error) {
	if in.Dies == "" {
		return atoms.Tree{}, errNoDies
	}
	return atoms.Input{Root: in.Dies}.Tree(), nil
}

// FilesByName reads every file a pattern matches, keyed by base name, as the
// composer reads a directory. A DIRECTORY IS NAMED, NOT READ: nil bytes make it
// a file the composer does not own, which CheckDrift reports by name, rather
// than a read that fails.
func FilesByName(t atoms.Tree, pattern string) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, p := range t.Glob(pattern) {
		if strings.HasSuffix(p, "/") {
			out[path.Base(p)] = nil
			continue
		}
		raw, err := t.Read(p)
		if err != nil {
			return nil, err
		}
		out[path.Base(p)] = []byte(raw)
	}
	return out, nil
}

// FilesIn is FilesByName naming the pattern it could not read.
func FilesIn(t atoms.Tree, pattern string) (map[string][]byte, error) {
	out, err := FilesByName(t, pattern)
	if err != nil {
		return nil, fmt.Errorf("%s could not be read: %v", pattern, err)
	}
	return out, nil
}

// HasContract reports whether a directory's files hold any contract.
func HasContract(files map[string][]byte) bool {
	for name := range files {
		if strings.HasSuffix(name, ".toml") {
			return true
		}
	}
	return false
}

// Roster answers each repo on a tree's fleet catalog (fleet/repos/<name>.json)
// and its verb prefix. A file that is not JSON still names a repo by its file
// name; its prefix is unknown, so no gateway name is attributed through it.
func Roster(t atoms.Tree) (map[string]string, error) {
	out := map[string]string{}
	for _, p := range t.Glob("fleet/repos/*.json") {
		body, err := t.Read(p)
		if err != nil {
			return nil, fmt.Errorf("%s could not be read: %v", p, err)
		}
		var star struct {
			VerbPrefix string `json:"verb_prefix"`
		}
		_ = json.Unmarshal([]byte(body), &star)
		out[strings.TrimSuffix(path.Base(p), ".json")] = star.VerbPrefix
	}
	return out, nil
}

// StarSet is a roster as the set of its star names; none at all is nil, the
// two-part file-name rule alone.
func StarSet(prefixes map[string]string) map[string]bool {
	if len(prefixes) == 0 {
		return nil
	}
	out := make(map[string]bool, len(prefixes))
	for s := range prefixes {
		out[s] = true
	}
	return out
}

// StarOf is the repository's name: the last path element of its origin.
func StarOf(repo string) string { return path.Base(strings.TrimSuffix(repo, ".git")) }

// FleetContracts answers the contracts on foundry-dies main that parse, and a
// finding for each that does not: one bad file is a finding about that file,
// never the directory's could-not-run.
func FleetContracts(in atoms.Input) ([]orbitcompose.Contract, []checks.Finding, error) {
	cs, bad, _, err := FleetContractsAndRoster(in)
	return cs, bad, err
}

// FleetContractsAndRoster is FleetContracts with the roster's verb prefixes it
// read on the way, for the atom that attributes a verb through them.
func FleetContractsAndRoster(in atoms.Input) ([]orbitcompose.Contract, []checks.Finding, map[string]string, error) {
	dies, err := Dies(in)
	if err != nil {
		return nil, nil, nil, err
	}
	files, err := FilesIn(dies, "orbits/*.toml")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("foundry-dies: %v", err)
	}
	prefixes, err := Roster(dies)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("foundry-dies: %v", err)
	}
	cs, bad, err := orbitcompose.ParseAll(files, StarSet(prefixes))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("foundry-dies/orbits does not compose: %v", err)
	}
	return cs, orbitlane.Unreadable(bad), prefixes, nil
}
