package checks

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// The Python, TypeScript and Rust half of fleet:kafka-flows, read by pattern.
//
// THE CALLS ARE FEW AND REGULAR. The fleet's Python and TypeScript produce
// through their stellar-core's `produce(transport, topic, event)`, and their
// consumers name `topics`/`groupId` (TS), `subscribe([...])`/`group_id` (Python)
// or rdkafka's `BaseRecord::to`/`subscribe(&[..])`/`group.id` (Rust). A file is
// read only when it imports a Kafka client or a core's topics module, so an
// unrelated `.subscribe(` (a UI store's) is never a flow. Names resolve
// through the language's own module constants, an env lookup's literal
// default and the kafkatopics table; anything else is reported unresolved.

// textLang is one language's patterns.
type textLang struct {
	// gate says the file speaks Kafka at all.
	gate *regexp.Regexp
	// constDef captures NAME and its expression at module level.
	constDef *regexp.Regexp
	produce  []*regexp.Regexp
	consume  []*regexp.Regexp
	group    []*regexp.Regexp
}

var (
	pyLang = textLang{
		gate:     regexp.MustCompile(`(?m)^\s*(?:from|import)\s+[\w.]*(?:kafka|kafka_topics|kafka_transport)\b`),
		constDef: regexp.MustCompile(`(?m)^([A-Z_][A-Z0-9_]*)\s*(?::\s*[^=\n]+)?=\s*(.+)$`),
		produce: []*regexp.Regexp{
			regexp.MustCompile(`(?:^|[^.\w])produce\(\s*[^,()]+,\s*([^,()\n]+)`),
			regexp.MustCompile(`\b(?:kt|kafka_topics)\.produce\(\s*[^,()]+,\s*([^,()\n]+)`),
			regexp.MustCompile(`\.send(?:_and_wait)?\(\s*([^,()\n]+)`),
		},
		consume: []*regexp.Regexp{
			regexp.MustCompile(`\.subscribe\(\s*\[([^\]]*)\]`),
			regexp.MustCompile(`\btopics\s*=\s*\[([^\]]*)\]`),
			regexp.MustCompile(`AIOKafkaConsumer\(\s*([^,()=\n]+)[,)]`),
		},
		group: []*regexp.Regexp{
			regexp.MustCompile(`\bgroup_id\s*=\s*([^,()\n]+)`),
			regexp.MustCompile(`["']group\.id["']\s*:\s*([^,}\n]+)`),
		},
	}
	tsLang = textLang{
		gate:     regexp.MustCompile(`(?m)^\s*import\b[^;]*from\s+["'](?:kafkajs|@platformatic/kafka|node-rdkafka|@confluentinc/[^"']+|[^"']*stellar-core-ts[^"']*kafka[^"']*)["']`),
		constDef: regexp.MustCompile(`(?m)^\s*(?:export\s+)?const\s+([A-Za-z_$][\w$]*)\s*(?::[^=\n]+)?=\s*([^;\n]+);?\s*$`),
		produce: []*regexp.Regexp{
			regexp.MustCompile(`(?:^|[^.\w])produce\(\s*[^,()]+,\s*([^,()\n]+)`),
			regexp.MustCompile(`(?m)^[^/\n]*?(?:^|[{,\s])topic:\s*([^,}\n]+)`),
		},
		consume: []*regexp.Regexp{
			regexp.MustCompile(`\btopics:\s*\[([^\]]*)\]`),
			regexp.MustCompile(`subscribe\(\s*\{\s*topic:\s*([^,}\n]+)`),
		},
		group: []*regexp.Regexp{regexp.MustCompile(`\bgroupId:\s*([^,}\n]+)`)},
	}
	rsLang = textLang{
		gate:     regexp.MustCompile(`\b(?:rdkafka|rskafka)::`),
		constDef: regexp.MustCompile(`(?m)^\s*(?:pub(?:\([^)]*\))?\s+)?const\s+([A-Z_][A-Z0-9_]*)\s*:\s*&(?:'static\s+)?str\s*=\s*([^;]+);`),
		produce: []*regexp.Regexp{
			regexp.MustCompile(`(?:BaseRecord|FutureRecord)::to\(\s*([^)\n]+)\)`),
			rsProduceCall,
		},
		consume: []*regexp.Regexp{regexp.MustCompile(`\.subscribe\(\s*&\[([^\]]*)\]`)},
		group:   []*regexp.Regexp{regexp.MustCompile(`"group\.id",\s*([^)\n]+)\)`)},
	}
)

// rsProduceCall is a Rust `.produce(topic, ...)`: rdkafka's, or a star's own
// transport over it.
var rsProduceCall = regexp.MustCompile(`\.produce\(\s*([^,()\n]+),`)

// rsPartition is rskafka's partition client, which names the topic; what the
// code then does with it says the direction.
var rsPartition = regexp.MustCompile(`\.partition_client\(\s*([^,()\n]+),`)

// rsPartitionFlows reads rskafka: a partition client followed by
// fetch_records is a (groupless) consume, by produce a produce.
func rsPartitionFlows(r textResolver, body, p string) []KafkaFlow {
	var out []KafkaFlow
	ms := rsPartition.FindAllStringSubmatchIndex(body, -1)
	for i, m := range ms {
		end := min(len(body), m[1]+800)
		if i+1 < len(ms) && ms[i+1][0] < end {
			end = ms[i+1][0]
		}
		window := body[m[1]:end]
		for _, d := range []struct{ dir, call string }{{FlowConsume, "fetch_records("}, {FlowProduce, ".produce("}} {
			if strings.Contains(window, d.call) && !kfCommented(body, m[0]) {
				out = append(out, r.flows(d.dir, body[m[2]:m[3]], p, kfLine(body, m[0]), nil)...)
			}
		}
	}
	return out
}

// langOf is the pattern set for a path.
func langOf(p string) (*textLang, string) {
	switch path.Ext(p) {
	case ".py":
		return &pyLang, "py"
	case ".ts", ".tsx", ".mts":
		return &tsLang, "ts"
	case ".rs":
		return &rsLang, "rs"
	}
	return nil, ""
}

var (
	kfQuoted   = regexp.MustCompile(`^(?:"([^"\\]*)"|'([^'\\]*)'|` + "`([^`$\\\\]*)`" + `)$`)
	kfAnyStr   = regexp.MustCompile(`"[^"\\]*"|'[^'\\]*'`)
	kfLastStr  = regexp.MustCompile(`(?:"([^"\\]*)"|'([^'\\]*)')[^"']*$`)
	kfNameTail = regexp.MustCompile(`^[A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*$`)
	kfEnvish   = regexp.MustCompile(`(?i)env|getenv`)
	kfCfgTest  = regexp.MustCompile(`(?m)^\s*#\[cfg\(test\)\]`)
)

// textFlows reads every non-Go source that speaks Kafka.
func textFlows(files map[string]string, known KnownTopics) []KafkaFlow {
	consts := map[string]map[string]string{} // lang -> NAME -> expression
	var speaking []string
	rustKafka := false
	for p, body := range files {
		lang, tag := langOf(p)
		if lang == nil {
			continue
		}
		if consts[tag] == nil {
			consts[tag] = map[string]string{}
		}
		for _, m := range lang.constDef.FindAllStringSubmatch(body, -1) {
			if _, dup := consts[tag][m[1]]; !dup {
				consts[tag][m[1]] = strings.TrimSpace(m[2])
			}
		}
		if lang.gate.MatchString(body) {
			speaking = append(speaking, p)
			rustKafka = rustKafka || tag == "rs"
		}
	}
	// A Rust crate names rdkafka in one file and calls through it in others.
	if rustKafka {
		for p := range files {
			if _, tag := langOf(p); tag == "rs" && !slices.Contains(speaking, p) {
				speaking = append(speaking, p)
			}
		}
	}
	sort.Strings(speaking)
	var out []KafkaFlow
	for _, p := range speaking {
		lang, tag := langOf(p)
		body := files[p]
		if loc := kfCfgTest.FindStringIndex(body); tag == "rs" && loc != nil {
			body = body[:loc[0]]
		}
		r := textResolver{consts: consts[tag], known: known}
		groups := r.groups(lang, body)
		if tag == "rs" {
			out = append(out, rsPartitionFlows(r, body, p)...)
		}
		for _, re := range lang.produce {
			if tag == "rs" && re == rsProduceCall && strings.Contains(body, "rskafka") {
				// rskafka's produce takes records, not a topic: its partition
				// client carries the topic (rsPartitionFlows).
				continue
			}
			for _, m := range re.FindAllStringSubmatchIndex(body, -1) {
				if kfCommented(body, m[0]) || kfTypeOnly(body[m[2]:m[3]]) || (tag == "ts" && kfOnLineWith(body, m[0], "subscribe(")) {
					continue
				}
				out = append(out, r.flows(FlowProduce, body[m[2]:m[3]], p, kfLine(body, m[0]), nil)...)
			}
		}
		for _, re := range lang.consume {
			for _, m := range re.FindAllStringSubmatchIndex(body, -1) {
				if kfCommented(body, m[0]) {
					continue
				}
				for _, el := range kfSplitList(body[m[2]:m[3]]) {
					out = append(out, r.flows(FlowConsume, el, p, kfLine(body, m[0]), groups)...)
				}
			}
		}
	}
	return out
}

// kfTypeOnly says a `topic:` capture is a type annotation, not a value.
func kfTypeOnly(expr string) bool {
	t := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(expr), ";"))
	return t == "string" || strings.HasPrefix(t, "string ") || strings.HasPrefix(t, "string[") || t == "str"
}

// kfOnLineWith says the line holding off also holds s.
func kfOnLineWith(body string, off int, s string) bool {
	start := strings.LastIndex(body[:off], "\n") + 1
	end := strings.Index(body[off:], "\n")
	if end < 0 {
		end = len(body) - off
	}
	return strings.Contains(body[start:off+end], s)
}

// kfLine is the 1-based line of an offset.
func kfLine(body string, off int) int { return strings.Count(body[:off], "\n") + 1 }

// kfCommented says the match starts on a comment line.
func kfCommented(body string, off int) bool {
	start := strings.LastIndex(body[:off], "\n") + 1
	t := strings.TrimSpace(body[start:min(len(body), off+1)])
	return strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "/*")
}

// kfSplitList splits a bracketed list's body on commas.
func kfSplitList(s string) []string {
	var out []string
	for _, el := range strings.Split(s, ",") {
		if el = strings.TrimSpace(el); el != "" {
			out = append(out, el)
		}
	}
	return out
}

// textResolver resolves an expression through one language's constants.
type textResolver struct {
	consts map[string]string
	known  KnownTopics
}

// value resolves an expression to a static string, with how.
func (r textResolver) value(expr string, depth int) (string, string, bool) {
	expr = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(expr), ")"))
	expr = strings.TrimPrefix(expr, "&")
	if depth > resolveDepth {
		return "", "", false
	}
	if m := kfQuoted.FindStringSubmatch(expr); m != nil {
		return m[1] + m[2] + m[3], "", true
	}
	if kfEnvish.MatchString(expr) {
		// A default is the operand after `??`/`||` (TypeScript), or the last
		// of two or more literals (`os.environ.get("NAME", "default")`). One
		// literal alone is the variable's NAME, and no default.
		if i := max(strings.LastIndex(expr, "??"), strings.LastIndex(expr, "||")); i >= 0 {
			if v, _, ok := r.value(expr[i+2:], depth+1); ok {
				return v, "env default", true
			}
			return "", "", false
		}
		if len(kfAnyStr.FindAllString(expr, -1)) >= 2 {
			m := kfLastStr.FindStringSubmatch(expr)
			return m[1] + m[2], "env default", true
		}
		return "", "", false
	}
	if kfNameTail.MatchString(expr) {
		name := expr[strings.LastIndex(expr, ".")+1:]
		if def, ok := r.consts[name]; ok && def != expr {
			if v, via, ok := r.value(def, depth+1); ok {
				if via == "" {
					via = "const " + name
				}
				return v, via, true
			}
		}
		if r.known != nil {
			if v, ok := r.known(name); ok {
				return v, "kafkatopics." + name, true
			}
		}
	}
	return "", "", false
}

// groups is the file's consumer groups, resolved; one unresolved entry when
// a group is named but not statically.
func (r textResolver) groups(lang *textLang, body string) []KafkaFlow {
	var out []KafkaFlow
	seen := map[string]bool{}
	for _, re := range lang.group {
		for _, m := range re.FindAllStringSubmatch(body, -1) {
			g := KafkaFlow{GroupUnresolved: strings.TrimSpace(m[1])}
			if v, _, ok := r.value(m[1], 0); ok {
				g = KafkaFlow{Group: v}
			}
			if k := g.Group + "\x00" + g.GroupUnresolved; !seen[k] {
				seen[k] = true
				out = append(out, g)
			}
		}
	}
	return out
}

// flows is one matched topic expression as flows, one per group.
func (r textResolver) flows(dir, expr, p string, ln int, groups []KafkaFlow) []KafkaFlow {
	f := KafkaFlow{Direction: dir, Where: fmt.Sprintf("%s:%d", p, ln)}
	if v, via, ok := r.value(expr, 0); ok {
		f.Topic, f.Via = v, via
	} else {
		f.Unresolved = strings.TrimSpace(expr)
	}
	if dir != FlowConsume || len(groups) == 0 {
		return []KafkaFlow{f}
	}
	out := make([]KafkaFlow, 0, len(groups))
	for _, g := range groups {
		c := f
		c.Group, c.GroupUnresolved = g.Group, g.GroupUnresolved
		out = append(out, c)
	}
	return out
}
