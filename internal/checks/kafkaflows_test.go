package checks

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// flowLines renders flows as `<direction> <subject> <group>` for comparison.
func flowLines(flows []KafkaFlow) []string {
	out := make([]string, 0, len(flows))
	for _, f := range flows {
		g := f.Group
		if f.GroupUnresolved != "" {
			g = "?" + f.GroupUnresolved
		}
		line := f.Direction + " " + f.subject()
		if f.Direction == FlowConsume {
			line += " " + g
		}
		if f.Pattern {
			line += " pattern"
		}
		if f.Family {
			line += " family"
		}
		out = append(out, line)
	}
	return out
}

// goTree is a one-module tree: go.mod plus the named files.
func goTree(files map[string]string) map[string]string {
	out := map[string]string{"go.mod": "module example.com/star\n\ngo 1.26\n"}
	for k, v := range files {
		out[k] = v
	}
	return out
}

const kgoImport = `import "github.com/twmb/franz-go/pkg/kgo"
`

func knownTable(name string) (string, bool) {
	v, ok := map[string]string{
		"TopicSessionTurns": "session-turns", "TOPIC_SESSION_TURNS": "session-turns",
		"TopicSuffix": "._ops.heartbeat", "TOPIC_CONSCIOUSNESS": "consciousness",
	}[name]
	return v, ok
}

func TestKafkaFlowsGo(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"a literal record and a literal subscription",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `
func p(c *kgo.Client) { c.Produce(nil, &kgo.Record{Topic: "t1"}, nil) }
func s() { kgo.NewClient(kgo.ConsumerGroup("g1"), kgo.ConsumeTopics("t2", "t3")) }
`},
			[]string{"produce t1", "consume t2 g1", "consume t3 g1"}},
		{"a constant, a default produce topic and a groupless reader",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `
const T = "beliefs"
func p() { kgo.NewClient(kgo.DefaultProduceTopic(T)) }
func r() { kgo.NewClient(kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{"surprise": nil})) }
`},
			[]string{"produce beliefs", "consume surprise "}},
		{"a committed record is not a produce",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `
func c(cl *kgo.Client) { cl.CommitRecords(nil, &kgo.Record{Topic: "t"}) }
`},
			nil},
		{"topic and group follow each call site together",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `
func One() { on("g-one", "t-one") }
func Two() { on("g-two", "t-two") }
func on(group, topic string) { kgo.NewClient(kgo.ConsumerGroup(group), kgo.ConsumeTopics(topic)) }
`},
			[]string{"consume t-one g-one", "consume t-two g-two"}},
		{"a reassigned parameter is still its callers'",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `
func Main() { run("t", "g") }
func run(topic, group string) {
	if group == "" { group = "dflt" }
	kgo.NewClient(kgo.ConsumerGroup(group), kgo.ConsumeTopics(topic))
}
`},
			[]string{"consume t g"}},
		{"two fields of one struct literal pair",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `
type L struct{ Topic, Group string }
var ls = []L{{Topic: "x", Group: "gx"}, {Topic: "y", Group: "gy"}}
func (l L) Run() { kgo.NewClient(kgo.ConsumerGroup(l.Group), kgo.ConsumeTopics(l.Topic)) }
`},
			[]string{"consume x gx", "consume y gy"}},
		{"config fields through env defaults, a flag default and cmp.Or",
			map[string]string{
				"internal/config/config.go": `package config
import ("flag"; "os"; "cmp")
type Config struct{ Topic, Group, Other string }
func env(k, d string) string { return d }
func Load() Config {
	c := Config{Topic: env("X_TOPIC", "from-env"), Group: cmp.Or(os.Getenv("X_GROUP"), "from-or")}
	flag.StringVar(&c.Other, "other", "from-flag", "")
	return c
}
`,
				"cmd/x/main.go": "package main\n" + `import ("example.com/star/internal/config"; "github.com/twmb/franz-go/pkg/kgo")
func main() {
	cfg := config.Load()
	kgo.NewClient(kgo.ConsumerGroup(cfg.Group), kgo.ConsumeTopics(cfg.Topic, cfg.Other))
}
`},
			[]string{"consume from-env from-or", "consume from-flag from-or"}},
		{"a method evaluated for its own call",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `
type C struct{ Prefix string }
var c = C{Prefix: "star"}
func (c C) Group(s string) string { return c.Prefix + "-" + s }
func run() { kgo.NewClient(kgo.ConsumerGroup(c.Group("turns")), kgo.ConsumeTopics("session-turns")) }
`},
			[]string{"consume session-turns star-turns"}},
		{"a function stored under another name is called by it",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `
type R struct{ connect func(string) }
func Connect(topic string) { kgo.NewClient(kgo.DefaultProduceTopic(topic)) }
func use() { r := R{connect: Connect}; r.connect("stored") }
var newSrc = func(topic string) { Connect(topic) }
func boot() { newSrc("lit") }
`},
			[]string{"produce lit", "produce stored"}},
		{"a method of another type with the same arity is not a caller",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `
type P struct{}
type S struct{}
func (p *P) Run(topic string) { kgo.NewClient(kgo.ConsumeTopics(topic)) }
func (s *S) Run(topic string) {}
func main() { p := &P{}; p.Run("for-p"); s := &S{}; s.Run("for-s") }
`},
			[]string{"consume for-p "}},
		{"a concatenation with an unknown side is a family pattern",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `import "fmt"
func p(star string) { kgo.NewClient(kgo.DefaultProduceTopic(star + "._ops.calls"), kgo.DefaultProduceTopic(fmt.Sprintf("%s.x.%d", "a", star))) }
`},
			[]string{"produce *._ops.calls pattern family", "produce a.x.* pattern"}},
		{"a regex subscription reads back as its glob",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `import "regexp"
var pat = "^[^.]+" + regexp.QuoteMeta("._ops.heartbeat") + "$"
func s() { kgo.NewClient(kgo.ConsumeRegex(), kgo.ConsumerGroup("nyx"), kgo.ConsumeTopics(pat)) }
`},
			[]string{"consume *._ops.heartbeat nyx pattern family"}},
		{"the core's helpers and its constant table",
			map[string]string{"a/a.go": `package a
import ("git.notusmi.com/rob/stellar-core-go/heartbeat"; "git.notusmi.com/rob/stellar-core-go/kafkatopics"; "git.notusmi.com/rob/stellar-core-go/requestlog")
func p() {
	kafkatopics.Record(kafkatopics.TopicSessionTurns, nil)
	kafkatopics.Tombstone(kafkatopics.TopicNotInTable, "k")
	heartbeat.Start(nil, nil, "a"+heartbeat.TopicSuffix, nil)
	requestlog.TopicSink(requestlog.CallsTopic("a"), nil)
}
`},
			[]string{"produce a._ops.calls family", "produce a._ops.heartbeat family", "produce kafkatopics.TopicNotInTable", "produce session-turns"}},
		{"an unresolvable name is reported as written",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `
func s(re interface{ String() string }) { kgo.NewClient(kgo.ConsumerGroup(group()), kgo.ConsumeTopics(re.String())) }
func group() string { return "" }
`},
			[]string{"consume re.String() ?group()"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flows, note := KafkaFlows(goTree(tc.files), knownTable)
			if note != "" {
				t.Fatalf("note %q", note)
			}
			got := flowLines(flows)
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

func TestKafkaFlowsNamesAGoFileThatWouldNotParse(t *testing.T) {
	_, note := KafkaFlows(goTree(map[string]string{"a/a.go": "package a\nfunc {"}), nil)
	if !strings.Contains(note, "a/a.go") {
		t.Errorf("note %q does not name the file", note)
	}
}

func TestKafkaFlowsText(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"python through the core and a confluent consumer",
			map[string]string{
				"pkg/topics.py": "TOPIC_X = 'x-topic'\nGROUP = os.environ.get('G', 'pg')\n",
				"pkg/run.py": `from stellar_core import kafka_topics as kt
from confluent_kafka import Consumer
kt.produce(transport, TOPIC_X, ev)
c = Consumer({"group.id": GROUP})
c.subscribe([TOPIC_X, "other"])
# c.subscribe(["commented"])
`},
			[]string{"produce x-topic", "consume other pg", "consume x-topic pg"}},
		{"python that speaks no kafka is not read",
			map[string]string{"a.py": "store.subscribe([x])\n"}, nil},
		{"typescript through the core and a platformatic consumer",
			map[string]string{
				"src/a.ts": `import { produce, TOPIC_CONSCIOUSNESS } from "@forge/stellar-core-ts/kafkatopics";
import { Consumer } from "@platformatic/kafka";
export const GROUP = "calliope-g";
await produce(transport, TOPIC_CONSCIOUSNESS, ev);
const opts = { groupId: GROUP };
consumer.consume({ topics: [TOPIC_CONSCIOUSNESS] });
interface R { topic: string; }
const rec = { topic: process.env.T ?? "env-topic", value };
`},
			[]string{"produce consciousness", "produce env-topic", "consume consciousness calliope-g"}},
		{"rust through rdkafka, a crate-wide transport and rskafka",
			map[string]string{
				"crates/a/src/bus.rs": `use rdkafka::producer::BaseRecord;
const T: &str = "rd-topic";
fn p() { BaseRecord::to(T); consumer.subscribe(&[T]); cfg.set("group.id", "rg"); }
`,
				"crates/a/src/claims.rs": `const C: &'static str = "claims";
fn c() { transport.produce(C, &k, v); }
#[cfg(test)]
mod tests { fn t() { transport.produce("test-only", &k, v); } }
`,
				"crates/b/src/broker.rs": `use rskafka::client::ClientBuilder;
pub const S: &str = "surprise";
async fn r() { let pc = client.partition_client(S, 0, H).await?; pc.fetch_records(0, 1..2, 9).await?; }
async fn w() { let pc = client.partition_client(S, 0, H).await?; pc.produce(records, C).await?; }
`},
			[]string{"produce claims", "produce rd-topic", "produce surprise", "consume rd-topic rg", "consume surprise "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flows, _ := KafkaFlows(tc.files, knownTable)
			got := flowLines(flows)
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

func TestKafkaFlowFindingsAreFactsNotGrades(t *testing.T) {
	flows := []KafkaFlow{
		{Direction: FlowProduce, Topic: "t", Where: "a.go:1"},
		{Direction: FlowConsume, Topic: "t", Group: "g", Where: "a.go:2", Via: "const T"},
		{Direction: FlowConsume, Unresolved: "cfg.X", GroupUnresolved: "cfg.G", Where: "a.go:3"},
		{Direction: FlowConsume, Topic: "*._ops.heartbeat", Pattern: true, Family: true, Where: "a.go:4"},
	}
	got := KafkaFlowFindings(flows, KafkaFlowsID)
	want := []Finding{
		{VerdictInert, "t", FlowProduce, "produce; at a.go:1", KafkaFlowsID},
		{VerdictInert, "t", FlowConsume, "consume; group=g; at a.go:2; via const T", KafkaFlowsID},
		{VerdictUnanalyzable, "cfg.X", FlowUnresolved, "consume; group=?cfg.G; at a.go:3", KafkaFlowsID},
		{VerdictInert, "*._ops.heartbeat", FlowConsume, "consume; groupless; pattern; family; at a.go:4", KafkaFlowsID},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("finding %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	none := KafkaFlowFindings(nil, KafkaFlowsID)
	if len(none) != 1 || none[0].Cause != FlowNone || none[0].Verdict != VerdictInert {
		t.Errorf("a tree with no flow carries %+v, want one inert %s", none, FlowNone)
	}
	if n := NonFamilyFlows(flows); n != 3 {
		t.Errorf("NonFamilyFlows = %d, want 3", n)
	}
}

func TestKafkaFlowsVerdictAlwaysPasses(t *testing.T) {
	a := AtomByID(KafkaFlowsID)
	files := goTree(map[string]string{"a/a.go": "package a\n" + kgoImport + `
func p() { kgo.NewClient(kgo.DefaultProduceTopic(unknown)) }
`})
	failed := func() error { return errors.New("door down") }
	v := KafkaFlowsVerdict(a, files, func(string) (string, bool) { return "", false }, failed)
	if v.State != int(StatePass) || len(v.Findings) != 1 || v.Findings[0].Verdict != VerdictUnanalyzable {
		t.Fatalf("verdict %+v", v)
	}
	logs := strings.Join(v.Logs, "\n")
	for _, want := range []string{"1 Kafka flow(s), 1 unresolved", "unknown produce", "could not be read", "door down"} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs miss %q:\n%s", want, logs)
		}
	}
	empty := KafkaFlowsVerdict(a, nil, nil, nil)
	if empty.State != int(StatePass) || !strings.Contains(strings.Join(empty.Logs, "\n"), "no Kafka flow") {
		t.Errorf("empty tree: %+v", empty)
	}
}

func TestKafkaSourceFile(t *testing.T) {
	for p, want := range map[string]bool{
		"go.mod": true, "a/go.mod": true, "a/b.go": true, "a/b_test.go": false, "vendor/x/y.go": false,
		"a/testdata/x.go": false, "src/a.ts": true, "src/a.test.ts": false, "src/a.d.ts": false,
		"node_modules/k/i.ts": false, "p/m.py": true, "p/test_m.py": false, "tests/m.py": false,
		"src/lib.rs": true, "README.md": false, "a/b.tsx": true,
	} {
		if got := KafkaSourceFile(p); got != want {
			t.Errorf("KafkaSourceFile(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestScreamingSnakeKeepsAcronyms(t *testing.T) {
	for in, want := range map[string]string{
		"TopicSessionTurns": "TOPIC_SESSION_TURNS", "TopicOureaCIRequests": "TOPIC_OUREA_CI_REQUESTS",
		"TopicCIAttest": "TOPIC_CI_ATTEST", "TopicAglaiaWritingDeltas": "TOPIC_AGLAIA_WRITING_DELTAS", "X": "X",
	} {
		if got := ScreamingSnake(in); got != want {
			t.Errorf("ScreamingSnake(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRegexGlob(t *testing.T) {
	for in, want := range map[string]string{
		`^[^.]+\._ops\.heartbeat$`: "*._ops.heartbeat", "*._ops.calls": "*._ops.calls", "^fleet\\.family\\..*$": "fleet.family.*",
	} {
		if got := RegexGlob(in); got != want {
			t.Errorf("RegexGlob(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFetchKnownTopics(t *testing.T) {
	bodies := map[string]string{
		"kafkatopics/topics.go":  "package kafkatopics\nconst (\n\tTopicSessionTurns = \"session-turns\"\n\tN = 3\n)\nconst A, B = \"a\"\n",
		"heartbeat/heartbeat.go": "package heartbeat\nconst TopicSuffix = \"._ops.heartbeat\"\nvar V = \"not a const\"\n",
	}
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(bodies[r.URL.Query().Get("path")]))
	}))
	defer srv.Close()
	door := Door{Base: srv.URL, Client: srv.Client()}
	table, err := FetchKnownTopics(context.Background(), door)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"TopicSessionTurns": "session-turns", "TOPIC_SESSION_TURNS": "session-turns", "TopicSuffix": "._ops.heartbeat"} {
		if table[k] != want {
			t.Errorf("table[%s] = %q, want %q", k, table[k], want)
		}
	}
	if _, ok := table["V"]; ok {
		t.Error("a var is not a constant")
	}
	known, knownErr := LazyKnownTopics(context.Background(), door)
	if knownErr() != nil {
		t.Error("an unasked table reported an error")
	}
	if v, ok := known("TopicSuffix"); !ok || v != "._ops.heartbeat" || knownErr() != nil {
		t.Errorf("lazy table answered %q %v %v", v, ok, knownErr())
	}
	status = http.StatusNotFound
	if _, err := FetchKnownTopics(context.Background(), door); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("a 404 answered %v", err)
	}
	status = http.StatusOK
	bodies["heartbeat/heartbeat.go"] = "package {"
	if _, err := FetchKnownTopics(context.Background(), door); err == nil || !strings.Contains(err.Error(), "did not parse") {
		t.Errorf("a broken file answered %v", err)
	}
	srv.Close()
	if _, err := FetchKnownTopics(context.Background(), door); err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("a closed door answered %v", err)
	}
}
