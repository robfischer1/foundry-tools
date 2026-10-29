package fleetwitness

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// put writes a file, making its directories.
func put(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// treeIn writes files to a directory and answers the In an atom is handed over
// it, WITHOUT git: the population is the file list, as Collect would build it
// for a tree with nothing ignored.
func treeIn(t *testing.T, files map[string]string) atomkit.In {
	t.Helper()
	dir := t.TempDir()
	var names []string
	for name, body := range files {
		put(t, dir, name, body)
		names = append(names, name)
	}
	sort.Strings(names)
	return atomkit.In{
		Root: dir, Files: checks.GatePopulation(names), Committable: names, Tracked: names,
		Now: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
	}
}

// run runs the atom over an input.
func run(t *testing.T, in atomkit.In) checks.Verdict {
	t.Helper()
	return check(context.Background(), in)
}

// expect holds a verdict to its state, its result word (an ABSENT pass reads
// "absent") and the words it must carry. A pass's Reason is only "<id>: PASS" —
// what the atom said rides in its Logs — so the words are looked for in both.
func expect(t *testing.T, v checks.Verdict, state checks.State, result string, needles ...string) {
	t.Helper()
	if v.State != int(state) || v.Result != result {
		t.Errorf("%s: state %d (%s), want %d (%s)\n%s", v.Atom, v.State, v.Result, state, result, v.Reason)
	}
	said := v.Reason + "\n" + strings.Join(v.Logs, "\n")
	for _, n := range needles {
		if !strings.Contains(said, n) {
			t.Errorf("%s: reason and logs lack %q:\n%s", v.Atom, n, said)
		}
	}
}

var pass = checks.StatePass.String()

var findings = checks.StateFindings.String()

var cannot = checks.StateCannotRun.String()

// stateOf is a table's plain int as the catalogue's State.
func stateOf(n int) checks.State { return checks.State(n) }

// narcissus is a stand-in for the witness's REST door, reached through the
// atom's Do seam: it records every request and answers per file, by the path the
// request names. GET /openapi.json is the readiness read; POST /v1/witness the ask.
type narcissus struct {
	t  *testing.T
	mu sync.Mutex
	// readyStatus is what GET /openapi.json answers (0 is 200); readyHits counts the probes.
	readyStatus atomic.Int64
	readyHits   atomic.Int64
	// readyFail, when not 0, makes a probe fail: 2 never reached the door, 4 no answer in time.
	readyFail atomic.Int64
	// readyURLs is every address a probe went to.
	readyURLs []string
	hits      map[string]int
	bodies    []string
	live      atomic.Int64
	peak      atomic.Int64
	// answer says what one ask of a file gets; n counts that file's asks from 1.
	answer func(w http.ResponseWriter, path string, n int)
}

func newNarcissus(t *testing.T, answer func(w http.ResponseWriter, path string, n int)) *narcissus {
	t.Helper()
	return &narcissus{t: t, hits: map[string]int{}, answer: answer}
}

// timeout is a deadline failure as the client reports one.
type timeout struct{}

func (timeout) Error() string   { return "Client.Timeout exceeded while awaiting headers" }
func (timeout) Timeout() bool   { return true }
func (timeout) Temporary() bool { return true }

var _ net.Error = timeout{}

// response is an answer as the client hands it back.
func response(status int, header http.Header, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

// do stands in for the client: the request goes to the stand-in, nowhere else.
func (n *narcissus) do(req *http.Request) (*http.Response, error) {
	u := req.URL.String()
	if req.Method == http.MethodGet && req.URL.Path == "/openapi.json" {
		n.readyHits.Add(1)
		n.mu.Lock()
		n.readyURLs = append(n.readyURLs, u)
		n.mu.Unlock()
		switch n.readyFail.Load() {
		case 2:
			return nil, errors.New(`Get "` + u + `": dial tcp: connection refused`)
		case 4:
			return nil, timeout{}
		}
		status := int(n.readyStatus.Load())
		if status == 0 {
			return response(http.StatusOK, http.Header{"X-Star": {"narcissus"}}, "{}"), nil
		}
		return response(status, http.Header{}, `{"service":"narcissus","status":"starting","ready":false,"store":"waiting"}`), nil
	}
	if req.Method != http.MethodPost || u != checks.WitnessAskURL || req.Header.Get("Content-Type") != "application/json" {
		n.t.Errorf("%s %s (%s) is not the ask", req.Method, u, req.Header.Get("Content-Type"))
	}
	raw, _ := io.ReadAll(req.Body)
	var args struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(raw, &args)
	p := args.Path
	n.mu.Lock()
	n.hits[p]++
	count := n.hits[p]
	n.bodies = append(n.bodies, string(raw))
	n.mu.Unlock()
	cur := n.live.Add(1)
	for old := n.peak.Load(); cur > old && !n.peak.CompareAndSwap(old, cur); old = n.peak.Load() {
	}
	defer n.live.Add(-1)
	w := &door{header: http.Header{}, status: http.StatusOK}
	n.answer(w, p, count)
	if w.dropped {
		return nil, errors.New("read tcp: connection reset by peer")
	}
	return response(w.status, w.header, w.body.String()), nil
}

// door is the ResponseWriter an answer is built on; Hijack is how a test
// answer drops the connection.
type door struct {
	header  http.Header
	status  int
	body    strings.Builder
	dropped bool
}

func (d *door) Header() http.Header         { return d.header }
func (d *door) WriteHeader(code int)        { d.status = code }
func (d *door) Write(b []byte) (int, error) { return d.body.Write(b) }
func (d *door) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	d.dropped = true
	c, peer := net.Pipe()
	_ = peer.Close()
	return c, nil, nil
}

func (n *narcissus) total() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	total := 0
	for _, c := range n.hits {
		total += c
	}
	return total
}

// said is a witness answer carrying a verdict, as the REST door sends the verb's text.
func said(verdict, recommendation string) string {
	inner, _ := json.Marshal(map[string]any{"verdict": verdict, "recommendation": recommendation})
	return string(inner)
}

func reply(verdict string) func(http.ResponseWriter, string, int) {
	return func(w http.ResponseWriter, _ string, _ int) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Star", "narcissus")
		_, _ = fmt.Fprint(w, said(verdict, "reuse it"))
	}
}

// dropped closes the connection with no answer: a transport failure.
func dropped(w http.ResponseWriter) {
	if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
		_ = conn.Close()
	}
}

// witnessIn is an input over a tree of the named files, a change set naming
// them (and anything else), and a Do that reaches the stand-in, with no waiting.
func witnessIn(t *testing.T, n *narcissus, tree map[string]string, changed ...string) (atomkit.In, *[]time.Duration) {
	t.Helper()
	in := treeIn(t, tree)
	in.Changed = changed
	in.Origin = "https://git.notusmi.com/rob/some-star.git"
	var pauses []time.Duration
	var mu sync.Mutex
	in.Witness = atoms.Witness{Sleep: func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		pauses = append(pauses, d)
		return nil
	}}
	if n != nil {
		in.Witness.Do = n.do
	}
	return in, &pauses
}

func TestFleetWitnessAsksOnlyForSourceTheStarAuthored(t *testing.T) {
	for _, tc := range []struct {
		name    string
		changed []string
		needles []string
	}{
		{"a change with no source", []string{"README.md", "go.sum"}, []string{"nothing to witness — no changed .py or .go file the star authored"}},
		{"only a test", []string{"a_test.go", "tests/test_x.py"}, []string{"nothing to witness", "skipped 2 test file(s)"}},
		{"only vendored source", []string{"vendor/x/y.go"}, []string{"nothing to witness", "skipped 1 vendored file(s)"}},
		{"only a language with no analyzer", []string{"lib.rs"}, []string{"nothing to witness", "skipped 1 file(s) in languages the witness has no analyzer for"}},
		{"an empty change set", nil, []string{"nothing to witness"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newNarcissus(t, reply("novel"))
			in, _ := witnessIn(t, n, map[string]string{"README.md": "x"}, tc.changed...)
			in.Witness.Do = func(req *http.Request) (*http.Response, error) {
				t.Errorf("a change with nothing to witness sent %s %s", req.Method, req.URL)
				return nil, errors.New("no")
			}
			v := run(t, in)
			expect(t, v, stateOf(0), pass, append(tc.needles, "narcissus — fleet:witness:")...)
			if n.total() != 0 {
				t.Errorf("a change with nothing to witness made %d request(s)", n.total())
			}
			if strings.Contains(strings.Join(v.Logs, "\n"), "identity:") {
				t.Errorf("an ask that was never made names an identity:\n%v", v.Logs)
			}
		})
	}
}

func TestFleetWitnessSettlesOnTheWitnessAnswer(t *testing.T) {
	tree := map[string]string{"a.go": "package a\nfunc A() {}\n", "b.py": "def b():\n  pass\n"}
	status := func(code int) func(http.ResponseWriter, string, int) {
		return func(w http.ResponseWriter, _ string, _ int) { w.WriteHeader(code) }
	}
	for _, tc := range []struct {
		name    string
		answer  func(http.ResponseWriter, string, int)
		state   int
		result  string
		asks    int
		pauses  int
		needles []string
	}{
		{"novel on both axes is a pass", reply("novel"), 0, pass, 2, 0, []string{"clean — 2 file(s) witnessed, novel on both axes"}},
		{"a duplicated standard is a finding", reply("standard"), 1, findings, 2, 0, []string{"findings in 2 of 2 file(s)", "duplicates a standard: reuse it"}},
		{"a convention is advisory and clean", reply("convention"), 0, pass, 2, 0, []string{"2 advisory (reuse, not rewrite)"}},
		{"a 4xx is the witness's own answer and is not asked again", status(400), 2, cannot, 2, 0, []string{"could not consult 2 of 2 file(s)", "narcissus returned HTTP 400"}},
		{"a 500 is not asked again either", status(500), 2, cannot, 2, 0, []string{"narcissus returned HTTP 500"}},
		{"a gateway that cannot reach the witness is asked again, to the bound", status(503), 2, cannot, 6, 4, []string{"narcissus returned HTTP 503"}},
		{"a dropped connection is asked again, to the bound", func(w http.ResponseWriter, _ string, _ int) { dropped(w) }, 2, cannot, 6, 4,
			[]string{"could not ask the witness:", "could not consult 2 of 2 file(s)"}},
		{"one transport failure, then an answer, is a pass", func(w http.ResponseWriter, p string, n int) {
			if n == 1 {
				dropped(w)
				return
			}
			reply("novel")(w, p, n)
		}, 0, pass, 4, 2, []string{"clean — 2 file(s) witnessed"}},
		{"a 502 then an answer is a pass", func(w http.ResponseWriter, p string, n int) {
			if n == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			reply("novel")(w, p, n)
		}, 0, pass, 4, 2, []string{"clean"}},
		{"a refusal naming no unit is nothing to witness, clean", func(w http.ResponseWriter, _ string, _ int) {
			inner, _ := json.Marshal(map[string]any{"verdict": "refused", "failure": map[string]any{"detail": "no function/method/class unit"}})
			_, _ = w.Write(inner)
		}, 0, pass, 2, 0, []string{"nothing to witness — every changed source file had no unit"}},
		{"a finding beside a file that could not be consulted is a finding that says so", func(w http.ResponseWriter, p string, n int) {
			if p == "b.py" {
				w.WriteHeader(http.StatusTeapot)
				return
			}
			reply("standard")(w, p, n)
		}, 1, findings, 2, 0, []string{"findings in 1 of 2 file(s)", "also could not consult 1: b.py"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newNarcissus(t, tc.answer)
			in, pauses := witnessIn(t, n, tree, "a.go", "b.py", "notes.md")
			v := run(t, in)
			expect(t, v, stateOf(tc.state), tc.result, append(tc.needles, checks.WitnessAskedAs())...)
			if n.total() != tc.asks {
				t.Errorf("%d request(s), want %d", n.total(), tc.asks)
			}
			if len(*pauses) != tc.pauses {
				t.Errorf("%d pause(s), want %d", len(*pauses), tc.pauses)
			}
			for _, d := range *pauses {
				if d != checks.WitnessRetryPause {
					t.Errorf("paused %s between attempts, want %s", d, checks.WitnessRetryPause)
				}
			}
		})
	}
}

func TestFleetWitnessAsksAboutTheFileAsTheChainDid(t *testing.T) {
	n := newNarcissus(t, reply("novel"))
	in, _ := witnessIn(t, n, map[string]string{"pkg/a.go": "package a\n"}, "pkg/a.go")
	run(t, in)
	if len(n.bodies) != 1 {
		t.Fatalf("%d request(s)", len(n.bodies))
	}
	want := checks.WitnessArgs("package a\n", "go", "ci:gate:some-star@HEAD", "pkg/a.go")
	if n.bodies[0] != want {
		t.Errorf("the request was\n%s\nwant\n%s", n.bodies[0], want)
	}
	t.Run("a star with no origin is unknown", func(t *testing.T) {
		n := newNarcissus(t, reply("novel"))
		in, _ := witnessIn(t, n, map[string]string{"a.py": "x = 1\n"}, "a.py")
		in.Origin = ""
		run(t, in)
		if !strings.Contains(n.bodies[0], `"caller":"ci:gate:unknown@HEAD"`) {
			t.Errorf("the caller was not unknown:\n%s", n.bodies[0])
		}
	})
}

func TestFleetWitnessAFileItCannotReadIsThatFilesRowAlone(t *testing.T) {
	n := newNarcissus(t, reply("novel"))
	in, _ := witnessIn(t, n, map[string]string{"a.go": "package a\n"}, "a.go", "gone.go")
	v := run(t, in)
	expect(t, v, stateOf(2), cannot, "could not consult 1 of 2 file(s)", "could not ask:", "gone.go")
	if n.total() != 1 {
		t.Errorf("%d request(s): the file that would not read must not be asked about", n.total())
	}
}

func TestFleetWitnessRowsKeepTheChangeSetsOrderWhateverOrderTheAnswersArriveIn(t *testing.T) {
	release := make(chan struct{})
	n := newNarcissus(t, func(w http.ResponseWriter, p string, c int) {
		if p == "a.go" {
			<-release // the first file answers last
		}
		if p == "c.go" {
			close(release)
		}
		reply("novel")(w, p, c)
	})
	in, _ := witnessIn(t, n, map[string]string{"a.go": "package a\n", "b.go": "package b\n", "c.go": "package c\n"}, "a.go", "b.go", "c.go")
	v := run(t, in)
	expect(t, v, stateOf(0), pass, "clean — 3 file(s) witnessed")
	said := v.Reason + "\n" + strings.Join(v.Logs, "\n")
	a, b, c := strings.Index(said, "| a.go"), strings.Index(said, "| b.go"), strings.Index(said, "| c.go")
	if a < 0 || a >= b || b >= c {
		t.Errorf("rows are out of the change set's order (a %d, b %d, c %d):\n%s", a, b, c, said)
	}
}

func TestFleetWitnessAsksFourAtOnceAtMost(t *testing.T) {
	n := newNarcissus(t, func(w http.ResponseWriter, p string, c int) {
		time.Sleep(30 * time.Millisecond)
		reply("novel")(w, p, c)
	})
	tree := map[string]string{}
	var changed []string
	for i := range 12 {
		name := fmt.Sprintf("f%02d.go", i)
		tree[name] = "package f\n"
		changed = append(changed, name)
	}
	in, _ := witnessIn(t, n, tree, changed...)
	// A slot never given back would block the twelve for good; say so, do not hang.
	done := make(chan checks.Verdict, 1)
	go func() { done <- run(t, in) }()
	select {
	case v := <-done:
		expect(t, v, stateOf(0), pass, "12 file(s) witnessed")
	case <-time.After(20 * time.Second):
		t.Fatal("twelve asks did not finish: a slot was not given back")
	}
	if peak := n.peak.Load(); peak > int64(checks.WitnessWorkers) || peak < 2 {
		t.Errorf("%d asks in flight at once, want 2..%d", peak, checks.WitnessWorkers)
	}
}

func TestFleetWitnessAChangeSetThatWouldNotComputeIsSaidAsIt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		state   int
		result  string
		needles []string
	}{
		{"a worktree snapshot has no change set, and the verdict says so", atoms.SnapshotError{Kind: "worktree"}, 0, pass,
			[]string{"no change set to witness at pre-push: the source is a worktree snapshot with no commits"}},
		{"a base the history does not reach", fmt.Errorf("the base abc is not in this history"), 2, cannot,
			[]string{"CANNOT RUN - could not read the change set: the base abc is not in this history"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newNarcissus(t, reply("novel"))
			in, _ := witnessIn(t, n, map[string]string{"a.go": "x"}, "a.go")
			in.ChangedErr = tc.err
			expect(t, run(t, in), stateOf(tc.state), tc.result, tc.needles...)
			if n.total() != 0 {
				t.Errorf("%d request(s) without a change set", n.total())
			}
		})
	}
}

// THE ASK IS POST /v1/witness ON THE REST DOOR, as the lane pod's
// ServiceAccount: one request per file, the verb's arguments as the body, and
// the transport's word decides whether it is asked again.
func TestFleetWitnessAsksOnTheRESTDoor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		do      func(n int) (*http.Response, error)
		state   int
		result  string
		asks    int
		needles []string
	}{
		{"an answer is the whole answer", func(int) (*http.Response, error) {
			return response(200, http.Header{"X-Star": {"narcissus"}}, said("novel", "")), nil
		}, 0, pass, 1, []string{"clean", checks.WitnessAskedAs()}},
		{"an ask that never reached the witness is asked again to the bound", func(int) (*http.Response, error) {
			return nil, errors.New("dial tcp: connection refused")
		}, 2, cannot, 3, []string{"could not ask the witness: dial tcp: connection refused", "could not consult 1 of 1 file(s)"}},
		{"an ask that was sent and drew no answer is never repeated", func(int) (*http.Response, error) {
			return nil, timeout{}
		}, 2, cannot, 1, []string{"could not ask the witness:", "did not answer the ask in time"}},
		{"an answer cut short is never repeated", func(int) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(iotest.ErrReader(errors.New("reset")))}, nil
		}, 2, cannot, 1, []string{"did not answer the ask in time", "the answer was cut short: reset"}},
		{"the star's own refusal is the verb erroring, not asked again", func(int) (*http.Response, error) {
			return response(400, http.Header{"X-Star": {"narcissus"}}, `{"code":"bad_args","detail":"x"}`), nil
		}, 2, cannot, 1, []string{"the witness verb errored:", "bad_args"}},
		{"a proxy's 403 is narcissus's HTTP status, not asked again", func(int) (*http.Response, error) {
			return response(403, http.Header{}, "Access denied"), nil
		}, 2, cannot, 1, []string{"narcissus returned HTTP 403"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, _ := witnessIn(t, nil, map[string]string{"a.go": "package a\n"}, "a.go")
			var asks int
			var got []string
			in.Witness.Do = func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/openapi.json" {
					return response(200, http.Header{}, "{}"), nil
				}
				asks++
				raw, _ := io.ReadAll(req.Body)
				got = append(got, req.Method+" "+req.URL.String()+" "+req.Header.Get("Content-Type")+" "+string(raw))
				return tc.do(asks)
			}
			expect(t, run(t, in), stateOf(tc.state), tc.result, tc.needles...)
			if asks != tc.asks {
				t.Errorf("asked %d time(s), want %d", asks, tc.asks)
			}
			want := "POST " + checks.WitnessAskURL + " application/json " + checks.WitnessArgs("package a\n", "go", "ci:gate:some-star@HEAD", "a.go")
			for _, g := range got {
				if g != want {
					t.Errorf("sent\n%s\nwant\n%s", g, want)
				}
			}
		})
	}
}

// With no seam set, the atom asks on the plaintext client.
func TestWitnessDefaultDoIsTheWitnessClient(t *testing.T) {
	if do(atoms.Witness{}) == nil {
		t.Fatal("no default client")
	}
	called := false
	if _, err := do(atoms.Witness{Do: func(*http.Request) (*http.Response, error) { called = true; return nil, errors.New("x") }})(nil); err == nil || !called {
		t.Fatal("the seam was not used")
	}
}

func TestWitnessDefaults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleep(atoms.Witness{})(ctx, time.Hour); err != context.Canceled {
		t.Errorf("the default sleep answered %v for a cancelled context", err)
	}
}

// A witness that does not say ready is a could-not-run about the target: state
// 2 (never a finding, never a pass), named so, and not one ask is sent. The
// revert of narcissus's own tip must not wait on narcissus (foundry-tools#18242).
func TestFleetWitnessCannotRunWhenTheWitnessIsNotReady(t *testing.T) {
	n := newNarcissus(t, reply("standard"))
	n.readyStatus.Store(http.StatusServiceUnavailable)
	in, pauses := witnessIn(t, n, map[string]string{"a.go": "package a\nfunc A() {}\n"}, "a.go")
	v := run(t, in)
	expect(t, v, stateOf(2), cannot, "CANNOT RUN - the witness (narcissus) is not ready: HTTP 503 (status=starting store=waiting)", "No file was asked")
	if n.total() != 0 {
		t.Errorf("a witness that was not ready was asked %d time(s)", n.total())
	}
	if got := n.readyHits.Load(); got != int64(checks.WitnessReadyAttempts) {
		t.Errorf("probed %d time(s), want %d", got, checks.WitnessReadyAttempts)
	}
	if len(*pauses) != checks.WitnessReadyAttempts-1 {
		t.Errorf("paused %v between probes", *pauses)
	}
}

// THE PROBE IS ON THE REST DOOR IT ASKS ON: GET /openapi.json at :8203, and
// narcissus being down, refusing or slow is the same could-not-run as a 503.
func TestFleetWitnessProbesReadinessOnTheRESTDoor(t *testing.T) {
	n := newNarcissus(t, reply("novel"))
	in, _ := witnessIn(t, n, map[string]string{"a.go": "package a\nfunc A() {}\n"}, "a.go")
	expect(t, run(t, in), stateOf(0), pass, "clean")
	if len(n.readyURLs) != 1 || n.readyURLs[0] != "http://narcissus:8203/openapi.json" {
		t.Errorf("probed %v", n.readyURLs)
	}
}

func TestFleetWitnessAWitnessThatIsDownRefusingOrSlowIsCouldNotRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		exit int64
	}{{"down or refusing (never reached)", 2}, {"slow (no answer in time)", 4}} {
		t.Run(tc.name, func(t *testing.T) {
			n := newNarcissus(t, reply("standard"))
			n.readyFail.Store(tc.exit)
			in, pauses := witnessIn(t, n, map[string]string{"a.go": "package a\nfunc A() {}\n"}, "a.go")
			expect(t, run(t, in), stateOf(2), cannot,
				"CANNOT RUN - the witness (narcissus) is not ready: unreachable:", "No file was asked")
			if n.total() != 0 || n.readyHits.Load() != int64(checks.WitnessReadyAttempts) || len(*pauses) != checks.WitnessReadyAttempts-1 {
				t.Errorf("%d ask(s), %d probe(s), pauses %v", n.total(), n.readyHits.Load(), *pauses)
			}
		})
	}
}

// A blip is not an outage: ready on the second probe, the asks go out.
func TestFleetWitnessAsksOnceTheWitnessIsReadyAgain(t *testing.T) {
	n := newNarcissus(t, reply("novel"))
	n.readyStatus.Store(http.StatusServiceUnavailable)
	in, _ := witnessIn(t, n, map[string]string{"a.go": "package a\nfunc A() {}\n"}, "a.go")
	sleep := in.Witness.Sleep
	in.Witness.Sleep = func(ctx context.Context, d time.Duration) error {
		n.readyStatus.Store(http.StatusOK)
		return sleep(ctx, d)
	}
	v := run(t, in)
	expect(t, v, stateOf(0), pass, "clean")
	if n.total() != 1 {
		t.Errorf("asked %d time(s), want 1", n.total())
	}
}

// THE DEADLINE. A voting fleet:witness asks for real, so its honest worst case
// grows with the change set while every other atom's does not.
func TestWitnessDeadlineIsTheWorstCaseOfTheSourcesItWouldAsk(t *testing.T) {
	const one = 3 * (120*time.Second + 2*time.Second)
	for _, tc := range []struct {
		name string
		in   atomkit.In
		want time.Duration
	}{
		{"no change set", atomkit.In{}, 0},
		{"only files nobody asks about", atomkit.In{Changed: []string{"README.md", "a_test.go"}}, 0},
		{"one source", atomkit.In{Changed: []string{"a.go"}}, one},
		{"four sources are one round", atomkit.In{Changed: []string{"a.go", "b.go", "c.py", "d.py"}}, one},
		{"five sources are two rounds", atomkit.In{Changed: []string{"a.go", "b.go", "c.py", "d.py", "e.go"}}, 2 * one},
		{"a test file is not a source", atomkit.In{Changed: []string{"a.go", "a_test.go"}}, one},
		{"a change set that would not compute", atomkit.In{ChangedErr: context.Canceled, Changed: []string{"a.go"}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := deadline(tc.in); got != tc.want {
				t.Errorf("deadline %v, want %v", got, tc.want)
			}
			if Def.Deadline == nil {
				t.Fatal("the declaration carries no deadline")
			}
		})
	}
}

// The declaration is the catalogue row it replaced: the same stage, group, text and
// fork lane, and the needs the split table gives it.
func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrepush || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "none" || Def.NotOnForks != checks.ForkFleetStyle || Def.SubsumedBy != "" || Def.NeedsManifest {
		t.Errorf("%+v", Def)
	}
	if Def.Desc != "Every changed .py/.go source file is shown to the code witness (narcissus), never a test: a canonical-class or Standard match is a finding, a Convention is advisory, novel is clean." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if len(Def.Needs) != 0 {
		t.Errorf("needs %v", Def.Needs)
	}
}

// A request that cannot be built is never sent.
func TestWitnessSendRefusesAnAddressItCannotBuild(t *testing.T) {
	w := atoms.Witness{Do: func(*http.Request) (*http.Response, error) {
		t.Error("a request that could not be built was sent")
		return nil, errors.New("no")
	}}
	if _, _, _, err := send(context.Background(), w, http.MethodGet, "http://bad host/", nil); err == nil {
		t.Fatal("no error for an address with a space in its host")
	}
}
