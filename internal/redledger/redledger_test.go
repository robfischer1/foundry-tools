package redledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

const (
	ledger = "01a123fc27b39e78efb0d3be0ac13677d710b83c6cf470a281fb69213ea526a6"
	sha    = "rob/stellar-core@b40e8fa0000000000000000000000000000000aa"
)

func caseJSON(id, tag string, extra string) string {
	return fmt.Sprintf(`{"id":%q,"tag":%q,"core":"aiws:stamp","verb":"stamp.decide","args":{},"response":{"ok":1},"expect":"red","red-as":"violated","today":{"ok":2},"provenance":"p","note":"n","status":"ok","trusted":true%s}`, id, tag, extra)
}

func tapeJSON(led string, cases ...string) string {
	return fmt.Sprintf(`{"ledger":%q,"cases":[%s]}`, led, strings.Join(cases, ","))
}

func tree(tapes map[string]string) fstest.MapFS {
	t := fstest.MapFS{"README.md": {Data: []byte("x")}}
	for name, body := range tapes {
		t["conformance/red/"+name] = &fstest.MapFile{Data: []byte(body)}
	}
	return t
}

// node is a ledger child as plan_task_list answers it.
type node struct {
	id, key, name, status string
	commits               []string
}

// hades is a scripted hades: pages of the list, reads, and failures.
type hades struct {
	nodes    []node
	pageSize int
	status   map[string]int    // verb -> HTTP status override
	body     map[string]string // verb -> raw body override
	err      map[string]error
	asked    []string
	args     []map[string]any
}

func text(isErr bool, v any) string {
	b, _ := json.Marshal(v)
	env, _ := json.Marshal(map[string]any{"isError": isErr, "content": []map[string]string{{"type": "text", "text": string(b)}}})
	return string(env)
}

func (h *hades) ask(_ context.Context, verb, args string) (int, string, error) {
	h.asked = append(h.asked, verb)
	var a map[string]any
	_ = json.Unmarshal([]byte(args), &a)
	h.args = append(h.args, a)
	if e := h.err[verb]; e != nil {
		return 0, "", e
	}
	if s := h.status[verb]; s != 0 {
		return s, h.body[verb], nil
	}
	if b, ok := h.body[verb]; ok {
		return 200, b, nil
	}
	switch verb {
	case "plan_task_list":
		size := h.pageSize
		if size == 0 {
			size = len(h.nodes) + 1
		}
		from := 0
		if c, _ := a["cursor"].(string); c != "" {
			fmt.Sscanf(c, "c%d", &from)
		}
		to := min(from+size, len(h.nodes))
		rows := []map[string]string{}
		for _, n := range h.nodes[from:to] {
			rows = append(rows, map[string]string{"id": n.id, "key": n.key, "name": n.name, "status": n.status})
		}
		var next any
		if to < len(h.nodes) {
			next = fmt.Sprintf("c%d", to)
		}
		return 200, text(false, map[string]any{"rows": rows, "next_cursor": next}), nil
	case "plan_task_read":
		var rows []map[string]any
		for _, id := range a["ids"].([]any) {
			for _, n := range h.nodes {
				if n.id == id {
					edges := []map[string]any{{"predicate": "hasName", "to": "x"}}
					for _, c := range n.commits {
						edges = append(edges, map[string]any{"predicate": "hasCommit", "to": c})
					}
					rows = append(rows, map[string]any{"id": n.id, "edges": edges})
				}
			}
		}
		return 200, text(false, map[string]any{"rows": rows, "missing": []string{}}), nil
	}
	return 404, "no verb", nil
}

func open(id string) node {
	return node{id: "open-" + id, key: Prefix + id, name: "[pending] " + id, status: "backlog"}
}

func closed(id, name string, commits ...string) node {
	return node{id: "done-" + id, key: Prefix + id, name: name, status: "done", commits: commits}
}

const (
	a = "stamp.cas-register/one"
	b = "stamp.casefold-match/two"
)

func twoCases() fstest.MapFS {
	return tree(map[string]string{"stamp.json": tapeJSON(ledger,
		caseJSON(a, "cas-register/one", ""), caseJSON(b, "casefold-match/two", ""))})
}

func has(t *testing.T, r Result, state int, want ...string) {
	t.Helper()
	if r.State != state {
		t.Fatalf("state %d, want %d: %s\n%v", r.State, state, r.Reason, r.Lines)
	}
	all := r.Reason + "\n" + strings.Join(r.Lines, "\n")
	for _, w := range want {
		if !strings.Contains(all, w) {
			t.Errorf("missing %q in:\n%s", w, all)
		}
	}
}

func TestAbsentWithoutRedTapes(t *testing.T) {
	h := &hades{}
	r := Check(context.Background(), fstest.MapFS{"conformance/tapes/x.json": {Data: []byte("{}")}, "conformance/red/notes.md": {}}, h.ask)
	has(t, r, Pass, "ABSENT", TapeGlob)
	if len(h.asked) != 0 {
		t.Errorf("an absent tree asked hades: %v", h.asked)
	}
}

func TestPassWhenEveryOpenNodeHasItsCase(t *testing.T) {
	h := &hades{nodes: []node{open(a), open(b), {id: "other", key: "other-key", name: "x", status: "backlog"},
		closed("old.gone/x", "[I] old", sha)}}
	r := Check(context.Background(), twoCases(), h.ask)
	has(t, r, Pass)
	if r.Reason != ID+": 2 open ledger node(s) = 2 red case(s)" {
		t.Errorf("reason %q", r.Reason)
	}
	if len(r.Lines) != 0 {
		t.Errorf("a pass carries lines: %v", r.Lines)
	}
	// every status, scoped to the ledger, asked of the graph.
	f := h.args[0]["filter"].(map[string]any)
	if f["parent"] != ledger || f["status"] != nil || h.args[0]["limit"] != float64(500) {
		t.Errorf("list args %v", h.args[0])
	}
	if _, ok := h.args[0]["cursor"]; ok {
		t.Errorf("first page carries a cursor: %v", h.args[0])
	}
}

// Direction 1: the item is closed while its check still fails.
func TestClosedNodeWithAStandingCaseIsAFinding(t *testing.T) {
	h := &hades{nodes: []node{open(a), closed(b, "[I] two", sha)}}
	has(t, Check(context.Background(), twoCases(), h.ask), Findings,
		b+": item closed while its check still fails")
	for _, st := range []string{"done", "cut", "killed", "mistrial"} {
		n := closed(b, "[III] two", sha)
		n.status = st
		h := &hades{nodes: []node{open(a), n}}
		has(t, Check(context.Background(), twoCases(), h.ask), Findings, "item closed while its check still fails")
	}
}

// Direction 2: the check passes (or was removed) while the item is open.
func TestOpenNodeWithoutACaseIsAFinding(t *testing.T) {
	h := &hades{nodes: []node{open(a), open(b), open("tail.gone/three")}}
	r := Check(context.Background(), twoCases(), h.ask)
	has(t, r, Findings, "tail.gone/three: check passes (or was removed) while its item is open")
	if len(r.Lines) != 1 {
		t.Errorf("lines %v", r.Lines)
	}
	for _, st := range []string{"backlog", "in-progress", "blocked", ""} {
		n := open("x.y/z")
		n.status = st
		h := &hades{nodes: []node{open(a), open(b), n}}
		has(t, Check(context.Background(), twoCases(), h.ask), Findings, "x.y/z: check passes")
	}
}

func TestACaseWithNoNodeIsAFinding(t *testing.T) {
	h := &hades{nodes: []node{open(a)}}
	has(t, Check(context.Background(), twoCases(), h.ask), Findings, b+": red case has no ledger node")
}

func TestAClosedNodeNeedsAReceiptAndAResolution(t *testing.T) {
	cases := map[string]struct {
		n    node
		want string
	}{
		"no receipt":     {closed("g.h/i", "[I] x"), "with no hasCommit receipt"},
		"malformed":      {closed("g.h/i", "[I] x", "abc123"), `receipt "abc123" is not rob/<repo>@<40-hex>`},
		"short sha":      {closed("g.h/i", "[I] x", "rob/r@abc"), "is not rob/<repo>@<40-hex>"},
		"upper sha":      {closed("g.h/i", "[I] x", "rob/r@"+strings.Repeat("A", 40)), "is not rob/<repo>@<40-hex>"},
		"pending name":   {closed("g.h/i", "[pending] x", sha), "carries no [I], [II] or [III] resolution"},
		"unprefixed":     {closed("g.h/i", "x [I]", sha), "carries no [I], [II] or [III] resolution"},
		"wrong brackets": {closed("g.h/i", "[IV] x", sha), "carries no [I], [II] or [III] resolution"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := &hades{nodes: []node{open(a), open(b), c.n}}
			has(t, Check(context.Background(), twoCases(), h.ask), Findings, "g.h/i: ", c.want)
		})
	}
	// a cut with a receipt naming the green case is allowed, as is each resolution.
	for _, name := range []string{"[I] x", "[II] x", "[III] x"} {
		n := closed("g.h/i", name, sha)
		n.status = "cut"
		h := &hades{nodes: []node{open(a), open(b), n}}
		has(t, Check(context.Background(), twoCases(), h.ask), Pass)
	}
	// ...and a cut without one is not.
	n := closed("g.h/i", "[III] x")
	n.status = "cut"
	h := &hades{nodes: []node{open(a), open(b), n}}
	has(t, Check(context.Background(), twoCases(), h.ask), Findings, "with no hasCommit receipt")
}

func TestTwoNodesForOneKeyIsAFinding(t *testing.T) {
	dup := open(a)
	dup.id = "open-dup"
	h := &hades{nodes: []node{open(a), dup, open(b)}}
	has(t, Check(context.Background(), twoCases(), h.ask), Findings, a+": 2 ledger nodes carry this key, want one")
}

func TestOnlyClosedNodesAreReadForReceipts(t *testing.T) {
	h := &hades{nodes: []node{open(a), open(b), closed("g.h/i", "[I] x", sha, sha)}}
	r := Check(context.Background(), twoCases(), h.ask)
	has(t, r, Pass)
	ids := h.args[len(h.args)-1]["ids"].([]any)
	if len(ids) != 1 || ids[0] != "done-g.h/i" {
		t.Errorf("read ids %v", ids)
	}
	// an all-open ledger asks no read.
	h = &hades{nodes: []node{open(a), open(b)}}
	Check(context.Background(), twoCases(), h.ask)
	if strings.Join(h.asked, ",") != "plan_task_list" {
		t.Errorf("asked %v", h.asked)
	}
}

func TestEveryPageOfTheLedgerIsRead(t *testing.T) {
	var nodes []node
	var cs []string
	for i := range 7 {
		id := fmt.Sprintf("stamp.p%d/q", i)
		nodes = append(nodes, open(id))
		cs = append(cs, caseJSON(id, fmt.Sprintf("p%d/q", i), ""))
	}
	tr := tree(map[string]string{"stamp.json": tapeJSON(ledger, cs...)})
	h := &hades{nodes: nodes, pageSize: 3}
	r7 := Check(context.Background(), tr, h.ask)
	has(t, r7, Pass)
	if r7.Reason != ID+": 7 open ledger node(s) = 7 red case(s)" {
		t.Errorf("reason %q", r7.Reason)
	}
	if got := strings.Join(h.asked, ","); got != "plan_task_list,plan_task_list,plan_task_list" {
		t.Errorf("asked %s", got)
	}
	if h.args[1]["cursor"] != "c3" || h.args[2]["cursor"] != "c6" {
		t.Errorf("cursors %v %v", h.args[1], h.args[2])
	}
	// a node on the last page still counts: drop its case.
	h = &hades{nodes: append(nodes, open("late.page/x")), pageSize: 3}
	has(t, Check(context.Background(), tr, h.ask), Findings, "late.page/x: check passes")
}

func TestReceiptsAreReadInBatchesOfAtMostFiveHundred(t *testing.T) {
	var nodes []node
	for i := range 501 {
		nodes = append(nodes, closed(fmt.Sprintf("g.h/n%d", i), "[I] x", sha))
	}
	h := &hades{nodes: nodes}
	r := Check(context.Background(), twoCases(), h.ask)
	has(t, r, Findings)
	var sizes []int
	for i, v := range h.asked {
		if v == "plan_task_read" {
			sizes = append(sizes, len(h.args[i]["ids"].([]any)))
		}
	}
	if len(sizes) != 2 || sizes[0] != 500 || sizes[1] != 1 {
		t.Errorf("batches %v", sizes)
	}
	for _, l := range r.Lines {
		if strings.Contains(l, "hasCommit") {
			t.Errorf("a batched receipt went missing: %s", l)
		}
	}
}

// ---- could not run: never a pass ----

func TestHadesTroubleIsCouldNotRunNeverAPass(t *testing.T) {
	good := []node{open(a), open(b)}
	loop := &hades{nodes: good, body: map[string]string{"plan_task_list": text(false, map[string]any{"rows": []any{}, "next_cursor": "same"})}}
	cases := map[string]struct {
		h    *hades
		want []string
	}{
		"not asked":       {&hades{nodes: good, err: map[string]error{"plan_task_list": errors.New("no identity within 2m0s")}}, []string{"CANNOT RUN", "no identity within 2m0s", "never read agrees with nothing"}},
		"403":             {&hades{nodes: good, status: map[string]int{"plan_task_list": 403}, body: map[string]string{"plan_task_list": "forbidden: no grant"}}, []string{"CANNOT RUN", "hades refused plan_task_list (403)", "no grant"}},
		"500":             {&hades{nodes: good, status: map[string]int{"plan_task_list": 503}, body: map[string]string{"plan_task_list": "down"}}, []string{"CANNOT RUN", "HTTP 503"}},
		"not an envelope": {&hades{nodes: good, body: map[string]string{"plan_task_list": "not json"}}, []string{"CANNOT RUN", "not a tool answer"}},
		"empty content":   {&hades{nodes: good, body: map[string]string{"plan_task_list": `{"isError":false,"content":[]}`}}, []string{"CANNOT RUN", "not a tool answer"}},
		"tool error":      {&hades{nodes: good, body: map[string]string{"plan_task_list": text(true, "bad_arguments")}}, []string{"CANNOT RUN", "refused"}},
		"not a page":      {&hades{nodes: good, body: map[string]string{"plan_task_list": `{"isError":false,"content":[{"text":"[1]"}]}`}}, []string{"CANNOT RUN", "not a page"}},
		"cursor repeats":  {loop, []string{"CANNOT RUN", `cursor "same" twice`}},
		"a later page fails": {&hades{nodes: good, pageSize: 1, status: map[string]int{}, err: nil,
			body: map[string]string{}}, nil},
	}
	delete(cases, "a later page fails")
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			has(t, Check(context.Background(), twoCases(), c.h.ask), CannotRun, c.want...)
		})
	}
}

func TestAPageThatFailsMidwayIsCouldNotRun(t *testing.T) {
	h := &hades{nodes: []node{open(a), open(b)}, pageSize: 1}
	n := 0
	ask := func(ctx context.Context, verb, args string) (int, string, error) {
		n++
		if n == 2 {
			return 502, "bad gateway", nil
		}
		return h.ask(ctx, verb, args)
	}
	has(t, Check(context.Background(), twoCases(), ask), CannotRun, "HTTP 502")
}

func TestAnEndlessListIsCouldNotRun(t *testing.T) {
	n := 0
	ask := func(context.Context, string, string) (int, string, error) {
		n++
		return 200, text(false, map[string]any{"rows": []any{}, "next_cursor": fmt.Sprintf("c%d", n)}), nil
	}
	has(t, Check(context.Background(), twoCases(), ask), CannotRun, fmt.Sprintf("still paging after %d pages", MaxPages))
	if n != MaxPages {
		t.Errorf("asked %d pages", n)
	}
}

func TestAnEmptyNextCursorEndsThePaging(t *testing.T) {
	body := text(false, map[string]any{"rows": []any{}, "next_cursor": ""})
	r := Check(context.Background(), twoCases(), func(context.Context, string, string) (int, string, error) { return 200, body, nil })
	has(t, r, Findings, "red case has no ledger node")
}

func TestReceiptReadTroubleIsCouldNotRun(t *testing.T) {
	nodes := []node{open(a), open(b), closed("g.h/i", "[I] x", sha)}
	cases := map[string]*hades{
		"refused":  {nodes: nodes, status: map[string]int{"plan_task_read": 403}, body: map[string]string{"plan_task_read": "no"}},
		"not rows": {nodes: nodes, body: map[string]string{"plan_task_read": `{"isError":false,"content":[{"text":"[]"}]}`}},
		"missing":  {nodes: nodes, body: map[string]string{"plan_task_read": text(false, map[string]any{"rows": []any{}, "missing": []string{"done-g.h/i"}})}},
		"short":    {nodes: nodes, body: map[string]string{"plan_task_read": text(false, map[string]any{"rows": []any{}, "missing": []string{}})}},
		"stranger": {nodes: nodes, body: map[string]string{"plan_task_read": text(false, map[string]any{"rows": []any{map[string]any{"id": "zzz"}}, "missing": []string{}})}},
	}
	want := map[string]string{"refused": "403", "not rows": "not rows", "missing": "no node for 1", "short": "0 rows for 1 ids", "stranger": "was not asked"}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			has(t, Check(context.Background(), twoCases(), h.ask), CannotRun, want[name])
		})
	}
}

func TestATapeThatDoesNotParseIsCouldNotRun(t *testing.T) {
	h := &hades{}
	r := Check(context.Background(), tree(map[string]string{"stamp.json": "{nope"}), h.ask)
	has(t, r, CannotRun, "stamp.json does not parse")
	if len(h.asked) != 0 {
		t.Errorf("hades asked: %v", h.asked)
	}
}

type unreadable struct{ fstest.MapFS }

func (unreadable) ReadFile(string) ([]byte, error) { return nil, errors.New("denied") }

type unglobbable struct{ fstest.MapFS }

func (unglobbable) Glob(string) ([]string, error) { return nil, errors.New("no walk") }

func TestATreeThatWillNotEnumerateIsCouldNotRun(t *testing.T) {
	has(t, Check(context.Background(), unglobbable{twoCases()}, (&hades{}).ask), CannotRun, "the tree would not enumerate: no walk")
}

func TestATapeThatWillNotReadIsCouldNotRun(t *testing.T) {
	r := Check(context.Background(), unreadable{twoCases()}, (&hades{}).ask)
	has(t, r, CannotRun, "would not read")
}

// ---- the shape of the tapes ----

func TestShapeFindings(t *testing.T) {
	good := caseJSON(a, "cas-register/one", "")
	bad := map[string]struct{ case_, want string }{
		"no id":           {strings.Replace(good, `"id":"`+a+`"`, `"id":""`, 1), "no id"},
		"id not tag":      {strings.Replace(good, `"id":"`+a+`"`, `"id":"stamp.other/x"`, 1), "id is not <core>.<tag>"},
		"id prefix":       {strings.Replace(good, `"id":"`+a+`"`, `"id":"Stamp.cas-register/one"`, 1), "id is not <core>.<tag>"},
		"tag":             {strings.Replace(strings.Replace(good, `"tag":"cas-register/one"`, `"tag":"Cas"`, 1), `"id":"`+a+`"`, `"id":"stamp.Cas"`, 1), `tag "Cas" is not <class>/<property>`},
		"core":            {strings.Replace(good, `aiws:stamp`, `stamp`, 1), `core "stamp" is not aiws:<package>`},
		"verb":            {strings.Replace(good, `"verb":"stamp.decide"`, `"verb":""`, 1), "no verb"},
		"args":            {strings.Replace(good, `"args":{},`, ``, 1), "no args"},
		"response":        {strings.Replace(good, `"response":{"ok":1},`, ``, 1), "no response"},
		"expect":          {strings.Replace(good, `"expect":"red"`, `"expect":"green"`, 1), `expect is "green"`},
		"red-as":          {strings.Replace(good, `"red-as":"violated"`, `"red-as":"maybe"`, 1), `red-as is "maybe"`},
		"today":           {strings.Replace(good, `"today":{"ok":2},`, ``, 1), "violated and no today"},
		"violated absent": {strings.Replace(good, `"trusted":true`, `"trusted":true,"absent":["a.b"]`, 1), "violated and carries absent"},
		"provenance":      {strings.Replace(good, `"provenance":"p"`, `"provenance":" "`, 1), "no provenance"},
		"note":            {strings.Replace(good, `"note":"n"`, `"note":""`, 1), "no note"},
		"status":          {strings.Replace(good, `"status":"ok"`, `"status":"bad"`, 1), "status must be ok and trusted true"},
		"trusted":         {strings.Replace(good, `"trusted":true`, `"trusted":false`, 1), "status must be ok and trusted true"},
		"unanalyzable":    {strings.Replace(strings.Replace(good, `"red-as":"violated"`, `"red-as":"unanalyzable"`, 1), `"today":{"ok":2}`, `"absent":[]`, 1), "unanalyzable and no absent"},
		"blank absent":    {strings.Replace(strings.Replace(good, `"red-as":"violated"`, `"red-as":"unanalyzable"`, 1), `"today":{"ok":2}`, `"absent":["x.y"," "]`, 1), "an absent member is blank"},
		"unan today":      {strings.Replace(good, `"red-as":"violated"`, `"red-as":"unanalyzable"`, 1) + "", "unanalyzable and carries today"},
	}
	for name, c := range bad {
		t.Run(name, func(t *testing.T) {
			tp, err := ParseTape([]byte(tapeJSON(ledger, c.case_)))
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Join(ShapeFindings([]string{"red/x.json"}, []Tape{tp}), "\n")
			if !strings.Contains(got, c.want) {
				t.Errorf("want %q in:\n%s", c.want, got)
			}
		})
	}
	// a good unanalyzable case and a good violated case are clean.
	un := strings.Replace(strings.Replace(caseJSON("stamp.x/y", "x/y", ""), `"red-as":"violated"`, `"red-as":"unanalyzable"`, 1), `"today":{"ok":2}`, `"absent":["a.b"]`, 1)
	tp, _ := ParseTape([]byte(tapeJSON(ledger, good, un)))
	if got := ShapeFindings([]string{"f"}, []Tape{tp}); len(got) != 0 {
		t.Errorf("clean tape: %v", got)
	}
	// today may legitimately be JSON null.
	tp, _ = ParseTape([]byte(tapeJSON(ledger, strings.Replace(good, `"today":{"ok":2}`, `"today":null`, 1))))
	if got := ShapeFindings([]string{"f"}, []Tape{tp}); len(got) != 0 {
		t.Errorf("null today: %v", got)
	}
}

func TestLedgerAgreementAcrossTapes(t *testing.T) {
	c1, c2 := caseJSON(a, "cas-register/one", ""), caseJSON(b, "casefold-match/two", "")
	other := "ffff"
	tr := tree(map[string]string{"a.json": tapeJSON(ledger, c1), "b.json": tapeJSON(other, c2)})
	h := &hades{nodes: []node{open(a), open(b)}}
	has(t, Check(context.Background(), tr, h.ask), Findings, "conformance/red/b.json: names ledger ffff, but conformance/red/a.json names "+ledger)

	tr = tree(map[string]string{"a.json": tapeJSON("", c1), "b.json": tapeJSON(ledger, c2)})
	h = &hades{nodes: []node{open(a), open(b)}}
	r := Check(context.Background(), tr, h.ask)
	has(t, r, Findings, "conformance/red/a.json: names no ledger node")
	if len(h.asked) == 0 || h.args[0]["filter"].(map[string]any)["parent"] != ledger {
		t.Errorf("the ledger a later tape names was not read: %v", h.args)
	}

	// no tape names one: nothing to read, and not a pass.
	tr = tree(map[string]string{"a.json": tapeJSON("", c1)})
	h = &hades{}
	r = Check(context.Background(), tr, h.ask)
	has(t, r, Findings, "names no ledger node")
	if len(h.asked) != 0 {
		t.Errorf("asked without a ledger: %v", h.asked)
	}
}

func TestAnEmptyTapeAndARepeatedIdAreFindings(t *testing.T) {
	c := caseJSON(a, "cas-register/one", "")
	tr := tree(map[string]string{"a.json": tapeJSON(ledger), "b.json": tapeJSON(ledger, c), "c.json": tapeJSON(ledger, c)})
	h := &hades{nodes: []node{open(a)}}
	has(t, Check(context.Background(), tr, h.ask), Findings,
		"conformance/red/a.json: holds no case", "id repeats the one in conformance/red/b.json")
}

// ---- the adapter and the report ----

func TestHadesAskReadsHadescallsOutput(t *testing.T) {
	run := func(code int, out, errs string) Ask {
		return HadesAsk(func(_ context.Context, args []string, o, e *strings.Builder) int {
			if args[0] != "plan_task_list" || args[1] != `{"x":1}` {
				t.Errorf("args %v", args)
			}
			o.WriteString(out)
			e.WriteString(errs)
			return code
		})
	}
	s, body, err := run(0, "HTTP 200\n{\"a\":1}\nmore", "")(context.Background(), "plan_task_list", `{"x":1}`)
	if err != nil || s != 200 || body != "{\"a\":1}\nmore" {
		t.Errorf("%d %q %v", s, body, err)
	}
	if _, _, err := run(2, "", " cannot connect \n")(context.Background(), "plan_task_list", `{"x":1}`); err == nil || err.Error() != "cannot connect" {
		t.Errorf("nonzero exit: %v", err)
	}
	if _, _, err := run(0, "200\nx", "")(context.Background(), "plan_task_list", `{"x":1}`); err == nil || !strings.Contains(err.Error(), "no status line") {
		t.Errorf("no status line: %v", err)
	}
	if _, _, err := run(0, "HTTP abc\nx", "")(context.Background(), "plan_task_list", `{"x":1}`); err == nil || !strings.Contains(err.Error(), "not a number") {
		t.Errorf("bad status: %v", err)
	}
	if s, _, err := run(0, " HTTP 403 \nx", "")(context.Background(), "plan_task_list", `{"x":1}`); err != nil || s != 403 {
		t.Errorf("padded head: %d %v", s, err)
	}
}

func TestRender(t *testing.T) {
	if got := Render(Result{Reason: "r", Lines: []string{"a", "b"}}); got != "r\na\nb\n" {
		t.Errorf("%q", got)
	}
	if got := Render(Result{Reason: "r"}); got != "r\n" {
		t.Errorf("%q", got)
	}
}

func TestNodeClosed(t *testing.T) {
	for st, want := range map[string]bool{"done": true, "cut": true, "killed": true, "mistrial": true, "backlog": false, "": false, "in-progress": false} {
		if (Node{Status: st}).Closed() != want {
			t.Errorf("%q closed = %v", st, !want)
		}
	}
}

// ---- the order of the report ----

// A report is read by a person and diffed between runs: the same ledger
// disagreement has to come out in the same order however hades lists it.
func TestFindingsComeOutInKeyOrder(t *testing.T) {
	h := &hades{nodes: []node{open(a), open(b), open("z.gone/three"), open("m.gone/two"), open("c.gone/one")}}
	r := Check(context.Background(), twoCases(), h.ask)
	has(t, r, Findings)
	var got []string
	for _, l := range r.Lines {
		got = append(got, strings.SplitN(l, ":", 2)[0])
	}
	if strings.Join(got, ",") != "c.gone/one,m.gone/two,z.gone/three" {
		t.Errorf("order %v", got)
	}
}

func TestCasesWithNoNodeComeOutInIdOrder(t *testing.T) {
	fs := tree(map[string]string{"stamp.json": tapeJSON(ledger,
		caseJSON("stamp.z-class/one", "z-class/one", ""), caseJSON("stamp.m-class/one", "m-class/one", ""),
		caseJSON("stamp.c-class/one", "c-class/one", ""))})
	r := Check(context.Background(), fs, (&hades{}).ask)
	want := []string{
		"stamp.c-class/one: red case has no ledger node",
		"stamp.m-class/one: red case has no ledger node",
		"stamp.z-class/one: red case has no ledger node",
	}
	if strings.Join(r.Lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("lines %v", r.Lines)
	}
}

// reversed is a tree whose Glob answers newest-first, as a GlobFS may.
type reversed struct{ fstest.MapFS }

func (r reversed) Glob(p string) ([]string, error) {
	m, err := fs.Glob(r.MapFS, p)
	slices.Reverse(m)
	return m, err
}

func TestTapesAreReadInNameOrder(t *testing.T) {
	other := strings.Repeat("b", 64)
	tr := reversed{tree(map[string]string{
		"alarm.json": tapeJSON(ledger, caseJSON(a, "cas-register/one", "")),
		"tail.json":  tapeJSON(other, caseJSON(b, "casefold-match/two", "")),
	})}
	h := &hades{nodes: []node{open(a), open(b)}}
	r := Check(context.Background(), tr, h.ask)
	has(t, r, Findings, "conformance/red/tail.json: names ledger "+other+", but conformance/red/alarm.json names "+ledger)
	if f := h.args[0]["filter"].(map[string]any); f["parent"] != ledger {
		t.Errorf("read the ledger %v, want the first tape's", f["parent"])
	}
}

// A finding names its node by the id's first twelve hex, as the handoff does.
func TestAFindingNamesItsNodeShort(t *testing.T) {
	n := open("x.y/z")
	n.id = strings.Repeat("0123456789ab", 5) + "cdef"
	r := Check(context.Background(), twoCases(), (&hades{nodes: []node{open(a), open(b), n}}).ask)
	has(t, r, Findings, "(node 0123456789ab is backlog, no red case)")
	if strings.Contains(strings.Join(r.Lines, ""), "0123456789ab0") {
		t.Errorf("the id was not shortened: %v", r.Lines)
	}
}

// ---- the binary ----

func TestMainRefusesTwoTrees(t *testing.T) {
	var out, errs strings.Builder
	run := func(context.Context, []string, func(string) string, io.Writer, io.Writer) int {
		t.Error("hades was asked")
		return 0
	}
	if got := Main(context.Background(), []string{"a", "b"}, run, os.Getenv, &out, &errs); got != CannotRun {
		t.Errorf("exit %d", got)
	}
	if !strings.Contains(errs.String(), "usage: redledger [tree]") || out.Len() != 0 {
		t.Errorf("out %q errs %q", out.String(), errs.String())
	}
}

func TestMainChecksTheNamedTreeThroughTheRunner(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/conformance/red", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/conformance/red/stamp.json", []byte(tapeJSON(ledger, caseJSON(a, "cas-register/one", ""))), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &hades{nodes: []node{open(a)}}
	env := func(k string) string { return "env:" + k }
	run := func(ctx context.Context, args []string, e func(string) string, o, _ io.Writer) int {
		if e("X") != "env:X" {
			t.Errorf("the runner was not handed the environment")
		}
		s, body, _ := h.ask(ctx, args[0], args[1])
		fmt.Fprintf(o, "HTTP %d\n%s", s, body)
		return 0
	}
	var out, errs strings.Builder
	if got := Main(context.Background(), []string{dir}, run, env, &out, &errs); got != Pass {
		t.Errorf("exit %d: %s %s", got, out.String(), errs.String())
	}
	if out.String() != ID+": 1 open ledger node(s) = 1 red case(s)\n" {
		t.Errorf("out %q", out.String())
	}
	// the working directory is the default tree: this package's has no red tapes.
	out.Reset()
	if got := Main(context.Background(), nil, run, env, &out, &errs); got != Pass || !strings.Contains(out.String(), "ABSENT") {
		t.Errorf("default tree: exit %d %q", got, out.String())
	}
}
