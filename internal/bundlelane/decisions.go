package bundlelane

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
)

// THE DECISION TABLE (stark42 A1 S4). foundry-dies derives data.grants.decisions
// at build time — its own Rego (tools/authz_table/derive.rego) asked over its own
// policy and the catalog roster — and the lane lays the answer into the policy
// die beside the grants it was derived from. It is never committed: a function of
// the commit, so a Rego or grant edit can never leave it stale. These are the
// lane's decisions about it; bundle_decisions.go at the module root runs them.
const (
	// DecisionsDerive is the Rego the table is derived by. A tree without it
	// ships no table, and decide answers by the rules, as before the table.
	DecisionsDerive = "tools/authz_table/derive.rego"
	// DecisionsDeriveDir is DecisionsDerive's directory, loaded whole.
	DecisionsDeriveDir = "tools/authz_table"
	// DecisionsQuery is what the derive answers: the table.
	DecisionsQuery = "data.authz_v2_table.decisions"
	// DecisionsPath is where the table is laid in the policy tree, so the die
	// serves it as data.grants.decisions (the "grants" root carries it).
	DecisionsPath = "policy/grants/decisions/data.json"
	// DecisionsStage is the roster the derive reads, staged as the fleet die
	// stages it (DeclaredPath), under the lane's work directory.
	DecisionsStage = "decisions-stage"
	// DecisionsCacheVolume is the engine volume derived tables are kept in, one
	// file per content digest of the derive's inputs (the lane's DECISIONS_KEY).
	DecisionsCacheVolume = "foundry-authz-decisions"
	// DecisionsCacheDir is where that volume is mounted.
	DecisionsCacheDir = "/decisions-cache"
)

// decisionsCacheScript serves the table from the cache when the key's file is
// there and otherwise derives it ("$@") and keeps it. The derive is written to a
// temp file of its own (the volume is shared, so two runs may derive one key at
// once) and renamed in, so a derive that fails or is cut short never leaves a
// half table under the key; a failed derive's output is passed on as it was.
const decisionsCacheScript = `f="$DECISIONS_CACHE/$DECISIONS_KEY.json"
if [ -s "$f" ]; then cat "$f"; exit 0; fi
t="$f.$$.tmp"
"$@" > "$t" || { c=$?; cat "$t"; rm -f "$t"; exit $c; }
mv "$t" "$f" && cat "$f"`

// DecisionsCachedArgs is DecisionsArgs behind the cache: the same derive argv,
// run only when the cache holds no table for the inputs' digest.
func DecisionsCachedArgs(stage string) []string {
	return append([]string{"sh", "-c", decisionsCacheScript, "sh"}, DecisionsArgs(stage)...)
}

// DecisionsStats is what the lane logs and gates about a derived table.
type DecisionsStats struct {
	Keys, Cells, ReasonSets, Bytes, Gzipped int
}

func (s DecisionsStats) String() string {
	return fmt.Sprintf("%d keys, %d cells, %d reason sets, %d bytes (%d gzipped)", s.Keys, s.Cells, s.ReasonSets, s.Bytes, s.Gzipped)
}

// decisionsDoc is the part of the table the lane reads: its keys' cells and
// its reason sets. The lookup (foundry-dies policy/authz_v2/table.rego) reads
// more; the lane only counts and refuses an empty table.
type decisionsDoc struct {
	Keys map[string]struct {
		C map[string]map[string]json.RawMessage `json:"c"`
		D json.RawMessage                       `json:"d"`
	} `json:"keys"`
	Reasons []json.RawMessage `json:"reasons"`
	Prefix  map[string]any    `json:"prefix"`
	Caps    map[string]any    `json:"caps"`
}

// Decisions grades the derive's output: one JSON object with keys, each with a
// default cell, reason sets, and the per-star prefixes and capabilities the
// lookup is guarded by. An empty or partial table is a finding: it would ship a
// die whose every call falls back to the rules while claiming to carry a table.
func Decisions(raw string) (DecisionsStats, error) {
	var d decisionsDoc
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return DecisionsStats{}, fmt.Errorf("the derived table is not one JSON object: %w", err)
	}
	if len(d.Keys) == 0 {
		return DecisionsStats{}, errors.New("the derived table has no keys: every call would fall back to the rules")
	}
	if len(d.Prefix) == 0 || len(d.Caps) == 0 {
		return DecisionsStats{}, errors.New("the derived table names no star prefix or no capability: the lookup could never hit")
	}
	s := DecisionsStats{Keys: len(d.Keys), ReasonSets: len(d.Reasons), Bytes: len(raw)}
	for k, row := range d.Keys {
		if len(row.D) == 0 {
			return DecisionsStats{}, fmt.Errorf("the derived table's key %q has no default cell", k)
		}
		for _, verbs := range row.C {
			s.Cells += len(verbs)
		}
	}
	s.Gzipped = gzipped(raw)
	return s, nil
}

// gzipped is how many bytes raw gzips to: what the table adds to the die on
// the wire. A gzip.Writer over a bytes.Buffer cannot fail — the buffer's
// Write never returns an error — so there is no error to carry.
func gzipped(raw string) int {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write([]byte(raw)) // a bytes.Buffer's Write never fails
	_ = w.Close()               // nor, then, the flush
	return buf.Len()
}

// DecisionsArgs is the `opa eval` argv that derives the table: the policy,
// the derive and the staged roster loaded as files (not a bundle: the derive
// is not in policy/'s manifest roots), the table printed as raw JSON.
func DecisionsArgs(stage string) []string {
	return []string{"opa", "eval", "-d", "policy", "-d", DecisionsDeriveDir, "-d", stage, "--format", "raw", DecisionsQuery}
}

// BuiltDecisions grades the built policy die's data.json: when the lane
// derived a table, the artifact carries it under data.grants.decisions — the
// table has to reach the die, not only the tree it was built from. It answers
// the keys the die carries, and a problem ("" when it holds).
func BuiltDecisions(dataJSON string, derived bool) (int, string) {
	var d struct {
		Grants struct {
			Decisions *decisionsDoc `json:"decisions"`
		} `json:"grants"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &d); err != nil {
		return 0, "the built bundle's data.json is not JSON: " + err.Error()
	}
	n := 0
	if d.Grants.Decisions != nil {
		n = len(d.Grants.Decisions.Keys)
	}
	if derived && n == 0 {
		return 0, "the decision table was derived but the built die carries no data.grants.decisions — decide would answer every call by the rules"
	}
	return n, ""
}
