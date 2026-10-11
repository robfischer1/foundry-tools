package checks

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// fleet:kafka-flows' extraction (slag-by-kind F13).
//
// TOPIC FLOWS ARE DERIVED, NOT DECLARED (plan decision, 2026-10-11). Which
// topics a star produces to and consumes from is a fact about its code, so
// this reads the code and records what it says beside the head's CI result,
// where nyx reads the latest main-head flows and compares them with the
// broker. Nothing is hand-declared, and there is no verb to declare one.
//
// THE ATOM NEVER REDS A REPO. A flow is a fact, not a grade: every flow is an
// `inert` finding, and the atom passes. A topic whose name the code does not
// state statically is an `unanalyzable` finding with cause `unresolved-topic`
// and the expression that named it — reported, never guessed. Only a tree that
// would not enumerate or read is a cannot-run.
//
// WHAT IS READ. Go is parsed (go/ast) and its topic and group expressions are
// resolved through string literals, constants, local assignments, one-hop
// function parameters (the call sites in the same repository), struct fields
// set in a composite literal, an assignment or a flag default, env lookups
// that carry a literal default, `+` and Sprintf concatenation (an unknown part
// becomes `*`, a family), and the stellar-core-go kafkatopics constants (read
// from that repository through the door, at main). Python, TypeScript and Rust
// are read by pattern: their client calls are few and regular, and their
// constants resolve the same way. Test files, vendor/ and testdata/ are not
// read: a suite's private topic is not a flow the star runs.

// FlowProduce and FlowConsume are a flow's two directions, and the two causes
// its finding carries.
const (
	FlowProduce = "produce"
	FlowConsume = "consume"
	// FlowUnresolved is the cause of a flow whose topic the code does not state.
	FlowUnresolved = "unresolved-topic"
	// FlowNone is the cause of the one finding a tree with no flow carries, so a
	// reader of the record can tell "scanned and found none" from "never scanned".
	FlowNone = "no-kafka-flows"
)

// KafkaFlow is one (topic, direction, consumer group) the code states.
type KafkaFlow struct {
	// Direction is FlowProduce or FlowConsume.
	Direction string
	// Topic is the topic's name, or a family glob (`*._ops.heartbeat`) when
	// Pattern is set. Empty when Unresolved says why.
	Topic   string
	Pattern bool
	// Family marks a flow on a per-star family topic (`<star>._ops.heartbeat`,
	// `<star>._ops.calls`): fleet plumbing every star rides, not a seam.
	Family bool
	// Unresolved is the source expression that named the topic, when nothing
	// static says what it evaluates to.
	Unresolved string
	// Group is the consumer group, when the code names one statically.
	// GroupUnresolved is its expression when it does not; both empty is a
	// groupless reader (ConsumePartitions, or no group beside the subscription).
	Group           string
	GroupUnresolved string
	// Where is path:line of the call that states the flow.
	Where string
	// Via says how the name was resolved, when it took more than a literal.
	Via string
}

// key is a flow's identity for deduplication: the same flow stated at two
// call sites is one flow.
func (f KafkaFlow) key() string {
	return strings.Join([]string{f.Direction, f.Topic, f.Unresolved, f.Group, f.GroupUnresolved}, "\x00")
}

// subject is what the finding names: the topic, or the expression that did
// not resolve.
func (f KafkaFlow) subject() string {
	if f.Topic != "" {
		return f.Topic
	}
	return f.Unresolved
}

// detail is the finding's one sentence. Its leading words are a grammar nyx
// reads back — `<direction>; group=<g>|group=?<expr>|groupless; at <where>` —
// so the facts survive a store that keeps only the schema's five fields.
func (f KafkaFlow) detail() string {
	parts := []string{f.Direction}
	if f.Direction == FlowConsume {
		switch {
		case f.Group != "":
			parts = append(parts, "group="+f.Group)
		case f.GroupUnresolved != "":
			parts = append(parts, "group=?"+f.GroupUnresolved)
		default:
			parts = append(parts, "groupless")
		}
	}
	if f.Pattern {
		parts = append(parts, "pattern")
	}
	if f.Family {
		parts = append(parts, "family")
	}
	parts = append(parts, "at "+f.Where)
	if f.Via != "" {
		parts = append(parts, "via "+f.Via)
	}
	return strings.Join(parts, "; ")
}

// KafkaFlowFindings is the flows in the findings schema's shape: one inert
// finding per flow, one unanalyzable finding per unresolved topic, and one
// inert `no-kafka-flows` finding for a tree that states none.
func KafkaFlowFindings(flows []KafkaFlow, probe string) []Finding {
	if len(flows) == 0 {
		return []Finding{{Verdict: VerdictInert, Subject: "kafka", Cause: FlowNone,
			Detail: "this tree states no Kafka produce or consume", Probe: probe}}
	}
	out := make([]Finding, 0, len(flows))
	for _, f := range flows {
		fd := Finding{Verdict: VerdictInert, Subject: f.subject(), Cause: f.Direction, Detail: f.detail(), Probe: probe}
		if f.Topic == "" {
			fd.Verdict, fd.Cause = VerdictUnanalyzable, FlowUnresolved
		}
		out = append(out, fd)
	}
	return capFindings(out, probe)
}

// KafkaFlowsReport is the atom's lines: a count, then one line per flow.
func KafkaFlowsReport(id string, flows []KafkaFlow, note string) string {
	var b strings.Builder
	if len(flows) == 0 {
		fmt.Fprintf(&b, "%s: no Kafka flow stated in this tree", id)
	} else {
		unresolved := 0
		for _, f := range flows {
			if f.Topic == "" {
				unresolved++
			}
		}
		fmt.Fprintf(&b, "%s: %d Kafka flow(s), %d unresolved — recorded, not graded", id, len(flows), unresolved)
		for _, f := range flows {
			fmt.Fprintf(&b, "\n  %s %s", f.subject(), f.detail())
		}
	}
	if note != "" {
		b.WriteString("\n" + note)
	}
	return b.String()
}

// KafkaSourceFile reports whether the extraction reads a path: Go, Python,
// TypeScript and Rust sources and the go.mod files that map import paths,
// never tests, vendor/ or testdata/.
func KafkaSourceFile(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "vendor", "testdata", "node_modules", "__tests__", "tests", "test":
			return false
		}
	}
	base := p[strings.LastIndex(p, "/")+1:]
	switch {
	case base == "go.mod":
		return true
	case strings.HasSuffix(base, "_test.go"), strings.HasPrefix(base, "test_"), strings.HasSuffix(base, "_test.py"),
		strings.Contains(base, ".test."), strings.Contains(base, ".spec."), strings.HasSuffix(base, ".d.ts"):
		return false
	}
	for _, ext := range []string{".go", ".py", ".ts", ".tsx", ".mts", ".rs"} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return false
}

// KnownTopics answers a contract constant's value by name — the Go name
// (`TopicSessionTurns`) or its SCREAMING_SNAKE twin (`TOPIC_SESSION_TURNS`),
// which is how the Python and TypeScript cores spell the same table. Nil
// answers nothing.
type KnownTopics func(name string) (string, bool)

// The core's topic constants live in stellar-core-go, at main: the contract
// topics in kafkatopics, the heartbeat family's suffix in heartbeat.
const KafkaTopicsRepo = "rob/stellar-core-go"

// KnownTopicSources are the core files whose string constants resolve a name.
var KnownTopicSources = []string{"kafkatopics/topics.go", "heartbeat/heartbeat.go"}

// FetchKnownTopics reads the core's constant table through the door. Any file
// that does not read is the error: half a table resolves some names and
// leaves the rest looking like code that states nothing.
func FetchKnownTopics(ctx context.Context, door Door) (map[string]string, error) {
	out := map[string]string{}
	for _, p := range KnownTopicSources {
		status, body, err := door.Get(ctx, KafkaTopicsRepo, p)
		switch {
		case err != nil:
			return nil, fmt.Errorf("the door is unreachable (%s): %w", door.URL(KafkaTopicsRepo, p), err)
		case status != http.StatusOK:
			return nil, fmt.Errorf("the door answered HTTP %d for %s", status, door.URL(KafkaTopicsRepo, p))
		}
		consts, err := ParseKnownTopics(string(body))
		if err != nil {
			return nil, fmt.Errorf("%s did not parse: %w", p, err)
		}
		for k, v := range consts {
			out[k] = v
		}
	}
	return out, nil
}

// ParseKnownTopics reads every package-level string constant of a Go file,
// under its own name and its SCREAMING_SNAKE twin.
func ParseKnownTopics(src string) (map[string]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "core.go", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, s := range g.Specs {
			vs := s.(*ast.ValueSpec)
			for i, n := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				if v, ok := stringLit(vs.Values[i]); ok {
					out[n.Name] = v
					out[ScreamingSnake(n.Name)] = v
				}
			}
		}
	}
	return out, nil
}

// ScreamingSnake turns a Go name into its SCREAMING_SNAKE twin, keeping an
// acronym whole: TopicOureaCIRequests -> TOPIC_OUREA_CI_REQUESTS.
func ScreamingSnake(name string) string {
	rs := []rune(name)
	var b strings.Builder
	for i, r := range rs {
		if i > 0 && unicode.IsUpper(r) {
			prevLower := unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1])
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if prevLower || (unicode.IsUpper(rs[i-1]) && nextLower) {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

// KafkaFlows extracts every flow the files state, deduplicated and sorted by
// direction, subject and group. files maps a slash path to its body (see
// KafkaSourceFile). The note says what was not read, for the report.
func KafkaFlows(files map[string]string, known KnownTopics) (flows []KafkaFlow, note string) {
	var notes []string
	gf, bad := goFlows(files, known)
	flows = append(flows, gf...)
	if len(bad) > 0 {
		sort.Strings(bad)
		notes = append(notes, fmt.Sprintf("not parsed (Go syntax error): %s", strings.Join(bad, ", ")))
	}
	flows = append(flows, textFlows(files, known)...)
	return dedupeFlows(flows), strings.Join(notes, "\n")
}

// dedupeFlows keeps the first site of each flow, in a stable order.
func dedupeFlows(flows []KafkaFlow) []KafkaFlow {
	sort.SliceStable(flows, func(i, j int) bool { return flows[i].Where < flows[j].Where })
	seen := map[string]bool{}
	var out []KafkaFlow
	for _, f := range flows {
		if strings.Contains(f.Topic, FamilyInfix) {
			f.Family = true
		}
		if seen[f.key()] {
			continue
		}
		seen[f.key()] = true
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Direction != b.Direction {
			return a.Direction > b.Direction // produce before consume
		}
		if a.subject() != b.subject() {
			return a.subject() < b.subject()
		}
		return a.Group+a.GroupUnresolved < b.Group+b.GroupUnresolved
	})
	return out
}

// FamilyInfix is what every per-star family topic carries.
const FamilyInfix = "._ops."

// NonFamilyFlows counts the flows a CI test broker exists for: every flow but a
// family one (heartbeats and call witnesses ride every star, and binding a
// broker for each of them would bind one everywhere).
func NonFamilyFlows(flows []KafkaFlow) int {
	n := 0
	for _, f := range flows {
		if !f.Pattern && !f.Family {
			n++
		}
	}
	return n
}

// KafkaFlowsID is the atom that records flows.
const KafkaFlowsID = "fleet:kafka-flows"

// KafkaFlowsVerdict is the atom's whole answer over the files it read: a pass
// carrying one finding per flow. known is asked only when a name needs the
// core's table; knownErr (asked after) says the table could not be read, and
// the names it would have resolved stay unresolved rather than failing.
func KafkaFlowsVerdict(a AtomDef, files map[string]string, known KnownTopics, knownErr func() error) Verdict {
	flows, note := KafkaFlows(files, known)
	if knownErr != nil {
		if err := knownErr(); err != nil {
			note = strings.TrimSpace(note + "\nthe core's topic constants could not be read, so names that need them are reported unresolved: " + err.Error())
		}
	}
	v := VerdictOf(a, int(StatePass), KafkaFlowsReport(a.ID, flows, note))
	v.Findings = KafkaFlowFindings(flows, a.ID)
	return v
}

// LazyKnownTopics fetches the core's table on the first name that needs it,
// once. The error reader says whether that fetch failed (nil when it never
// ran).
func LazyKnownTopics(ctx context.Context, door Door) (KnownTopics, func() error) {
	var (
		once  sync.Once
		table map[string]string
		err   error
	)
	known := func(name string) (string, bool) {
		once.Do(func() { table, err = FetchKnownTopics(ctx, door) })
		v, ok := table[name]
		return v, ok
	}
	return known, func() error { return err }
}
