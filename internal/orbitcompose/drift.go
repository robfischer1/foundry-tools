package orbitcompose

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// THE DRIFT CHECK: whether a rendered directory still matches what its
// contracts compose to, with no disk or engine involved.
//
// WHY IT EXISTS. orbitcompose renders flux's prime/orbits from
// foundry-dies/orbits, but nothing runs it. A contract change reaches a pod
// only if someone remembers to re-render (Task #104.1). This is the check
// that refuses to let that go unnoticed: it reads both directories as
// they stand and says exactly which files a re-render would write or remove.
//
// SELF-CONTAINED ON PURPOSE. Its inputs are two maps of file name to bytes
// and its answer is a state and a report, so a gate atom (ops:orbit-composed)
// carries it today and a dedicated orbit lane can carry the same function
// later without unpicking it from another check.

// ErrNoContract is ContractsOf's answer for a set holding no .toml file.
var ErrNoContract = errors.New("holds no contract (*.toml)")

// ContractsOf parses contract files keyed by file name, in name order. A name
// that is not a .toml file is not a contract (the README); none at all is an
// error.
func ContractsOf(files map[string][]byte) ([]Contract, error) {
	var names []string
	for name := range files {
		if strings.HasSuffix(name, ".toml") {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, ErrNoContract
	}
	slices.Sort(names)
	out := make([]Contract, 0, len(names))
	for _, name := range names {
		c, err := ParseContract(name, files[name])
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// The drift check's three answers, in the gate's own vocabulary.
const (
	DriftNone     = 0 // the directory is the composed set
	DriftFound    = 1 // a re-render would change it, or it holds a file the composer does not own
	DriftCannotGo = 2 // the contracts themselves do not compose
)

// Drift is the check's answer: the gate state, and the lines a reader needs.
type Drift struct {
	State  int
	Report string
}

// CheckDrift compares the rendered directory have with what contracts
// compose to under namespace.
//
// A DIRECTORY THAT IS NOT THE COMPOSED SET IS A FINDING, and so is a file in
// it the composer does not own. Both are faults in the rendered tree and its
// committer's to fix. CONTRACTS THAT DO NOT PARSE ARE A COULD-NOT-RUN: the
// rendered tree cannot be judged against a source that does not compose,
// and the source repository's own gate is where that red belongs.
func CheckDrift(contracts, have map[string][]byte, namespace string) Drift {
	cs, err := ContractsOf(contracts)
	if err != nil {
		return Drift{State: DriftCannotGo, Report: "the contracts do not compose: " + err.Error()}
	}
	c, err := Plan(Files(namespace, Compose(cs)), have)
	if err != nil {
		return Drift{State: DriftFound, Report: err.Error()}
	}
	if c.Empty() {
		return Drift{State: DriftNone, Report: fmt.Sprintf("the directory is the composed set of %d contract(s)", len(cs))}
	}
	return Drift{State: DriftFound, Report: "stale against its contracts — a re-render would:\n" + Report(c) + "\n" + RenderHint}
}

// RenderHint is how a person brings the directory back, as the line the
// finding ends with.
const RenderHint = "re-render: go run ./orbitcompose -contracts <foundry-dies>/orbits -out <flux>/prime/orbits (in foundry-tools), and land the result in flux"
