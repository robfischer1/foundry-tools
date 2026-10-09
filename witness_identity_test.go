package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/checks"
)

const (
	whoamiNeedle = `"/usr/local/bin/witnesscall","whoami"`
	postNeedle   = `"/usr/local/bin/witnesscall","post","https://narcissus:8201/mcp","/tmp/witness-request.json"`
	laneSVID     = "spiffe://notusmi.com/job/gate/gate-nereus-abc1234-xyz12"
)

// spireRun is a run whose lane forwarded its SPIRE socket.
func spireRun() *run {
	return newRun(dag.Directory(), "", "base-sha").withSpire(dag.LoadSocketFromID("spire-agent-socket"))
}

// clearAsks stands in for the clear-port ask and counts it.
func clearAsks(t *testing.T) *int {
	t.Helper()
	n := new(int)
	prev := askWitness
	askWitness = func(context.Context, string) (int, string, string, error) {
		*n++
		return 200, "application/json", "clear", nil
	}
	t.Cleanup(func() { askWitness = prev })
	return n
}

func TestARunWithNoSocketAsksInTheClearAndSaysWhy(t *testing.T) {
	engine.reset()
	n := clearAsks(t)
	via := newRun(dag.Directory(), "", "base-sha").witnessAsker(context.Background())
	if _, _, body, err := via.ask(context.Background(), "{}"); err != nil || body != "clear" || *n != 1 {
		t.Errorf("body %q err %v clear asks %d", body, err, *n)
	}
	if got := via.say(); got != checks.WitnessInTheClear("the lane forwarded no --spire socket") {
		t.Errorf("%q", got)
	}
	if engine.chain("witnesscall") != "" {
		t.Error("a run with no socket built witnesscall")
	}
}

func TestARunWithTheSocketAsksAsTheLanesSVID(t *testing.T) {
	engine.reset()
	n := clearAsks(t)
	prev := witnessStamp
	witnessStamp = func() string { return "run-1" }
	t.Cleanup(func() { witnessStamp = prev })
	engine.stdout(whoamiNeedle, laneSVID+"\n")
	engine.stdout(postNeedle, "HTTP 200\napplication/json\n{\"ok\":true}")

	via := spireRun().witnessAsker(context.Background())
	status, ctype, body, err := via.ask(context.Background(), `{"id":1}`)
	if err != nil || status != 200 || ctype != "application/json" || body != `{"ok":true}` || *n != 0 {
		t.Fatalf("%d %q %q %v clear asks %d", status, ctype, body, err, *n)
	}
	if got := via.say(); got != checks.WitnessAskedAs(laneSVID, 0, 1) {
		t.Errorf("%q", got)
	}
	c := engine.chain(postNeedle)
	wantCalls(t, c,
		[]string{"withUnixSocket", `path:"/run/spire/agent.sock"`, `owner:"65532:65532"`},
		[]string{"withEnvVariable", `name:"WITNESSCALL_SOCKET"`, `value:"unix:///run/spire/agent.sock"`},
		[]string{"withEnvVariable", `name:"WITNESS_RUN"`, `value:"run-1"`},
		[]string{"withNewFile", `path:"/tmp/witness-request.json"`},
	)
	if !hasCall(c, "withExec", "expect:ANY") {
		t.Errorf("the post does not run under anyExit:\n%s", c)
	}
	if engine.chain(`"go","build","-trimpath","-o","/out/witnesscall","./witnesscall"`) == "" {
		t.Error("witnesscall is not built from the module's own source")
	}
}

func TestNoIdentityIsTheClearPortNeverAVerdict(t *testing.T) {
	cases := map[string]struct {
		script func()
		why    string
	}{
		"the agent has no entry": {func() {
			engine.exitCode(whoamiNeedle, 3)
			engine.stderr(whoamiNeedle, "witnesscall: no identity from unix:///run/spire/agent.sock within 20s")
		}, "witnesscall: no identity from unix:///run/spire/agent.sock within 20s"},
		"the engine would not build it": {func() { engine.fail(whoamiNeedle, "engine gone") }, "witnesscall never ran: "},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			engine.reset()
			n := clearAsks(t)
			c.script()
			via := spireRun().witnessAsker(context.Background())
			if _, _, body, err := via.ask(context.Background(), "{}"); err != nil || body != "clear" || *n != 1 {
				t.Errorf("body %q err %v clear asks %d", body, err, *n)
			}
			if got := via.say(); !strings.HasPrefix(got, "identity: none — ") || !strings.Contains(got, c.why) {
				t.Errorf("%q", got)
			}
			if engine.chain(postNeedle) != "" {
				t.Error("asked narcissus as an identity it did not have")
			}
		})
	}
}

func TestAnIdentifiedAskThatFailsFallsBackToTheClearAndCountsIt(t *testing.T) {
	cases := map[string]func(){
		"witnesscall could not ask": func() {
			engine.exitCode(postNeedle, 2)
			engine.stderr(postNeedle, "witnesscall: the witness did not answer")
		},
		"no status line":   func() { engine.stdout(postNeedle, "garbage") },
		"the exec errored": func() { engine.fail(postNeedle, "engine gone") },
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			engine.reset()
			n := clearAsks(t)
			prev := witnessSleep
			t.Cleanup(func() { witnessSleep = prev })
			witnessSleep = func(context.Context, time.Duration) error { return nil }
			engine.stdout(whoamiNeedle, laneSVID)
			script()
			via := spireRun().witnessAsker(context.Background())
			if _, _, body, err := via.ask(context.Background(), "{}"); err != nil || body != "clear" || *n != 1 {
				t.Errorf("body %q err %v clear asks %d", body, err, *n)
			}
			if got := via.say(); got != checks.WitnessAskedAs(laneSVID, 1, 1) {
				t.Errorf("%q", got)
			}
		})
	}
}

func TestIdentifiedPostAnswersWhatWitnesscallPrinted(t *testing.T) {
	engine.reset()
	engine.stdout(postNeedle, "HTTP 503\ntext/plain\nbusy")
	status, ctype, body, err := identifiedPost(witnessCaller(dag.LoadSocketFromID("s"), "r"))(context.Background(), "{}")
	if err != nil || status != 503 || ctype != "text/plain" || body != "busy" {
		t.Errorf("%d %q %q %v", status, ctype, body, err)
	}
	engine.reset()
	engine.fail(postNeedle, "engine gone")
	if _, _, _, err := identifiedPost(witnessCaller(dag.LoadSocketFromID("s"), "r"))(context.Background(), "{}"); err == nil || !strings.Contains(err.Error(), "engine gone") {
		t.Errorf("an exec that never ran answered %v", err)
	}
	engine.reset()
	engine.exitCode(postNeedle, 2)
	engine.stderr(postNeedle, "cannot ask")
	if _, _, _, err := identifiedPost(witnessCaller(dag.LoadSocketFromID("s"), "r"))(context.Background(), "{}"); err == nil || !strings.Contains(err.Error(), "cannot ask") {
		t.Errorf("err %v", err)
	}
}

// gate-file hands the lane's socket to the run that grades it.
func TestGateFileKeepsTheLanesSocket(t *testing.T) {
	m := gateOn(t, cleanVector)
	spire := dag.LoadSocketFromID("spire-agent-socket")
	if _, err := m.GateFile(context.Background(), fakeTree, gatePin, "base-sha", "", nil, false, spire, nil, "", nil); err != nil || m.spire != spire {
		t.Fatalf("spire %v err %v", m.spire, err)
	}
}

// A timeout over mTLS is the witness being slow. Repeating it in the clear
// doubles its load and files an `unidentified` record (2465 on 2026-10-07):
// the ask settles could-not-run, asked once, and the clear port hears nothing.
func TestAnIdentifiedAskThatTimesOutIsNotRepeatedInTheClear(t *testing.T) {
	engine.reset()
	n := clearAsks(t)
	prev := witnessSleep
	t.Cleanup(func() { witnessSleep = prev })
	witnessSleep = func(context.Context, time.Duration) error { return nil }
	engine.stdout(whoamiNeedle, laneSVID)
	engine.exitCode(postNeedle, checks.WitnessNoAnswerExit)
	engine.stderr(postNeedle, "witnesscall: the witness did not answer: context deadline exceeded")

	via := spireRun().witnessAsker(context.Background())
	_, _, body, err := via.ask(context.Background(), "{}")
	if !errors.Is(err, checks.ErrWitnessNoAnswer) || body != "" || *n != 0 {
		t.Errorf("body %q err %v clear asks %d", body, err, *n)
	}
	if got := strings.Count(engine.chain(postNeedle), `"post"`); got != 1 {
		t.Errorf("the timed-out ask was posted %d times, want 1", got)
	}
	if got := via.say(); got != checks.WitnessAskedAs(laneSVID, 0, 1) {
		t.Errorf("%q", got)
	}
}
