package checks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
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
