package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

// fleet:consumed-events-emitted's judgement.
//
// THE DEFECT IS A READER OF AN EVENT NOBODY WRITES. A delivery path was retired
// with no replacement (the commit hook; the kit lay), and every consumer that
// still filtered session-events on event_type 'commit' went dark for a month:
// nothing errored, because an empty read is a correct answer to a question no
// producer is left to satisfy.
//
// THE CHECK IS STATIC AND FLEET-WIDE. It reads this tree for the event_type
// values it CONSUMES (SQL `event_type = 'x'`, a comparison against a literal, a
// membership test against a constant set, a read verb called with one), and for
// each that this tree does not itself emit, asks the fleet through the door
// whether any tracked source emits it. A type nothing emits is a finding.
//
// WHAT IT CANNOT SEE, so a reader trusts the right amount: an event_type built
// at run time from a variable is not a literal, so it neither counts as an
// emitter nor as a consumer. A fleet that emits only dynamically for a type a
// consumer reads will be flagged; the answer is to name the type once in the
// emitter as a constant, which is also what makes it greppable.

// EventSite is one file that names an event type.
type EventSite struct{ Type, File string }

var (
	// emitRe is `event_type: "x"`, `"event_type": "x"`, `event_type="x"`,
	// `EventType: "x"`: the key spelled any of its ways with a literal value.
	emitRe = regexp.MustCompile(`(?i)event_?type["'` + "`" + `]?\s*(?::=|[:=])\s*["'` + "`" + `]([A-Za-z0-9_.:-]+)["'` + "`" + `]`)
	// emitConstRe is the same key with an identifier value, resolved against
	// the tree's own string constants.
	emitConstRe = regexp.MustCompile(`(?i)event_?type["'` + "`" + `]?\s*(?::=|[:=])\s*([A-Za-z_][A-Za-z0-9_.]*)\b`)
	// constDefRe is `NAME = "x"`, `const name = "x"`, `name string = "x"`.
	constDefRe = regexp.MustCompile(`(?m)^[ \t]*(?:const[ \t]+|var[ \t]+)?([A-Za-z_][A-Za-z0-9_]*)[ \t]*(?:[A-Za-z_*\[\]]+[ \t]*)?(?::?=)[ \t]*["'` + "`" + `]([A-Za-z0-9_.:-]+)["'` + "`" + `][ \t]*(?:;|//|#|$)`)

	sqlEqRe  = regexp.MustCompile(`(?i)event_?type\s*(?:=|<>|!=)\s*'([A-Za-z0-9_.:-]+)'`)
	sqlInRe  = regexp.MustCompile(`(?i)event_?type\s+(?:NOT\s+)?IN\s*\(([^)]*)\)`)
	cmpRe    = regexp.MustCompile(`(?i)event_?type["'` + "`" + `]?[\])]*\s*(?:===|!==|==|!=)\s*["'` + "`" + `]([A-Za-z0-9_.:-]+)["'` + "`" + `]`)
	memberRe = regexp.MustCompile(`(?i)event_?type["'` + "`" + `]?[\])]*\s+(?:not\s+)?in\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	// readCallRe is a read verb followed, in the same call, by an event_type
	// literal: tartarus_session_events_read, get_session_events, and so on.
	readCallRe = regexp.MustCompile(`(?is)(?:session_?events_?read|get_?session_?events|read_?session_?events)\b.{0,240}?event_?type["'` + "`" + `]?\s*(?::=|[:=,])\s*["'` + "`" + `]([A-Za-z0-9_.:-]+)["'` + "`" + `]`)
	// aliasRe is `etype = event.get("event_type")` / `et := e.EventType`: a name
	// that now holds the event type, compared further down.
	aliasRe = regexp.MustCompile(`(?im)^[ \t]*([A-Za-z_][A-Za-z0-9_]*)[ \t]*(?::=|=)[^\n=]*event_?type`)
	// switchRe opens `switch e.EventType {`; its case literals are consumed.
	switchRe = regexp.MustCompile(`(?i)switch\s+[A-Za-z_][A-Za-z0-9_.]*event_?type\s*\{`)
	caseRe   = regexp.MustCompile(`(?m)^\s*case\s+((?:"[A-Za-z0-9_.:-]+"\s*,?\s*)+):`)
	quotedRe = regexp.MustCompile(`["']([A-Za-z0-9_.:-]+)["']`)
)

// eventSourceSkip is a path whose event types are not the tree's own:
// tests (which fabricate emitters and consumers alike), vendored and generated
// code, fixtures.
func eventSourceSkip(p string) bool {
	l := strings.ToLower(p)
	for _, seg := range strings.Split(l, "/") {
		switch seg {
		case "vendor", "node_modules", "testdata", "test", "tests", "fixtures", "fixture", "dist", "target", "__pycache__", "specs", "docs":
			return true
		}
	}
	base := l[strings.LastIndex(l, "/")+1:]
	return strings.HasSuffix(base, "_test.go") || strings.HasPrefix(base, "test_") ||
		strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") ||
		strings.HasSuffix(base, ".d.ts")
}

// EventSourceExt is the set of files the check reads.
func EventSourceExt(p string) bool {
	for _, e := range []string{".go", ".py", ".ts", ".tsx", ".js", ".mjs", ".sh", ".rs", ".sql", ".json", ".md"} {
		if strings.HasSuffix(p, e) {
			return !eventSourceSkip(p)
		}
	}
	return false
}

// emitDocRe is an emit CALL spelled in prose: a skill that tells the model to
// `tartarus_emit(event_type="pushback", ...)` is the live emitter of that type
// (the calibration skill emits pushback this way and nothing else does), so a
// markdown file counts as an emitter, and only through a call.
var emitDocRe = regexp.MustCompile(`(?is)emit\w*\([^)]{0,200}?event_?type\s*[=:]\s*["']([A-Za-z0-9_.:-]+)["']`)

// EventTypeUses reads files (path -> contents) and answers the event types
// they consume and the event types they emit, each with the files that name it.
func EventTypeUses(files map[string]string) (consumed, emitted []EventSite) {
	consts := map[string]map[string]bool{}
	for p, body := range files {
		if !EventSourceExt(p) {
			continue
		}
		for _, m := range constDefRe.FindAllStringSubmatch(body, -1) {
			if consts[m[1]] == nil {
				consts[m[1]] = map[string]bool{}
			}
			consts[m[1]][m[2]] = true
		}
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		if EventSourceExt(p) {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	seenC, seenE := map[EventSite]bool{}, map[EventSite]bool{}
	addC := func(t, f string) {
		if s := (EventSite{t, f}); t != "" && !seenC[s] {
			seenC[s] = true
			consumed = append(consumed, s)
		}
	}
	addE := func(t, f string) {
		if s := (EventSite{t, f}); t != "" && !seenE[s] {
			seenE[s] = true
			emitted = append(emitted, s)
		}
	}
	for _, p := range paths {
		body := files[p]
		if strings.HasSuffix(p, ".md") {
			for _, m := range emitDocRe.FindAllStringSubmatch(body, -1) {
				addE(m[1], p)
			}
			continue
		}
		// A consumption span is not also an emission: `"event_type": "commit"`
		// inside a read call is the filter, and a WHERE clause is a filter.
		type span struct{ from, to int }
		var reads []span
		for _, m := range readCallRe.FindAllStringSubmatchIndex(body, -1) {
			addC(body[m[2]:m[3]], p)
			reads = append(reads, span{m[0], m[1]})
		}
		for _, m := range sqlEqRe.FindAllStringSubmatch(body, -1) {
			addC(m[1], p)
		}
		for _, m := range sqlInRe.FindAllStringSubmatch(body, -1) {
			for _, q := range quotedRe.FindAllStringSubmatch(m[1], -1) {
				addC(q[1], p)
			}
		}
		for _, m := range cmpRe.FindAllStringSubmatch(body, -1) {
			addC(m[1], p)
		}
		for _, m := range memberRe.FindAllStringSubmatch(body, -1) {
			for _, t := range setConstants(body, m[1]) {
				addC(t, p)
			}
		}
		for _, m := range aliasRe.FindAllStringSubmatch(body, -1) {
			re := regexp.MustCompile(`\b` + regexp.QuoteMeta(m[1]) + `\s*(?:===|!==|==|!=)\s*["']([A-Za-z0-9_.:-]+)["']`)
			for _, c := range re.FindAllStringSubmatch(body, -1) {
				addC(c[1], p)
			}
			in := regexp.MustCompile(`\b` + regexp.QuoteMeta(m[1]) + `\s+(?:not\s+)?in\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
			for _, c := range in.FindAllStringSubmatch(body, -1) {
				for _, t := range setConstants(body, c[1]) {
					addC(t, p)
				}
			}
		}
		for _, m := range switchRe.FindAllStringIndex(body, -1) {
			end := min(m[1]+2000, len(body))
			for _, c := range caseRe.FindAllStringSubmatch(body[m[1]:end], -1) {
				for _, q := range quotedRe.FindAllStringSubmatch(c[1], -1) {
					addC(q[1], p)
				}
			}
		}
		inRead := func(at int) bool {
			for _, s := range reads {
				if at >= s.from && at < s.to {
					return true
				}
			}
			return false
		}
		for _, m := range emitRe.FindAllStringSubmatchIndex(body, -1) {
			if inRead(m[0]) || filterLine(body, m[0]) {
				continue
			}
			addE(body[m[2]:m[3]], p)
		}
		for _, m := range emitConstRe.FindAllStringSubmatchIndex(body, -1) {
			if inRead(m[0]) || filterLine(body, m[0]) {
				continue
			}
			name := body[m[2]:m[3]]
			if i := strings.LastIndex(name, "."); i != -1 {
				name = name[i+1:]
			}
			for t := range consts[name] {
				addE(t, p)
			}
		}
	}
	return consumed, emitted
}

// filterLine reports whether the line holding byte offset at is a SQL filter
// (WHERE/AND/SELECT/FROM), which reads an event type rather than writing one.
func filterLine(body string, at int) bool {
	start := strings.LastIndex(body[:at], "\n") + 1
	line := strings.ToUpper(body[start:at])
	return strings.Contains(line, "WHERE ") || strings.Contains(line, " AND ") ||
		strings.Contains(line, "SELECT ") || strings.Contains(line, " FROM ")
}

// setConstants answers the quoted literals of `NAME = {...}`, `(...)`, `[...]`
// or `frozenset({...})` in body.
func setConstants(body, name string) []string {
	re := regexp.MustCompile(`(?s)\b` + regexp.QuoteMeta(name) + `\b\s*(?::[^=\n]+)?=\s*(?:frozenset\(|set\(|tuple\(|\[\]string)?\s*[\(\[{]([^)\]}]*)[\)\]}]`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		return nil
	}
	var out []string
	for _, q := range quotedRe.FindAllStringSubmatch(m[1], -1) {
		out = append(out, q[1])
	}
	return out
}

// Fleet is the door's view of the tracked fleet.
type fleetRepos struct {
	Owner string   `json:"owner"`
	Repos []string `json:"repos"`
}

type treeEntries struct {
	Entries []struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
		Mode string `json:"mode"`
	} `json:"entries"`
}

// EventScanMaxBytes bounds one file the fleet scan reads.
const EventScanMaxBytes = 300_000

// getJSON reads one door endpoint (custody/repos, tree) as JSON.
func (d Door) getJSON(ctx context.Context, endpoint string, q url.Values, into any) error {
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: DoorTimeout}
	}
	u := strings.TrimRight(d.Base, "/") + endpoint
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, u)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

// FleetEmitters answers, for each wanted event type, one fleet file that emits
// it, scanning every tracked repo except self through the door. It stops as soon
// as every wanted type has an emitter. A repo or file the door would not answer
// is an error: a type reported unemitted after a partial scan would be a guess.
func FleetEmitters(ctx context.Context, door Door, self string, wanted []string) (map[string]string, error) {
	var repos fleetRepos
	if err := door.getJSON(ctx, "/custody/repos", nil, &repos); err != nil {
		return nil, fmt.Errorf("the fleet listing: %w", err)
	}
	want := map[string]bool{}
	for _, w := range wanted {
		want[w] = true
	}
	found := map[string]string{}
	var mu sync.Mutex
	var left atomic.Int64
	left.Store(int64(len(want)))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var g errgroup.Group
	g.SetLimit(16)
	for _, repo := range repos.Repos {
		if strings.HasPrefix(repo, "forge-archives/") || repo == self || strings.HasSuffix(repo, "/"+self) {
			continue
		}
		var tree treeEntries
		if err := door.getJSON(ctx, "/tree", url.Values{"repo": {repo}}, &tree); err != nil {
			if ctx.Err() != nil {
				break
			}
			return nil, fmt.Errorf("the tree of %s: %w", repo, err)
		}
		for _, e := range tree.Entries {
			if e.Size == 0 || e.Size > EventScanMaxBytes || !EventSourceExt(e.Path) || e.Mode == "0120000" || e.Mode == "0160000" {
				continue
			}
			g.Go(func() error {
				if ctx.Err() != nil || left.Load() == 0 {
					return nil
				}
				status, body, err := door.Get(ctx, repo, e.Path)
				if err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return fmt.Errorf("%s %s: %w", repo, e.Path, err)
				}
				if status != http.StatusOK {
					return fmt.Errorf("%s %s: HTTP %d", repo, e.Path, status)
				}
				_, emitted := EventTypeUses(map[string]string{e.Path: string(body)})
				mu.Lock()
				for _, s := range emitted {
					if want[s.Type] && found[s.Type] == "" {
						found[s.Type] = repo + ":" + e.Path
						if left.Add(-1) == 0 {
							cancel()
						}
					}
				}
				mu.Unlock()
				return nil
			})
		}
	}
	err := g.Wait()
	if left.Load() == 0 {
		return found, nil
	}
	return found, err
}

// ConsumedEventsEmitted judges one tree. consumed and emitted are the tree's
// own EventTypeUses; scan asks the fleet for the types the tree consumes and
// does not emit (nil scan is a door that was not asked, which cannot occur in
// production). An error from the fleet scan is CANNOT RUN, never a finding.
func ConsumedEventsEmitted(ctx context.Context, files map[string]string, scan func(ctx context.Context, wanted []string) (map[string]string, error)) (int, string) {
	const id = "fleet:consumed-events-emitted"
	consumed, emitted := EventTypeUses(files)
	if len(consumed) == 0 {
		return 0, id + ": this tree consumes no literal event_type"
	}
	local := map[string]bool{}
	for _, e := range emitted {
		local[e.Type] = true
	}
	byType := map[string][]string{}
	var types []string
	for _, c := range consumed {
		if byType[c.Type] == nil {
			types = append(types, c.Type)
		}
		byType[c.Type] = append(byType[c.Type], c.File)
	}
	sort.Strings(types)
	var need []string
	for _, t := range types {
		if !local[t] {
			need = append(need, t)
		}
	}
	if len(need) == 0 {
		return 0, fmt.Sprintf("%s: %d consumed event type(s), all emitted by this tree", id, len(types))
	}
	found, err := scan(ctx, need)
	if err != nil {
		return 2, fmt.Sprintf("%s: CANNOT RUN - the fleet scan did not finish (%v). A type reported unemitted after a partial scan is a guess, not a finding.", id, err)
	}
	var lines []string
	for _, t := range need {
		if found[t] == "" {
			lines = append(lines, fmt.Sprintf("event-types: '%s' is consumed (%s) and no source in the fleet emits it", t, strings.Join(firstN(byType[t], 3), ", ")))
		}
	}
	if len(lines) == 0 {
		return 0, fmt.Sprintf("%s: %d consumed event type(s); every one has an emitter (%d outside this tree)", id, len(types), len(need))
	}
	lines = append(lines,
		"A consumer of an event type nothing emits goes silent without an error: an empty read is a correct answer to a question no producer is left to satisfy (the commit hook retired with no replacement; consumers dark for a month). "+
			"Restore the emitter, move the consumer to the type that is emitted, or retire the consumer. An emitter that builds the type at run time is invisible here: name the type once as a constant.")
	return 1, strings.Join(lines, "\n")
}

func firstN(s []string, n int) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	if len(out) > n {
		out = append(out[:n], fmt.Sprintf("+%d more", len(out)-n))
	}
	return out
}
