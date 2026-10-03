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

// Unreadable is a contract file the composer cannot read, and why. Err names
// the file.
type Unreadable struct {
	File string
	Err  error
}

// ParseAll parses every contract file in files, in name order, and answers
// the ones that parse and, apart, the ones that do not: one bad file is a
// finding about that file, never the whole directory's refusal. A name that
// is not a .toml file is not a contract (the README); none at all is
// ErrNoContract. stars is SplitName's roster.
func ParseAll(files map[string][]byte, stars map[string]bool) (good []Contract, bad []Unreadable, err error) {
	var names []string
	for name := range files {
		if strings.HasSuffix(name, ".toml") {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, nil, ErrNoContract
	}
	slices.Sort(names)
	for _, name := range names {
		c, err := ParseContract(name, files[name], stars)
		if err != nil {
			bad = append(bad, Unreadable{File: name, Err: err})
			continue
		}
		good = append(good, c)
	}
	return good, bad, nil
}

// JoinUnreadable is every unreadable file's reason as one error, nil for
// none: the write path's refusal, which names each file at once rather than
// the first.
func JoinUnreadable(bad []Unreadable) error {
	if len(bad) == 0 {
		return nil
	}
	reasons := make([]string, len(bad))
	for i, b := range bad {
		reasons[i] = b.Err.Error()
	}
	return errors.New(strings.Join(reasons, "; "))
}

// ContractsOf parses contract files keyed by file name, in name order, and
// refuses them all when any one does not parse: what a WRITE needs, since a
// partial composition would delete the sidecars of the contracts it skipped.
// The lane's read path is ParseAll.
func ContractsOf(files map[string][]byte, stars map[string]bool) ([]Contract, error) {
	good, bad, err := ParseAll(files, stars)
	if err != nil {
		return nil, err
	}
	if err := JoinUnreadable(bad); err != nil {
		return nil, err
	}
	return good, nil
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
// compose to under namespace. stars is SplitName's roster.
//
// A DIRECTORY THAT IS NOT THE COMPOSED SET IS A FINDING, and so is a file in
// it the composer does not own. Both are faults in the rendered tree and its
// committer's to fix. A CONTRACT THAT DOES NOT PARSE IS ITS OWN FINDING
// (contract-unparseable, naming the file): the readable contracts still
// compose and are still compared, so one bad file never takes the directory's
// verdict from every other. Only a directory with no contract at all is a
// could-not-run — there is nothing to compose.
func CheckDrift(contracts, have map[string][]byte, namespace string, stars map[string]bool) Drift {
	cs, bad, err := ParseAll(contracts, stars)
	if err != nil {
		return Drift{State: DriftCannotGo, Report: "the contracts do not compose: " + err.Error()}
	}
	unreadable := UnreadableReport(bad)
	c, err := Plan(Files(namespace, Compose(cs)), have)
	if err != nil {
		return Drift{State: DriftFound, Report: err.Error() + unreadable}
	}
	if c.Empty() {
		if len(bad) > 0 {
			return Drift{State: DriftFound, Report: fmt.Sprintf("the directory is the composed set of the %d readable contract(s)", len(cs)) + unreadable}
		}
		return Drift{State: DriftNone, Report: fmt.Sprintf("the directory is the composed set of %d contract(s)", len(cs))}
	}
	return Drift{State: DriftFound, Report: "stale against its contracts — a re-render would:\n" + Report(c) + "\n" + RenderHint + unreadable}
}

// UnreadableReport is one "contract-unparseable" line per unreadable file,
// each led by a newline, "" for none.
func UnreadableReport(bad []Unreadable) string {
	var b strings.Builder
	for _, u := range bad {
		b.WriteString("\ncontract-unparseable: " + u.Err.Error())
	}
	return b.String()
}

// RenderHint is how a person brings the directory back, as the line the
// finding ends with.
const RenderHint = "re-render: go run ./orbitcompose -contracts <foundry-dies>/orbits -out <flux>/prime/orbits (in foundry-tools), and land the result in flux"
