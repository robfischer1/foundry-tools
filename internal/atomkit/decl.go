package atomkit

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// Def is a logic atom's whole declaration: its identity, what makes it
// relevant to a tree, what it needs, and its Check (lld.md SYS-003-MOD-002).
// An atom file exports exactly one, named Def.
type Def struct {
	ID, Stage string
	// Lane is the verdict's lane and the key the absent sentence reads; "" for
	// an any-lane atom (checks.LaneAny).
	Lane        checks.Lane
	Group, Desc string
	Surface     string        // root:<f> | any:<glob> | all:<f>,<f>
	Toolchain   string        // a tools.toml [toolchain.<name>] key, or "none"
	Needs       []string      // dies | svid | pytools | tool:<name> | service:<name>
	Timeout     time.Duration // 0 = 10 min
	// Deadline, when set, is how long this atom may take over this input; the run
	// gives the atom the longer of it and Timeout. An atom whose honest worst case
	// scales with the change (fleet:witness asks about every changed source) is
	// not cut off by a deadline sized for the atoms that read a few files.
	Deadline   func(In) time.Duration
	History    string // "" = shallow | "full"
	WritesTree bool
	SubsumedBy string
	NotOnForks string
	// ReasksOwn says the atom asks again, itself, the part of its work that could
	// not run (go:mutation, per module), so a runner that re-asks a could-not-run
	// atom whole does not re-ask this one: a mutation module that graded is the
	// costliest answer in the fleet to throw away. The index carries it as
	// reasks_own, and only when it is set.
	ReasksOwn bool
	// AfterAny are the atoms this one follows on ANY verdict (the index's
	// after_any; daedalus AFA1): the runner hands this atom its token once each
	// has a verdict, and when one did not pass it stands this atom down as
	// excluded itself rather than unreached. go:diff-coverage follows
	// go:test-race so it reads the profile that run left instead of running the
	// suite a second time. A runner that does not know the field ignores it, and
	// the atom must then do its whole work unordered.
	AfterAny []string
	// NeedsManifest is checks.AtomDef.NeedsManifest: the atom's subject is the
	// lane's root manifest, and its absent sentence says so.
	NeedsManifest bool
	Check         func(ctx context.Context, in In) checks.Verdict
}

// idRE is E4's atom id: <ns>:<name>.
var idRE = regexp.MustCompile(`^[a-z][a-z0-9-]*:[a-z][a-z0-9-]*$`)

// stages are the stages an atom may declare.
var stages = []string{"precommit", "prepush", "mutation", "visual", "dev", "cast", "publish", "bundle", "bases"}

// defaultTimeout is what Timeout 0 means.
const defaultTimeout = 10 * time.Minute

// described is the atom-index@1 entry Describe prints: the index entry less
// the fields the builder fills (kind, digest, manifest, binary_sha256).
type described struct {
	ID           string   `json:"id"`
	Stage        string   `json:"stage"`
	Lane         string   `json:"lane"`
	Group        string   `json:"group"`
	Desc         string   `json:"desc"`
	Surface      string   `json:"surface"`
	Toolchain    string   `json:"toolchain"`
	Needs        []string `json:"needs"`
	TimeoutS     int      `json:"timeout_s"`
	History      string   `json:"history"`
	WritesTree   bool     `json:"writes_tree"`
	SubsumedBy   string   `json:"subsumed_by"`
	NotOnForks   string   `json:"not_on_forks"`
	ReasksOwn    bool     `json:"reasks_own,omitempty"`
	AfterAny     []string `json:"after_any,omitempty"`
	AbsentReason string   `json:"absent_reason"`
	ForkReason   string   `json:"fork_reason"`
}

// Describe is the atom's atom-index@1 entry as JSON (lld.md FN-082). A
// declaration that does not validate has no entry.
func Describe(d Def) ([]byte, error) {
	if err := Validate(d); err != nil {
		return nil, err
	}
	e := described{
		ID: d.ID, Stage: d.Stage, Lane: string(d.Lane), Group: d.Group, Desc: d.Desc,
		Surface: d.Surface, Toolchain: d.Toolchain, Needs: append([]string{}, d.Needs...),
		TimeoutS: int(d.Timeout / time.Second), History: d.History, WritesTree: d.WritesTree,
		SubsumedBy: d.SubsumedBy, NotOnForks: d.NotOnForks, ReasksOwn: d.ReasksOwn,
		AfterAny: slices.Clone(d.AfterAny), AbsentReason: absentSentence(d),
	}
	slices.Sort(e.Needs)
	if d.Timeout == 0 {
		e.TimeoutS = int(defaultTimeout / time.Second)
	}
	if d.History == "" {
		e.History = "shallow"
	}
	if d.NotOnForks != "" {
		e.ForkReason = checks.ForkReasonFor(d.ID, "{record}", d.NotOnForks)
	}
	b, _ := Marshal(e) // a struct of strings, ints and string lists always marshals
	return b[:len(b)-1], nil
}

// Validate is the declaration test (lld.md FN-083): the first rule d breaks,
// as an error, or nil.
func Validate(d Def) error {
	if !idRE.MatchString(d.ID) {
		return fmt.Errorf("id %s is not <ns>:<name>", d.ID)
	}
	if !slices.Contains(stages, d.Stage) {
		return fmt.Errorf("stage %s is unknown", d.Stage)
	}
	if !validSurface(d.Surface) {
		// the sentence is the design's, colon and all; spelled by concatenation so
		// the error-string lint reads a message, not a constant
		return errors.New("surface " + d.Surface + " is not root:, any: or all:")
	}
	if d.Toolchain == "" {
		return errors.New("toolchain is empty")
	}
	for _, n := range d.Needs {
		if n != "dies" && n != "svid" && n != "pytools" && !strings.HasPrefix(n, "tool:") && !strings.HasPrefix(n, "service:") {
			return fmt.Errorf("need %s is not dies, svid, pytools, tool:<name> or service:<name>", n)
		}
	}
	if d.History != "" && d.History != "shallow" && d.History != "full" {
		return fmt.Errorf("history %s is not shallow or full", d.History)
	}
	if d.NotOnForks != "" && !slices.Contains(checks.ForkReasons, d.NotOnForks) {
		return fmt.Errorf("not_on_forks %s is not one of checks.ForkReasons", d.NotOnForks)
	}
	for i, a := range d.AfterAny {
		switch {
		case a == d.ID:
			return fmt.Errorf("after_any names the atom itself, %s", a)
		case !idRE.MatchString(a):
			return fmt.Errorf("after_any %s is not <ns>:<name>", a)
		case slices.Contains(d.AfterAny[:i], a):
			return fmt.Errorf("after_any names %s twice", a)
		}
	}
	if d.Check == nil {
		return errors.New("check is nil")
	}
	return nil
}

// AfterAnyCycle is the first cycle the after_any edges of defs close, as the
// ids around it ("a -> b -> a"), or "" when they close none. daedalus orders by
// the edges and does not check them: a cycle would hold every atom on it to the
// pod's deadline, so the catalogue refuses one at load. An edge to an id no def
// carries is not a cycle; the planner drops an edge to an atom it does not run.
func AfterAnyCycle(defs []*Def) string {
	edges := map[string][]string{}
	var ids []string
	for _, d := range defs {
		edges[d.ID] = d.AfterAny
		ids = append(ids, d.ID)
	}
	slices.Sort(ids)
	const (
		unseen = iota
		open
		done
	)
	state := map[string]int{}
	var path []string
	var visit func(id string) string
	visit = func(id string) string {
		switch state[id] {
		case open:
			i := slices.Index(path, id)
			return strings.Join(append(slices.Clone(path[i:]), id), " -> ")
		case done:
			return ""
		}
		state[id] = open
		path = append(path, id)
		for _, next := range edges[id] {
			if c := visit(next); c != "" {
				return c
			}
		}
		path = path[:len(path)-1]
		state[id] = done
		return ""
	}
	for _, id := range ids {
		if c := visit(id); c != "" {
			return c
		}
	}
	return ""
}

// validSurface is a surface with one of the three prefixes and something after it.
func validSurface(s string) bool {
	for _, p := range []string{"root:", "any:", "all:"} {
		if rest, ok := strings.CutPrefix(s, p); ok {
			return rest != ""
		}
	}
	return false
}
