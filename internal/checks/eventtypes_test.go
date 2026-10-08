package checks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func types(sites []EventSite) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range sites {
		if !seen[s.Type] {
			seen[s.Type] = true
			out = append(out, s.Type)
		}
	}
	sort.Strings(out)
	return out
}

func same(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

func TestEventTypeUsesReadsEveryConsumerShape(t *testing.T) {
	files := map[string]string{
		"a.sql.json": "SELECT 1 FROM fleet.session_events WHERE event_type = 'sql_eq' AND ts > 1 -- and event_type IN ('in_a', 'in_b')\n",
		"b.py": `CALIBRATION_TYPES = frozenset({"cal_a", "cal_b"})
if ev.get("event_type") in CALIBRATION_TYPES:
    pass
if ev["event_type"] == "py_cmp":
    pass
etype = event.get("event_type")
if etype == "alias_cmp":
    pass
`,
		"c.go": `package c
func f(e Event) {
	if e.EventType != "go_cmp" {
	}
	switch e.EventType {
	case "sw_a", "sw_b":
	case "sw_c":
	}
	tartarus_session_events_read(map[string]any{"session_uuid": s, "event_type": "read_verb"})
}
`,
	}
	consumed, emitted := EventTypeUses(files)
	want := []string{"alias_cmp", "cal_a", "cal_b", "go_cmp", "in_a", "in_b", "py_cmp", "read_verb", "sql_eq", "sw_a", "sw_b", "sw_c"}
	if got := types(consumed); !same(got, want) {
		t.Errorf("consumed\n got %v\nwant %v", got, want)
	}
	if len(emitted) != 0 {
		t.Errorf("a filter is not an emitter: %v", emitted)
	}
}

func TestEventTypeUsesReadsEveryEmitterShape(t *testing.T) {
	files := map[string]string{
		"hook.py": `emit({"event_type": "py_dict", "payload": {}})
fleet_emit(event_type="py_kwarg", payload={})
EVT = "py_const"
emit({"event_type": EVT})
`,
		"e.go":        "package e\nconst eventCommit = \"go_const\"\nvar x = Event{EventType: eventCommit}\nvar y = Event{EventType: \"go_lit\"}\n",
		"skill.md":    "Call `tartarus_emit(event_type=\"doc_emit\", scope=\"x\")` when Rob pushes back. A bare event_type=\"doc_prose\" is prose.\n",
		"e_test.go":   "package e\nvar z = Event{EventType: \"test_only\"}\n",
		"vendor/v.go": "package v\nvar z = Event{EventType: \"vendored\"}\n",
	}
	_, emitted := EventTypeUses(files)
	want := []string{"doc_emit", "go_const", "go_lit", "py_const", "py_dict", "py_kwarg"}
	if got := types(emitted); !same(got, want) {
		t.Errorf("emitted\n got %v\nwant %v", got, want)
	}
}

func TestFilterSpansAreNotEmitters(t *testing.T) {
	files := map[string]string{"q.go": "package q\nvar s = `SELECT * FROM t WHERE event_type = 'q_filter'`\nvar r = tartarus_session_events_read(session, {\"event_type\": \"q_read\"})\n"}
	_, emitted := EventTypeUses(files)
	if len(emitted) != 0 {
		t.Errorf("filters counted as emitters: %v", emitted)
	}
}

// fleetDoor serves /custody/repos, /tree and /archive from repo -> path -> body.
func fleetDoor(t *testing.T, fleet map[string]map[string]string, count *int) Door {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		repo := r.URL.Query().Get("repo")
		switch r.URL.Path {
		case "/custody/repos":
			var repos []string
			for k := range fleet {
				repos = append(repos, k)
			}
			sort.Strings(repos)
			_ = json.NewEncoder(w).Encode(map[string]any{"owner": "rob", "repos": repos})
		case "/tree":
			files, ok := fleet[repo]
			if !ok {
				http.NotFound(w, r)
				return
			}
			var entries []map[string]any
			for p, b := range files {
				entries = append(entries, map[string]any{"path": p, "mode": "0100644", "size": len(b)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"entries": entries})
		case "/archive":
			mu.Lock()
			if count != nil {
				*count++
			}
			mu.Unlock()
			body, ok := fleet[repo][r.URL.Query().Get("path")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return Door{Base: srv.URL, Client: srv.Client()}
}

func TestFleetEmittersFindsEmittersAndSkipsSelfAndArchives(t *testing.T) {
	fleet := map[string]map[string]string{
		"rob/producer":        {"emit.py": `emit({"event_type": "wanted"})`, "big.go": "package x\n"},
		"rob/self":            {"emit.py": `emit({"event_type": "only_in_self"})`},
		"forge-archives/dead": {"emit.py": `emit({"event_type": "only_in_archive"})`},
		"rob/vendored":        {"vendor/x.go": `var a = E{EventType: "vendored_only"}`},
	}
	door := fleetDoor(t, fleet, nil)
	found, err := FleetEmitters(context.Background(), door, "self", []string{"wanted", "only_in_self", "only_in_archive", "vendored_only"})
	if err == nil && len(found) == 4 {
		t.Fatalf("self, archives and vendored code must not answer: %v", found)
	}
	if found["wanted"] != "rob/producer:emit.py" {
		t.Errorf("wanted -> %q", found["wanted"])
	}
	for _, k := range []string{"only_in_self", "only_in_archive", "vendored_only"} {
		if found[k] != "" {
			t.Errorf("%s was answered by %q", k, found[k])
		}
	}
}

func TestFleetEmittersStopsOnceEverythingIsFound(t *testing.T) {
	fleet := map[string]map[string]string{"rob/a": {"a.py": `emit({"event_type": "t"})`}}
	for i := 0; i < 40; i++ {
		fleet["rob/z"+string(rune('a'+i%26))+string(rune('a'+i/26))] = map[string]string{"x.py": "x = 1"}
	}
	var asked int
	door := fleetDoor(t, fleet, &asked)
	found, err := FleetEmitters(context.Background(), door, "", []string{"t"})
	if err != nil || found["t"] == "" {
		t.Fatalf("found %v err %v", found, err)
	}
}

func TestFleetEmittersErrorsWhenTheFleetWillNotAnswer(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if _, err := FleetEmitters(context.Background(), Door{Base: srv.URL, Client: srv.Client()}, "", []string{"t"}); err == nil ||
		!strings.Contains(err.Error(), "the fleet listing") {
		t.Errorf("want the listing named, got %v", err)
	}
	fleet := map[string]map[string]string{"rob/a": {"a.py": "x = 1"}}
	door := fleetDoor(t, fleet, nil)
	srvBad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/archive" {
			http.Error(w, "boom", http.StatusServiceUnavailable)
			return
		}
		resp, err := http.Get(door.Base + r.URL.RequestURI())
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer srvBad.Close()
	if _, err := FleetEmitters(context.Background(), Door{Base: srvBad.URL, Client: srvBad.Client()}, "", []string{"t"}); err == nil ||
		!strings.Contains(err.Error(), "HTTP 503") {
		t.Errorf("a file the door would not serve must be an error, got %v", err)
	}
}

func TestConsumedEventsEmittedLadder(t *testing.T) {
	consumer := map[string]string{"c.py": `if ev["event_type"] == "gone_type":
    pass
if ev["event_type"] == "local_type":
    pass
emit({"event_type": "local_type"})
`}
	for _, tc := range []struct {
		name    string
		files   map[string]string
		found   map[string]string
		err     error
		state   int
		needles []string
		asked   bool
	}{
		{"consumes nothing", map[string]string{"c.py": "x = 1"}, nil, nil, 0, []string{"consumes no literal event_type"}, false},
		{"all local", map[string]string{"c.py": "if ev[\"event_type\"] == \"local_type\":\n    pass\nemit({\"event_type\": \"local_type\"})\n"}, nil, nil, 0, []string{"all emitted by this tree"}, false},
		{"unemitted is a finding", consumer, map[string]string{}, nil, 1,
			[]string{"'gone_type' is consumed (c.py) and no source in the fleet emits it", "retired with no replacement"}, true},
		{"emitted elsewhere", consumer, map[string]string{"gone_type": "rob/p:x.py"}, nil, 0, []string{"every one has an emitter (1 outside this tree)"}, true},
		{"scan failure cannot run", consumer, nil, errors.New("tree of rob/x: HTTP 503"), 2, []string{"CANNOT RUN", "partial scan is a guess"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var wanted []string
			state, report := ConsumedEventsEmitted(context.Background(), tc.files, func(_ context.Context, need []string) (map[string]string, error) {
				wanted = need
				return tc.found, tc.err
			})
			if state != tc.state {
				t.Fatalf("state %d, want %d\n%s", state, tc.state, report)
			}
			for _, n := range tc.needles {
				if !strings.Contains(report, n) {
					t.Errorf("report lacks %q:\n%s", n, report)
				}
			}
			if tc.asked != (wanted != nil) {
				t.Errorf("fleet asked=%v, want %v (%v)", wanted != nil, tc.asked, wanted)
			}
			if tc.asked && !same(wanted, []string{"gone_type"}) {
				t.Errorf("only the type this tree does not emit goes to the fleet, got %v", wanted)
			}
		})
	}
}

func TestFirstNDeduplicatesAndCounts(t *testing.T) {
	got := firstN([]string{"a", "a", "b", "c", "d", "e"}, 3)
	if !same(got, []string{"a", "b", "c", "+2 more"}) {
		t.Errorf("got %v", got)
	}
}

func TestCommentsNeitherConsumeNorEmit(t *testing.T) {
	files := map[string]string{
		"a.go": "package a\n// switch e.EventType {\n// if e.EventType == \"in_comment\"\n/* EventType: \"block\" */\nfunc f() {\n\tswitch seg {\n\tcase \"vendor\", \"docs\":\n\t}\n}\n",
		"b.py": "# if ev['event_type'] == 'py_comment'\n",
	}
	consumed, emitted := EventTypeUses(files)
	if len(consumed) != 0 || len(emitted) != 0 {
		t.Errorf("comments were read: consumed %v emitted %v", consumed, emitted)
	}
}

func TestEventSourceExtTable(t *testing.T) {
	for p, want := range map[string]bool{
		"a.go": true, "a.py": true, "a.ts": true, "a.tsx": true, "a.js": true, "a.mjs": true, "a.sh": true,
		"a.rs": true, "a.sql": true, "a.json": true, "skills/x/x.md": true,
		"a.txt": false, "a.yaml": false, "Makefile": false,
		"vendor/a.go": false, "node_modules/a.js": false, "testdata/a.go": false, "test/a.go": false, "tests/a.py": false,
		"fixtures/a.json": false, "fixture/a.json": false, "dist/a.js": false, "target/a.rs": false,
		"__pycache__/a.py": false, "specs/a.md": false, "docs/a.md": false, "Docs/a.md": false,
		"a_test.go": false, "test_a.py": false, "a.test.ts": false, "a.spec.ts": false, "a.d.ts": false,
		"internal/latest.go": true, "internal/contest/a.go": true, "src/testing.py": true,
	} {
		if got := EventSourceExt(p); got != want {
			t.Errorf("EventSourceExt(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestStripLineCommentsByLanguage(t *testing.T) {
	for _, tc := range []struct{ file, body, want string }{
		{"a.py", "# gone\nkeep\n  # gone too\n", "\nkeep\n\n"},
		{"a.sh", "# gone\nkeep", "\nkeep"},
		{"a.sql", "-- gone\nkeep # not a comment here\n", "\nkeep # not a comment here\n"},
		{"a.json", "# kept\n// kept\n", "# kept\n// kept\n"},
		{"a.go", "// gone\n/* gone\n * gone\nkeep # x\n", "\n\n\nkeep # x\n"},
		{"a.ts", "// gone\nkeep\n", "\nkeep\n"},
		{"a.rs", "// gone\nkeep // trailing stays\n", "\nkeep // trailing stays\n"},
	} {
		if got := stripLineComments(tc.file, tc.body); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.file, got, tc.want)
		}
	}
}

func TestEventTypeUsesIsDedupedPerFileAndSortedByPath(t *testing.T) {
	files := map[string]string{
		"z.py": "emit({'event_type': 'one'})\nemit({'event_type': 'one'})\nif e['event_type'] == 'two': pass\nif e['event_type'] == 'two': pass\n",
		"a.py": "emit({'event_type': 'one'})\nif e['event_type'] == 'two': pass\n",
	}
	consumed, emitted := EventTypeUses(files)
	wantE := []EventSite{{"one", "a.py"}, {"one", "z.py"}}
	wantC := []EventSite{{"two", "a.py"}, {"two", "z.py"}}
	if len(emitted) != 2 || emitted[0] != wantE[0] || emitted[1] != wantE[1] {
		t.Errorf("emitted %v, want %v", emitted, wantE)
	}
	if len(consumed) != 2 || consumed[0] != wantC[0] || consumed[1] != wantC[1] {
		t.Errorf("consumed %v, want %v", consumed, wantC)
	}
}

func TestEventTypeConstantsResolveAcrossFilesAndSkipIgnoredOnes(t *testing.T) {
	files := map[string]string{
		"consts.go":   "package c\nconst (\n\tEvCommit = \"via_const\"\n)\nconst evOther string = \"typed_const\"\nvar evVar = \"var_const\"\n",
		"use.go":      "package u\nvar a = E{EventType: pkg.EvCommit}\nvar b = E{EventType: evOther}\nvar c = E{EventType: evVar}\nvar d = E{EventType: unknownName}\n",
		"vendor/c.go": "package v\nconst Ghost = \"ghost_const\"\n",
		"use2.go":     "package u\nvar a = E{EventType: Ghost}\n",
		"multi.py":    "A = \"first\"\nA = \"second\"\nemit(event_type=A)\n",
	}
	_, emitted := EventTypeUses(files)
	got := types(emitted)
	want := []string{"first", "second", "typed_const", "var_const", "via_const"}
	if !same(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestEverySQLFilterShapeIsAFilterNotAnEmitter(t *testing.T) {
	for _, line := range []string{
		"SELECT * FROM t WHERE event_type: 'x'",
		"WHERE event_type = 'x'",
		"  AND event_type = 'x'",
		"SELECT event_type: 'x'",
		"  FROM event_type: 'x'",
	} {
		files := map[string]string{"q.py": "q = \"\"\"\n" + line + "\n\"\"\"\n"}
		_, emitted := EventTypeUses(files)
		for _, e := range emitted {
			t.Errorf("%q emitted %v", line, e)
		}
	}
	_, emitted := EventTypeUses(map[string]string{"q.py": "where_event = {'event_type': 'plain'}\n"})
	if len(emitted) != 1 {
		t.Errorf("a plain dict is an emitter: %v", emitted)
	}
}

func TestSetConstantsForms(t *testing.T) {
	for _, tc := range []struct {
		body, name string
		want       []string
	}{
		{"SET = {'a', 'b'}", "SET", []string{"a", "b"}},
		{"SET = ('a', 'b')", "SET", []string{"a", "b"}},
		{"SET = ['a']", "SET", []string{"a"}},
		{"SET = frozenset({'a', \"b\"})", "SET", []string{"a", "b"}},
		{"SET: Set[str] = {'a'}", "SET", []string{"a"}},
		{"var set = []string{\"x\", \"y\"}", "set", []string{"x", "y"}},
		{"OTHER = {'a'}", "SET", nil},
		{"SETS = {'a'}", "SET", nil},
	} {
		if got := setConstants(tc.body, tc.name); !same(got, tc.want) {
			t.Errorf("%q: got %v want %v", tc.body, got, tc.want)
		}
	}
}

func TestMarkdownEmitsOnlyThroughACall(t *testing.T) {
	files := map[string]string{
		"skill.md": "A bare event_type=\"prose_only\" is not an emitter. Call fleet_emit(\n  scope=\"x\", event_type: \"multi_line\") and tartarus_emit(event_type=\"another\").\n// event_type == \"consumer_in_md\"\n",
	}
	consumed, emitted := EventTypeUses(files)
	if got := types(emitted); !same(got, []string{"another", "multi_line"}) {
		t.Errorf("emitted %v", got)
	}
	if len(consumed) != 0 {
		t.Errorf("markdown consumes nothing: %v", consumed)
	}
}

func TestEventTypeCaseAndSpellings(t *testing.T) {
	files := map[string]string{
		"a.ts":  "if (e.eventType === 'ts_cmp') {}\nif (e.event_type !== \"ts_neq\") {}\nconst x = { event_type: 'ts_emit' }\nconst y = { EventType := 'go_def' }\n",
		"b.sql": "SELECT 1 WHERE event_type NOT IN ('n1', 'n2') AND event_type <> 'n3' AND event_type != 'n4'\n",
	}
	consumed, emitted := EventTypeUses(files)
	if got := types(consumed); !same(got, []string{"n1", "n2", "n3", "n4", "ts_cmp", "ts_neq"}) {
		t.Errorf("consumed %v", got)
	}
	if got := types(emitted); !same(got, []string{"go_def", "ts_emit"}) {
		t.Errorf("emitted %v", got)
	}
}

func TestDoorGetJSON(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte(`{"owner":"rob"}`))
		case "/bad":
			_, _ = w.Write([]byte(`not json`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	var out fleetRepos
	// Client nil: the door builds its own bounded client.
	d := Door{Base: srv.URL + "/"}
	if err := d.getJSON(context.Background(), "/ok", nil, &out); err != nil || out.Owner != "rob" || gotQuery != "" {
		t.Errorf("ok: %v %+v query=%q", err, out, gotQuery)
	}
	if err := d.getJSON(context.Background(), "/ok", map[string][]string{"repo": {"a/b"}}, &out); err != nil || gotQuery != "repo=a%2Fb" {
		t.Errorf("query: %v %q", err, gotQuery)
	}
	if err := d.getJSON(context.Background(), "/bad", nil, &out); err == nil {
		t.Error("a body that is not JSON must be an error")
	}
	if err := d.getJSON(context.Background(), "/missing", nil, &out); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("404: %v", err)
	}
	if err := (Door{Base: "http://bad host"}).getJSON(context.Background(), "/", nil, &out); err == nil {
		t.Error("an unparseable address must be an error")
	}
	srv.Close()
	if err := d.getJSON(context.Background(), "/ok", nil, &out); err == nil {
		t.Error("a dead door must be an error")
	}
}

func TestFleetEmittersSkipRulesAndEarlyStop(t *testing.T) {
	big := strings.Repeat("x", EventScanMaxBytes+1)
	fleet := map[string]map[string]string{
		"rob/a-self":  {"e.py": `emit({"event_type": "from_self_like"})`},
		"rob/self":    {"e.py": `emit({"event_type": "from_self"})`},
		"other/self":  {"e.py": `emit({"event_type": "from_other_self"})`},
		"rob/skipped": {"empty.py": "", "big.py": big + `emit({"event_type": "from_big"})`, "doc.txt": `emit({"event_type": "from_txt"})`},
		"rob/real":    {"e.py": `emit({"event_type": "from_real"})`},
	}
	var asked int
	door := fleetDoor(t, fleet, &asked)
	found, err := FleetEmitters(context.Background(), door, "self", []string{"from_self_like", "from_self", "from_other_self", "from_big", "from_txt", "from_real"})
	if err != nil {
		t.Fatal(err)
	}
	if found["from_self_like"] != "rob/a-self:e.py" || found["from_real"] != "rob/real:e.py" {
		t.Errorf("a repo whose name merely ends like self is scanned: %v", found)
	}
	for _, k := range []string{"from_self", "from_other_self", "from_big", "from_txt"} {
		if found[k] != "" {
			t.Errorf("%s must not be found: %v", k, found)
		}
	}
	if asked != 2 {
		t.Errorf("only the two real files are fetched, asked %d", asked)
	}

	// Symlinks and submodules are never fetched.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/custody/repos":
			_, _ = w.Write([]byte(`{"repos":["rob/m"]}`))
		case "/tree":
			_, _ = w.Write([]byte(`{"entries":[{"path":"l.py","mode":"0120000","size":10},{"path":"s.py","mode":"0160000","size":10}]}`))
		default:
			t.Errorf("fetched %s", r.URL)
		}
	}))
	defer srv.Close()
	if found, err := FleetEmitters(context.Background(), Door{Base: srv.URL, Client: srv.Client()}, "", []string{"x"}); err != nil || len(found) != 0 {
		t.Errorf("symlink/submodule: %v %v", found, err)
	}
}

func TestFleetEmittersStopsDispatchingOnceSatisfied(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 400; i++ {
		files["f"+string(rune('a'+i/26/26%26))+string(rune('a'+i/26%26))+string(rune('a'+i%26))+".py"] = `emit({"event_type": "t"})`
	}
	var asked int
	door := fleetDoor(t, map[string]map[string]string{"rob/a": files}, &asked)
	found, err := FleetEmitters(context.Background(), door, "", []string{"t"})
	if err != nil || found["t"] == "" {
		t.Fatalf("%v %v", found, err)
	}
	if asked >= 400 {
		t.Errorf("all %d files were fetched; the scan must stop once every type is found", asked)
	}
}

func TestFleetEmittersReportsATreeAndAFileThatWillNotAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/custody/repos":
			_, _ = w.Write([]byte(`{"repos":["rob/a","rob/b"]}`))
		case "/tree":
			if r.URL.Query().Get("repo") == "rob/b" {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"entries":[{"path":"a.py","mode":"0100644","size":5}]}`))
		case "/archive":
			_, _ = w.Write([]byte(`x = 1`))
		}
	}))
	defer srv.Close()
	_, err := FleetEmitters(context.Background(), Door{Base: srv.URL, Client: srv.Client()}, "", []string{"t"})
	if err == nil || !strings.Contains(err.Error(), "the tree of rob/b") {
		t.Errorf("want the tree named, got %v", err)
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/custody/repos":
			_, _ = w.Write([]byte(`{"repos":["rob/a"]}`))
		case "/tree":
			_, _ = w.Write([]byte(`{"entries":[{"path":"a.py","mode":"0100644","size":5}]}`))
		default:
			http.Error(w, "gone", http.StatusServiceUnavailable)
		}
	}))
	srv2.Close()
	if _, err := FleetEmitters(context.Background(), Door{Base: srv2.URL, Client: srv2.Client()}, "", []string{"t"}); err == nil {
		t.Error("a dead door is an error")
	}
}

func TestConsumedEventsEmittedSortsTypesAndListsThreeSites(t *testing.T) {
	files := map[string]string{
		"a.py": "if e['event_type'] == 'zz': pass\nif e['event_type'] == 'aa': pass\n",
		"b.py": "if e['event_type'] == 'zz': pass\n",
		"c.py": "if e['event_type'] == 'zz': pass\n",
		"d.py": "if e['event_type'] == 'zz': pass\n",
	}
	var wanted []string
	state, report := ConsumedEventsEmitted(context.Background(), files, func(_ context.Context, need []string) (map[string]string, error) {
		wanted = need
		return map[string]string{}, nil
	})
	if state != 1 || !same(wanted, []string{"aa", "zz"}) {
		t.Fatalf("state %d wanted %v", state, wanted)
	}
	if strings.Index(report, "'aa'") > strings.Index(report, "'zz'") {
		t.Errorf("types are reported in order:\n%s", report)
	}
	if !strings.Contains(report, "(a.py, b.py, c.py, +1 more)") {
		t.Errorf("three sites then a count:\n%s", report)
	}
	state, report = ConsumedEventsEmitted(context.Background(), files, func(_ context.Context, need []string) (map[string]string, error) {
		return map[string]string{"zz": "x"}, nil
	})
	if state != 1 || strings.Contains(report, "'zz'") || !strings.Contains(report, "'aa'") {
		t.Errorf("only the unemitted type is a finding: %d\n%s", state, report)
	}
}

func TestEveryPatternReadsEveryMatchNotJustTheFirst(t *testing.T) {
	files := map[string]string{
		"all.py": `read_a = tartarus_session_events_read(session, {"event_type": "rc1"})
read_b = get_session_events(event_type="rc2")
q = "WHERE event_type = 'eq1' OR event_type = 'eq2'"
r = "event_type IN ('in1') AND event_type IN ('in2')"
if e['event_type'] == 'cmp1' or e['event_type'] != 'cmp2': pass
W1 = {'m1'}
W2 = {'m2'}
if e['event_type'] in W1 or e['event_type'] in W2: pass
a = ev.get("event_type")
b = ev.get("event_type")
if a == 'al1' or a == 'al2' or b == 'bl1': pass
t1 = ev.get("event_type")
t2 = ev.get("event_type")
if t1 in W1 or t2 in W2: pass
`,
		"sw.go": "package s\nfunc f(e E) {\n\tswitch e.EventType {\n\tcase \"s1\":\n\t}\n\tswitch x.EventType {\n\tcase \"s2\":\n\t}\n}\n",
	}
	consumed, _ := EventTypeUses(files)
	want := []string{"al1", "al2", "bl1", "cmp1", "cmp2", "eq1", "eq2", "in1", "in2", "m1", "m2", "rc1", "rc2", "s1", "s2"}
	if got := types(consumed); !same(got, want) {
		t.Errorf("consumed\n got %v\nwant %v", got, want)
	}
	_, emitted := EventTypeUses(map[string]string{
		"e.py": "emit({'event_type': 'e1'})\nemit({'event_type': 'e2'})\nX = 'x1'\nY = 'x2'\nemit({'event_type': X})\nemit({'event_type': Y})\nemit({'event_type': p.X})\n",
	})
	if got := types(emitted); !same(got, []string{"e1", "e2", "x1", "x2"}) {
		t.Errorf("emitted %v", got)
	}
}

func TestAnEmitterBesideAReadIsStillAnEmitter(t *testing.T) {
	// The span of the read ends where its literal ends; an emitter flush against
	// either edge is outside it.
	body := `pre = {"event_type": "pre_emit"}; tartarus_session_events_read(event_type="filtered"); post = {"event_type": "post_emit"}` + "\n"
	_, emitted := EventTypeUses(map[string]string{"x.py": body})
	if got := types(emitted); !same(got, []string{"post_emit", "pre_emit"}) {
		t.Errorf("emitted %v", got)
	}
	flush := `tartarus_session_events_read(event_type="filt")event_type: "flush_after"` + "\n"
	if _, em := EventTypeUses(map[string]string{"y.py": flush}); !same(types(em), []string{"flush_after"}) {
		t.Errorf("a literal starting where the read ends is an emitter: %v", em)
	}
	cs := "K = 'const_in_read'\ntartarus_session_events_read(event_type=K)\nW = 'plain_const'\nx = {'event_type': W}\n"
	consumed, em := EventTypeUses(map[string]string{"z.py": cs})
	if got := types(consumed); !same(got, []string{"const_in_read"}) {
		t.Errorf("a constant passed to a read verb is consumed: %v", got)
	}
	if got := types(em); !same(got, []string{"plain_const"}) {
		t.Errorf("a constant inside a read is a filter; one on a plain line is an emitter: %v", got)
	}
}

func TestAliasedMembershipReadsTheConstantSet(t *testing.T) {
	body := "WATCHED = ('w1', 'w2')\netype = ev.get('event_type')\nif etype in WATCHED:\n    pass\nif etype not in OTHER:\n    pass\n"
	consumed, _ := EventTypeUses(map[string]string{"h.py": body})
	if got := types(consumed); !same(got, []string{"w1", "w2"}) {
		t.Errorf("consumed %v", got)
	}
}

func TestFleetEmittersTransportFailureAndUnwantedTypes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/custody/repos":
			_, _ = w.Write([]byte(`{"repos":["rob/a"]}`))
		case "/tree":
			_, _ = w.Write([]byte(`{"entries":[{"path":"a.py","mode":"0100644","size":50}]}`))
		case "/archive":
			if r.URL.Query().Get("path") == "a.py" {
				panic(http.ErrAbortHandler)
			}
		}
	}))
	defer srv.Close()
	found, err := FleetEmitters(context.Background(), Door{Base: srv.URL, Client: srv.Client()}, "", []string{"t"})
	if err == nil || !strings.Contains(err.Error(), "rob/a a.py") || strings.Contains(err.Error(), "HTTP") || len(found) != 0 {
		t.Errorf("a file that cut the connection is an error naming it: %v %v", found, err)
	}

	door := fleetDoor(t, map[string]map[string]string{"rob/a": {"a.py": `emit({"event_type": "wanted"}); emit({"event_type": "unwanted"})`}}, nil)
	got, err := FleetEmitters(context.Background(), door, "", []string{"wanted"})
	if err != nil || len(got) != 1 || got["wanted"] == "" {
		t.Errorf("only wanted types are recorded: %v %v", got, err)
	}
	if _, err := FleetEmitters(context.Background(), fleetDoor(t, map[string]map[string]string{"rob/a": {"a.py": "x"}}, nil), "", []string{"t"}); err != nil {
		t.Errorf("scanning without a find is not an error: %v", err)
	}
}

func TestMembershipAndAliasAndSwitchEachReadEveryMatch(t *testing.T) {
	member := "W1 = {'m1'}\nW2 = {'m2'}\nif e['event_type'] in W1: pass\nif d['event_type'] in W2: pass\n"
	if c, _ := EventTypeUses(map[string]string{"m.py": member}); !same(types(c), []string{"m1", "m2"}) {
		t.Errorf("membership: %v", types(c))
	}
	alias := "W1 = {'a1'}\nW2 = {'a2'}\nx = ev.get('event_type')\ny = ev.get('event_type')\nif x in W1: pass\nif y in W2: pass\n"
	if c, _ := EventTypeUses(map[string]string{"a.py": alias}); !same(types(c), []string{"a1", "a2"}) {
		t.Errorf("alias membership: %v", types(c))
	}
	pad := strings.Repeat("\t_ = padding_to_push_the_next_switch_out_of_the_first_window()\n", 60)
	sw := "package s\nfunc f(e E) {\n\tswitch e.EventType {\n\tcase \"s1\":\n\t}\n" + pad + "\tswitch x.EventType {\n\tcase \"s2\":\n\t}\n}\n"
	if c, _ := EventTypeUses(map[string]string{"s.go": sw}); !same(types(c), []string{"s1", "s2"}) {
		t.Errorf("switch: %v", types(c))
	}
}

func TestAConstantOnAFilterLineIsNotAnEmitter(t *testing.T) {
	body := "K = 'where_k'\nq = \"SELECT 1 FROM t WHERE event_type = K\"\nAND_K = 'and_k'\nq2 = \"x AND event_type = AND_K\"\n"
	_, emitted := EventTypeUses(map[string]string{"q.py": body})
	if len(emitted) != 0 {
		t.Errorf("a constant compared in SQL is a filter: %v", emitted)
	}
}

func TestOneAliasAndOneSwitchWithManyMatches(t *testing.T) {
	alias := "W1 = {'a1'}\nW2 = {'a2'}\nx = ev.get('event_type')\nif x in W1: pass\nif x not in W2: pass\nif x == 'c1' or x == 'c2': pass\n"
	if c, _ := EventTypeUses(map[string]string{"a.py": alias}); !same(types(c), []string{"a1", "a2", "c1", "c2"}) {
		t.Errorf("alias: %v", types(c))
	}
	sw := "package s\nfunc f(e E) {\n\tswitch e.EventType {\n\tcase \"s1\":\n\tcase \"s2\", \"s3\":\n\tcase \"s4\":\n\t}\n}\n"
	if c, _ := EventTypeUses(map[string]string{"s.go": sw}); !same(types(c), []string{"s1", "s2", "s3", "s4"}) {
		t.Errorf("switch: %v", types(c))
	}
}

func TestFleetEmittersFetchesAtMostSixteenAtOnce(t *testing.T) {
	var inFlight, peak atomic.Int64
	files := map[string]string{}
	for i := 0; i < 80; i++ {
		files[fmt.Sprintf("f%02d.py", i)] = "x = 1"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/custody/repos":
			_, _ = w.Write([]byte(`{"repos":["rob/a"]}`))
		case "/tree":
			var entries []string
			for p := range files {
				entries = append(entries, fmt.Sprintf(`{"path":%q,"mode":"0100644","size":5}`, p))
			}
			_, _ = w.Write([]byte(`{"entries":[` + strings.Join(entries, ",") + `]}`))
		case "/archive":
			n := inFlight.Add(1)
			for {
				old := peak.Load()
				if n <= old || peak.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(30 * time.Millisecond)
			inFlight.Add(-1)
			_, _ = w.Write([]byte("x = 1"))
		}
	}))
	defer srv.Close()
	if _, err := FleetEmitters(context.Background(), Door{Base: srv.URL, Client: srv.Client()}, "", []string{"never"}); err != nil {
		t.Fatal(err)
	}
	if p := peak.Load(); p > 16 || p < 2 {
		t.Errorf("peak concurrency %d, want 2..16", p)
	}
}

// MEASURED over the fleet's checkouts (foundry-tools#15344): 81 of 86 trees read
// as "consumes no literal event_type". Most mention the key not at all; these
// are the consumer shapes the others use that the regexes did not know.
func TestEventTypeUsesReadsSetHasAndAccessorComparisons(t *testing.T) {
	files := map[string]string{
		// theia/lineage: a constant Set asked about the event's type.
		"lens.ts": `const LINEAGE_EVENTS = new Set(["commit", "predict", "outcome"]);
export const isLineage = (node) => LINEAGE_EVENTS.has(node.data.event.eventType);
`,
		// a Rust comparison through an accessor call.
		"n.rs": `fn f(ev: &Ev) -> bool { ev.event_type.as_str() == "mistrial" }
`,
	}
	consumed, _ := EventTypeUses(files)
	want := []string{"commit", "mistrial", "outcome", "predict"}
	if got := types(consumed); !same(got, want) {
		t.Fatalf("consumed %v want %v", got, want)
	}
}

// A green is "no dark LITERAL consumer": the files that read the type without a
// literal are named, so the sentence cannot be read as "no dark consumer".
func TestConsumedEventsEmittedNamesTheFilesItCouldNotJudge(t *testing.T) {
	files := map[string]string{
		"internal/store/events.go": "package store\nfunc f(e Row) { counts[e.EventType]++ }\n",
		"internal/p/emit.go":       "package p\nvar x = Event{EventType: \"commit\"}\nfunc g(e *Row) { e.EventType = strings.TrimSpace(e.Other) }\n",
		"tron/normalize.rs":        "fn f(raw: &V) { let kind = raw.get(\"event_type\"); }\n",
		"app/lens.ts":              "const isX = (e) => e.eventType === 'x';\n",
	}
	state, report := ConsumedEventsEmitted(context.Background(), files, func(context.Context, []string) (map[string]string, error) {
		return map[string]string{"x": "r:f"}, nil
	})
	if state != 0 {
		t.Fatalf("state %d: %s", state, report)
	}
	for _, want := range []string{"NOT JUDGED", "internal/store/events.go", "tron/normalize.rs"} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q: %s", want, report)
		}
	}
	if strings.Contains(report, "emit.go") || strings.Contains(report, "lens.ts") {
		t.Errorf("a write, or a file that was judged, must not be listed: %s", report)
	}

	_, report = ConsumedEventsEmitted(context.Background(), map[string]string{"x.go": "package x\n"}, nil)
	if strings.Contains(report, "NOT JUDGED") {
		t.Errorf("a tree with nothing to say must not claim an unjudged file: %s", report)
	}
}

// Every match counts, not the first: a write before a read, two Set asks in one
// file, and the listing is ordered.
func TestDynamicEventReadersJudgesEveryMatchAndOrdersItsAnswer(t *testing.T) {
	files := map[string]string{
		"z.ts": "ev.eventType = 'x'\nconst k = row.eventType\n",
		"y.py": "x = d.get('event_type')\n",
		"b.rs": "let k = ev.event_type;\n",
		"a.go": "package a\nvar k = e.EventType\n",
		"c.sh": "echo $row[\"event_type\"]\n",
		"d.go": "package d\nfunc f() { e.EventType = \"x\"; e.EventType := 1 }\n",
		// not listed: a write only, a comment, a test, markdown, json, a judged file.
		"w.ts":      "ev.eventType = 'x'\n",
		"cmt.go":    "package c\n// k := e.EventType\n",
		"a_test.go": "package a\nvar k = e.EventType\n",
		"doc.md":    "row.event_type\n",
		"doc.json":  "{\"a\": \"row.event_type\"}\n",
		"j.go":      "package j\nvar k = e.EventType\n",
	}
	got := DynamicEventReaders(files, []EventSite{{"commit", "j.go"}})
	want := []string{"a.go", "b.rs", "c.sh", "y.py", "z.ts"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if note := dynamicNote(nil); note != "" {
		t.Errorf("nothing unjudged, nothing to say: %q", note)
	}
	note := dynamicNote(want)
	for _, w := range []string{"NOT JUDGED", "5 file(s)", "a.go, b.rs, c.sh", "+2 more"} {
		if !strings.Contains(note, w) {
			t.Errorf("note lacks %q: %s", w, note)
		}
	}
}

func TestEventTypeUsesAsksEverySetNotTheFirst(t *testing.T) {
	files := map[string]string{"lens.ts": `const A = new Set(["a1", "a2"]);
const B = new Set(["b1"]);
export const f = (e) => A.has(e.eventType) || B.has(e.eventType) || B.includes(e.event_type);
`}
	consumed, _ := EventTypeUses(files)
	if got, want := types(consumed), []string{"a1", "a2", "b1"}; !same(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
