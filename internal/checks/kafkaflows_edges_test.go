package checks

import (
	"strings"
	"testing"
)

// The resolver's edges, one tree each: what it follows, and where it stops
// and says so rather than guessing.
func TestKafkaFlowsGoEdges(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		known KnownTopics
		want  []string
	}{
		{"function-local declarations, closures and a renamed import",
			map[string]string{"a/a.go": `package a
import k "github.com/twmb/franz-go/pkg/kgo"
func p() {
	const c = "from-const"
	var v = "from-var"
	var a, b = two()
	var z int
	t := "from-local"
	x, y := two()
	_, _, _, _, _ = a, b, z, x, y
	go func() { k.NewClient(k.DefaultProduceTopic(c), k.DefaultProduceTopic(v), k.DefaultProduceTopic(t)) }()
}
func two() (string, string) { return "", "" }
`}, nil,
			[]string{"produce from-const", "produce from-local", "produce from-var"}},
		{"a field written by assignment and a function stored from another package",
			map[string]string{
				"a/a.go": `package a
import ("example.com/star/b"; "github.com/twmb/franz-go/pkg/kgo")
type Cfg struct{ Topic string }
type W struct{ dial func(string) }
func run(cfg *Cfg) {
	cfg.Topic = "assigned"
	kgo.NewClient(kgo.DefaultProduceTopic(cfg.Topic))
	w := W{dial: b.Dial}
	w.dial("through-a-field")
}
`,
				"b/b.go": "package b\n" + kgoImport + "func Dial(topic string) { kgo.NewClient(kgo.ConsumeTopics(topic)) }\n",
			}, nil,
			[]string{"produce assigned", "consume through-a-field "}},
		{"partition reads that are not a literal map",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `
func r(m map[string]map[int32]kgo.Offset) { kgo.NewClient(kgo.ConsumePartitions(m), kgo.ConsumePartitions(m, m)) }
func s(c *struct{ cl *kgo.Client }) { c.cl.Close() }
`}, nil,
			[]string{"consume m "}},
		{"one flow stated twice is one flow, and groups order a topic's flows",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `
func a() { kgo.NewClient(kgo.ConsumerGroup("g2"), kgo.ConsumeTopics("t")) }
func b() { kgo.NewClient(kgo.ConsumerGroup("g1"), kgo.ConsumeTopics("t")) }
func c() { kgo.NewClient(kgo.ConsumerGroup("g1"), kgo.ConsumeTopics("t")) }
`}, nil,
			[]string{"consume t g1", "consume t g2"}},
		{"a group that is only partly known, and a parameter paired with a literal",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `
func Boot(prefix string) { on(prefix + "-suffix") }
func Main() { on2("t2") }
func on(group string) { kgo.NewClient(kgo.ConsumerGroup(group), kgo.ConsumeTopics("t1")) }
func on2(topic string) { kgo.NewClient(kgo.ConsumerGroup("lit"), kgo.ConsumeTopics(topic)) }
`}, nil,
			[]string{"consume t1 ?*-suffix", "consume t2 lit"}},
		{"fields of two different values do not pair",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `
type L struct{ Topic, Group string }
var ls = []L{{Topic: "x", Group: "gx"}}
func run(a, b L) { kgo.NewClient(kgo.ConsumerGroup(b.Group), kgo.ConsumeTopics(a.Topic)) }
`}, nil,
			[]string{"consume x gx"}},
		{"a closure's own local shadows the outer parameter",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `
func Main() { f("outer", "t") }
func f(group, topic string) {
	func() {
		group := "inner"
		kgo.NewClient(kgo.ConsumerGroup(group), kgo.ConsumeTopics(topic))
	}()
}
`}, nil,
			[]string{"consume t inner"}},
		{"receivers read off parameters, generics and constructors",
			map[string]string{
				"a/a.go": "package a\n" + kgoImport + `
type P struct{}
type G[T any] struct{}
func (p *P) Run(topic string) { kgo.NewClient(kgo.DefaultProduceTopic(topic)) }
func (g *G[T]) Run(topic string) {}
func NewP() *P { return &P{} }
func NewNothing() {}
func use(p *P, g *G[int]) {
	p.Run("via-param")
	g.Run("not-p")
	q := NewP()
	q.Run("via-ctor")
	n := NewNothing()
	n.Run("unknown-type")
	h := build()
	h.Run("ambiguous-ctor")
	var holder struct{ p *P }
	holder.p.Run("through-a-field")
	r := q
	r.Run("via-alias")
}
func build() *P { return nil }
`,
				"b/b.go": "package b\ntype Q struct{}\nfunc build() *Q { return nil }\n",
			}, nil,
			[]string{"produce ambiguous-ctor", "produce through-a-field", "produce unknown-type", "produce via-alias", "produce via-ctor", "produce via-param"}},
		{"values that are not strings, cycles, composites and parentheses",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `
var pa, pb = two()
func two() (string, string) { return "", "" }
var loopA = loopB
var loopB = loopA
var many = []string{"s1", ("s2")}
var keyed = map[int]string{0: "s3"}
func p(x interface{}) {
	kgo.NewClient(kgo.ConsumeTopics(1), kgo.ConsumeTopics(loopA), kgo.ConsumeTopics(many...), kgo.ConsumeTopics(keyed[0]), kgo.ConsumeTopics(x.(string)))
	kgo.NewClient(kgo.ConsumeTopics(keyedLit()...))
}
func keyedLit() []string { return map[int]string{1: "s4"}[1:] }
`}, nil,
			[]string{"consume 1 ", "consume keyedLit() ", "consume keyed[0] ", "consume loopA ", "consume s1 ", "consume s2 ", "consume x.(string) "}},
		{"constants through an in-tree import, and imports that are not",
			map[string]string{
				"a/a.go": `package a
import ("os"; "example.com/star/topics"; "github.com/twmb/franz-go/pkg/kgo")
func p() { kgo.NewClient(kgo.DefaultProduceTopic(topics.T), kgo.DefaultProduceTopic(os.Args[0]), kgo.DefaultProduceTopic(os.Getenv)) }
`,
				"topics/t.go": "package topics\nconst T = \"in-tree\"\nfunc Name(s string) string { return \"n-\" + s }\n",
				"c/c.go":      "package c\nimport (\"example.com/star/topics\"; \"github.com/twmb/franz-go/pkg/kgo\")\nfunc p() { kgo.NewClient(kgo.DefaultProduceTopic(topics.Name(\"q\"))) }\n",
			}, nil,
			[]string{"produce in-tree", "produce n-q", "produce os.Args[0]", "produce os.Getenv"}},
		{"concatenation, regex helpers, call topics and Sprintf edges",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `import ("fmt"; "x/beats"; "x/requestlog")
func p(u, v string, f string) {
	kgo.NewClient(kgo.DefaultProduceTopic(u+v), kgo.DefaultProduceTopic("left."+v), kgo.DefaultProduceTopic(requestlog.CallsTopic(u)))
	kgo.NewClient(kgo.DefaultProduceTopic(fmt.Sprintf(f, u)), kgo.DefaultProduceTopic(fmt.Sprintf("100%%-%s", "x")))
	kgo.NewClient(kgo.ConsumeTopics(beats.TopicRegex("*._ops.heartbeat")))
}
`}, nil,
			[]string{"produce *._ops.calls pattern family", "produce 100%-x", "produce fmt.Sprintf(f, u)", "produce left.* pattern", "produce u + v", "consume *._ops.heartbeat  pattern family"}},
		{"a function evaluated for its call: bodiless, mismatched, nested literals",
			map[string]string{"a/a.go": "package a\n" + kgoImport + `
func ext(s string) string
func one(s string) string { f := func() string { return "nested" }; _ = f; return "one-" + s }
func join(a, b string) string { return a + "." + b }
func un(string) string { return "unnamed" }
func pick() { kgo.NewClient(kgo.DefaultProduceTopic(ext("x")), kgo.DefaultProduceTopic(one("x", "y")), kgo.DefaultProduceTopic(one("z"))) }
func more() { kgo.NewClient(kgo.DefaultProduceTopic(join("a", "b")), kgo.DefaultProduceTopic(un("x"))) }
`}, nil,
			[]string{"produce a.b", "produce ext(\"x\")", "produce one(\"x\", \"y\")", "produce one-z", "produce unnamed"}},
		{"a name the core's table does not hold and no table at all",
			map[string]string{"a/a.go": `package a
import "git.notusmi.com/rob/stellar-core-go/kafkatopics"
func p() { kafkatopics.Record(TopicUnheard, nil) }
`}, nil,
			[]string{"produce TopicUnheard"}},
		{"a go.mod with no module line maps nothing",
			map[string]string{"go.mod": "go 1.26\n", "a/a.go": "package a\n" + kgoImport + `func p() { kgo.NewClient(kgo.DefaultProduceTopic("t")) }`}, nil,
			[]string{"produce t"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flows, _ := KafkaFlows(goTree(tc.files), tc.known)
			got := flowLines(flows)
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

func TestKafkaFlowsTextEdges(t *testing.T) {
	flows, _ := KafkaFlows(map[string]string{
		"a.py": "from kafka import KafkaProducer\np.send(os.environ['T'])\np.send(UNKNOWN)\np.send()\nTOPIC_SELF = TOPIC_SELF\np.send(TOPIC_SELF)\nCA = CB\nCB = CA\np.send(CA)",
		"b.ts": "import { Kafka } from \"kafkajs\";\nconst m = { topic: process.env.T ?? other };\n",
	}, nil)
	got := strings.Join(flowLines(flows), "\n")
	want := "produce CA\nproduce TOPIC_SELF\nproduce UNKNOWN\nproduce os.environ['T']\nproduce process.env.T ?? other"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if kfOnLineWith("a\nlast", 3, "las") != true {
		t.Error("the last line, with no newline after it, was not read")
	}
}

func TestRegexGlobCollapsesRuns(t *testing.T) {
	if got := RegexGlob("^.*[^.]+x$"); got != "*x" {
		t.Errorf("RegexGlob = %q, want *x", got)
	}
}
