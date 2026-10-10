// Package redledger is conformance:red-ledger's logic: the agreement between a
// tree's red tapes (conformance/red/*.json) and the F18 child nodes of the
// project graph, in both directions.
//
// A red case is a named check that fails against the shipped core; its ledger
// node is the item that says someone owns the gap. The pair must exist
// together. A node closed while its case still stands, an open node whose case
// is gone, a case no node names, and a node closed without a receipt are all
// findings. There is no exemption status.
//
// NEVER A PASS ON A GUESS. Hades unreachable, refusing (403), answering a page
// wrongly, or a tape that does not parse is could-not-run (state 2), not a
// finding about the tree and never a pass.
//
// THE LOGIC LIVES HERE, NOT IN THE BINARY'S main, so the mutation lane's
// mutants run against these tests (the hadescall lesson, foundry-tools#53).
package redledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// ID is the atom's name in reports.
const ID = "conformance:red-ledger"

// States, as the gate's verdicts spell them.
const (
	Pass      = 0
	Findings  = 1
	CannotRun = 2
)

// Prefix marks the ledger nodes this check owns: their key is Prefix+case id.
const Prefix = "class-gap:"

// TapeGlob is where a tree's red tapes live, relative to its root.
const TapeGlob = "conformance/red/*.json"

// MaxPages bounds the paging loop: a ledger of 43 nodes is one page at limit
// 500, so a list that keeps answering cursors is broken, not long.
const MaxPages = 20

const pageLimit = 500

// Ask posts one verb's JSON arguments to hades and answers its HTTP status
// and body. An error means hades was never asked or never answered.
type Ask func(ctx context.Context, verb, args string) (status int, body string, err error)

// Result is the atom's verdict.
type Result struct {
	State  int
	Reason string
	Lines  []string
}

var (
	tagRe    = regexp.MustCompile(`^[a-z][a-z0-9-]*/[a-z0-9][a-z0-9-]*$`)
	coreRe   = regexp.MustCompile(`^aiws:[a-z][a-z0-9-]*$`)
	prefixRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	commitRe = regexp.MustCompile(`^rob/[A-Za-z0-9._-]+@[0-9a-f]{40}$`)
)

// Case is one red case, the fields this check reads.
type Case struct {
	ID       string          `json:"id"`
	Tag      string          `json:"tag"`
	Core     string          `json:"core"`
	Verb     string          `json:"verb"`
	Args     json.RawMessage `json:"args"`
	Response json.RawMessage `json:"response"`
	Expect   string          `json:"expect"`
	RedAs    string          `json:"red-as"`
	Today    json.RawMessage `json:"today"`
	Absent   []string        `json:"absent"`
	Prov     string          `json:"provenance"`
	Note     string          `json:"note"`
	Status   string          `json:"status"`
	Trusted  bool            `json:"trusted"`
}

// Tape is one red tape file.
type Tape struct {
	Ledger string `json:"ledger"`
	Cases  []Case `json:"cases"`
}

// ParseTape reads one tape. Unknown fields are not refused: the schema is the
// stellar-core's to police, and this check reads the fields it needs.
func ParseTape(raw []byte) (Tape, error) {
	var t Tape
	if err := json.Unmarshal(raw, &t); err != nil {
		return Tape{}, err
	}
	return t, nil
}

// ShapeFindings checks every case's shape (the red envelope) and that every
// tape names the same ledger. names and tapes are parallel and sorted.
func ShapeFindings(names []string, tapes []Tape) []string {
	var out []string
	seen := map[string]string{}
	ledger, ledgerFile := "", ""
	for i, t := range tapes {
		file := names[i]
		if t.Ledger == "" {
			out = append(out, file+": names no ledger node")
		} else if ledger == "" {
			ledger, ledgerFile = t.Ledger, file
		} else if t.Ledger != ledger {
			out = append(out, fmt.Sprintf("%s: names ledger %s, but %s names %s", file, t.Ledger, ledgerFile, ledger))
		}
		if len(t.Cases) == 0 {
			out = append(out, file+": holds no case")
		}
		for _, c := range t.Cases {
			where := file + ": " + c.ID
			for _, why := range caseShape(c) {
				out = append(out, where+": "+why)
			}
			if first, dup := seen[c.ID]; dup {
				out = append(out, fmt.Sprintf("%s: id repeats the one in %s", where, first))
			}
			seen[c.ID] = file
		}
	}
	return out
}

func caseShape(c Case) []string {
	var w []string
	if c.ID == "" {
		w = append(w, "no id")
	} else if pre, ok := strings.CutSuffix(c.ID, "."+c.Tag); !ok || !prefixRe.MatchString(pre) {
		w = append(w, fmt.Sprintf("id is not <core>.<tag> for tag %q", c.Tag))
	}
	if !tagRe.MatchString(c.Tag) {
		w = append(w, fmt.Sprintf("tag %q is not <class>/<property>", c.Tag))
	}
	if !coreRe.MatchString(c.Core) {
		w = append(w, fmt.Sprintf("core %q is not aiws:<package>", c.Core))
	}
	if c.Verb == "" {
		w = append(w, "no verb")
	}
	if len(c.Args) == 0 {
		w = append(w, "no args")
	}
	if len(c.Response) == 0 {
		w = append(w, "no response")
	}
	if c.Expect != "red" {
		w = append(w, fmt.Sprintf("expect is %q, want \"red\"", c.Expect))
	}
	switch c.RedAs {
	case "violated":
		if len(c.Today) == 0 {
			w = append(w, "violated and no today")
		}
		if len(c.Absent) > 0 {
			w = append(w, "violated and carries absent")
		}
	case "unanalyzable":
		if len(c.Absent) == 0 {
			w = append(w, "unanalyzable and no absent")
		}
		for _, m := range c.Absent {
			if strings.TrimSpace(m) == "" {
				w = append(w, "an absent member is blank")
			}
		}
		if len(c.Today) > 0 {
			w = append(w, "unanalyzable and carries today")
		}
	default:
		w = append(w, fmt.Sprintf("red-as is %q, want violated or unanalyzable", c.RedAs))
	}
	if strings.TrimSpace(c.Prov) == "" {
		w = append(w, "no provenance")
	}
	if strings.TrimSpace(c.Note) == "" {
		w = append(w, "no note")
	}
	if c.Status != "ok" || !c.Trusted {
		w = append(w, "status must be ok and trusted true")
	}
	return w
}

// Node is one ledger child as the graph holds it.
type Node struct {
	ID, Key, Name, Status string
	Commits               []string
}

// Closed reports whether the status ends a node.
func (n Node) Closed() bool {
	switch n.Status {
	case "done", "cut", "killed", "mistrial":
		return true
	}
	return false
}

// resolved reports whether a name carries a resolution prefix.
func resolved(name string) bool {
	for _, p := range []string{"[I]", "[II]", "[III]"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// Compare is the set comparison: red case ids against the ledger's nodes.
func Compare(cases []string, nodes []Node) []string {
	var out []string
	red := map[string]bool{}
	for _, id := range cases {
		red[id] = true
	}
	byKey := map[string][]Node{}
	for _, n := range nodes {
		byKey[n.Key] = append(byKey[n.Key], n)
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		id := strings.TrimPrefix(k, Prefix)
		ns := byKey[k]
		if len(ns) > 1 {
			out = append(out, fmt.Sprintf("%s: %d ledger nodes carry this key, want one", id, len(ns)))
		}
		for _, n := range ns {
			if n.Closed() && red[id] {
				out = append(out, fmt.Sprintf("%s: item closed while its check still fails (node %s is %s, the red case stands)", id, short(n.ID), n.Status))
			}
			if !n.Closed() && !red[id] {
				out = append(out, fmt.Sprintf("%s: check passes (or was removed) while its item is open (node %s is %s, no red case)", id, short(n.ID), n.Status))
			}
			if n.Closed() {
				if len(n.Commits) == 0 {
					out = append(out, fmt.Sprintf("%s: node %s is %s with no hasCommit receipt", id, short(n.ID), n.Status))
				}
				for _, c := range n.Commits {
					if !commitRe.MatchString(c) {
						out = append(out, fmt.Sprintf("%s: receipt %q is not rob/<repo>@<40-hex>", id, c))
					}
				}
				if !resolved(n.Name) {
					out = append(out, fmt.Sprintf("%s: node %s is %s but its name carries no [I], [II] or [III] resolution", id, short(n.ID), n.Status))
				}
			}
		}
	}
	ids := append([]string(nil), cases...)
	sort.Strings(ids)
	for _, id := range ids {
		if _, ok := byKey[Prefix+id]; !ok {
			out = append(out, id+": red case has no ledger node")
		}
	}
	return out
}

func short(id string) string {
	return id[:min(len(id), 12)]
}

// couldNot builds the could-not-run result.
func couldNot(format string, a ...any) Result {
	return Result{State: CannotRun, Reason: ID + ": CANNOT RUN - " + fmt.Sprintf(format, a...)}
}

// call asks hades and unwraps the tool answer's text. Every way of not getting
// one is an error worded as a could-not-run reason.
func call(ctx context.Context, ask Ask, verb string, args any) (string, error) {
	b, _ := json.Marshal(args) // maps of strings and numbers always encode
	status, body, err := ask(ctx, verb, string(b))
	if err != nil {
		return "", fmt.Errorf("hades was not asked %s: %v", verb, err)
	}
	if status == 403 {
		return "", fmt.Errorf("hades refused %s (403): the CI identity holds no grant for it: %.300s", verb, body)
	}
	if status != 200 {
		return "", fmt.Errorf("hades answered %s with HTTP %d: %.300s", verb, status, body)
	}
	var env struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil || len(env.Content) == 0 {
		return "", fmt.Errorf("%s answered a 200 that is not a tool answer: %.200q", verb, body)
	}
	if env.IsError {
		return "", fmt.Errorf("%s refused: %.300s", verb, env.Content[0].Text)
	}
	return env.Content[0].Text, nil
}

type row struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Key    string `json:"key"`
	Edges  []struct {
		Predicate string `json:"predicate"`
		To        string `json:"to"`
	} `json:"edges"`
}

// Nodes reads the ledger's class-gap children, every status, every page, and
// the receipts of the closed ones.
func Nodes(ctx context.Context, ask Ask, ledger string) ([]Node, error) {
	var nodes []Node
	cursor := ""
	seen := map[string]bool{}
	for range MaxPages {
		args := map[string]any{"filter": map[string]any{"parent": ledger}, "limit": pageLimit}
		if cursor != "" {
			args["cursor"] = cursor
		}
		text, err := call(ctx, ask, "plan_task_list", args)
		if err != nil {
			return nil, err
		}
		var p struct {
			Rows []row   `json:"rows"`
			Next *string `json:"next_cursor"`
		}
		if err := json.Unmarshal([]byte(text), &p); err != nil {
			return nil, fmt.Errorf("plan_task_list's answer is not a page: %.200q", text)
		}
		for _, r := range p.Rows {
			if strings.HasPrefix(r.Key, Prefix) {
				nodes = append(nodes, Node{ID: r.ID, Key: r.Key, Name: r.Name, Status: r.Status})
			}
		}
		if p.Next == nil || *p.Next == "" {
			return nodes, receipts(ctx, ask, nodes)
		}
		if seen[*p.Next] {
			return nil, fmt.Errorf("plan_task_list answered the cursor %q twice", *p.Next)
		}
		seen[*p.Next] = true
		cursor = *p.Next
	}
	return nil, fmt.Errorf("plan_task_list is still paging after %d pages", MaxPages)
}

// receipts fills Commits on the closed nodes from plan_task_read's edges.
func receipts(ctx context.Context, ask Ask, nodes []Node) error {
	var ids []string
	at := map[string]int{}
	for i, n := range nodes {
		if n.Closed() {
			ids = append(ids, n.ID)
			at[n.ID] = i
		}
	}
	for batch := range slices.Chunk(ids, pageLimit) {
		text, err := call(ctx, ask, "plan_task_read", map[string]any{"ids": batch})
		if err != nil {
			return err
		}
		var a struct {
			Rows    []row    `json:"rows"`
			Missing []string `json:"missing"`
		}
		if err := json.Unmarshal([]byte(text), &a); err != nil {
			return fmt.Errorf("plan_task_read's answer is not rows: %.200q", text)
		}
		if len(a.Missing) > 0 {
			return fmt.Errorf("plan_task_read found no node for %d listed id(s): %v", len(a.Missing), a.Missing)
		}
		if len(a.Rows) != len(batch) {
			return fmt.Errorf("plan_task_read answered %d rows for %d ids", len(a.Rows), len(batch))
		}
		for _, r := range a.Rows {
			i, ok := at[r.ID]
			if !ok {
				return fmt.Errorf("plan_task_read answered a node (%s) that was not asked", short(r.ID))
			}
			for _, e := range r.Edges {
				if e.Predicate == "hasCommit" {
					nodes[i].Commits = append(nodes[i].Commits, e.To)
				}
			}
		}
	}
	return nil
}

// Check is the atom over a tree: absent without red tapes, else the shapes
// and the comparison against the live ledger.
func Check(ctx context.Context, tree fs.FS, ask Ask) Result {
	files, err := fs.Glob(tree, TapeGlob)
	if err != nil {
		return couldNot("the tree would not enumerate: %v", err)
	}
	if len(files) == 0 {
		return Result{State: Pass, Reason: ID + ": ABSENT - this tree tracks no " + TapeGlob + ", so it declares no red case for the ledger to pair"}
	}
	sort.Strings(files)
	var tapes []Tape
	var cases []string
	for _, f := range files {
		raw, err := fs.ReadFile(tree, f)
		if err != nil {
			return couldNot("%s would not read: %v", f, err)
		}
		t, err := ParseTape(raw)
		if err != nil {
			return couldNot("%s does not parse: %v", path.Base(f), err)
		}
		tapes = append(tapes, t)
		for _, c := range t.Cases {
			cases = append(cases, c.ID)
		}
	}
	findings := ShapeFindings(files, tapes)
	ledger := tapes[0].Ledger
	for _, t := range tapes {
		if t.Ledger != "" {
			ledger = t.Ledger
			break
		}
	}
	if ledger == "" {
		// Nothing says which node to read: the shapes already name that.
		return judged(findings, len(cases), 0)
	}
	nodes, err := Nodes(ctx, ask, ledger)
	if err != nil {
		return couldNot("%v. A ledger that was never read agrees with nothing.", err)
	}
	findings = append(findings, Compare(cases, nodes)...)
	open := 0
	for _, n := range nodes {
		if !n.Closed() {
			open++
		}
	}
	return judged(findings, len(cases), open)
}

func judged(findings []string, red, open int) Result {
	if len(findings) > 0 {
		return Result{State: Findings, Reason: ID + ": findings - " + strconv.Itoa(len(findings)) + " disagreement(s) between the red tapes and the ledger", Lines: findings}
	}
	return Result{State: Pass, Reason: fmt.Sprintf("%s: %d open ledger node(s) = %d red case(s)", ID, open, red)}
}

// HadesAsk adapts hadescall's Run (its stdout is "HTTP <status>" and the
// body) to an Ask. A nonzero exit is hadescall's could-not-ask.
func HadesAsk(run func(ctx context.Context, args []string, stdout, stderr *strings.Builder) int) Ask {
	return func(ctx context.Context, verb, args string) (int, string, error) {
		var out, errs strings.Builder
		if code := run(ctx, []string{verb, args}, &out, &errs); code != 0 {
			return 0, "", errors.New(strings.TrimSpace(errs.String()))
		}
		head, body, _ := strings.Cut(out.String(), "\n")
		code, ok := strings.CutPrefix(strings.TrimSpace(head), "HTTP ")
		if !ok {
			return 0, "", fmt.Errorf("hadescall answered no status line: %.200q", out.String())
		}
		status, err := strconv.Atoi(code)
		if err != nil {
			return 0, "", fmt.Errorf("hadescall answered a status that is not a number: %.200q", head)
		}
		return status, body, nil
	}
}

// Render writes a Result as the report: the reason, then one line a finding.
func Render(r Result) string {
	return strings.Join(append([]string{r.Reason}, r.Lines...), "\n") + "\n"
}
