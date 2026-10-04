package checks

import (
	"strings"
	"testing"
)

func TestParseWitnessCallReadsStatusTypeAndBody(t *testing.T) {
	cases := map[string]struct {
		out         string
		status      int
		ctype, body string
	}{
		"json":            {"HTTP 200\napplication/json\n{\"a\":1}", 200, "application/json", `{"a":1}`},
		"a body of lines": {"HTTP 200\ntext/event-stream\nevent: message\ndata: {}", 200, "text/event-stream", "event: message\ndata: {}"},
		"status alone":    {"HTTP 503", 503, "", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			status, ctype, body, err := ParseWitnessCall(c.out)
			if err != nil || status != c.status || ctype != c.ctype || body != c.body {
				t.Errorf("got %d %q %q %v", status, ctype, body, err)
			}
		})
	}
}

func TestParseWitnessCallRefusesWhatIsNoStatusLine(t *testing.T) {
	for _, out := range []string{"", "HTTP ok\nx\ny", "200\napplication/json\n{}", "witnesscall: no identity"} {
		if status, _, _, err := ParseWitnessCall(out); err == nil || status != 0 || !strings.Contains(err.Error(), "no status line") {
			t.Errorf("%q: %d %v", out, status, err)
		}
	}
}

func TestTheIdentityLineSaysHowTheWitnessWasAsked(t *testing.T) {
	if got := WitnessAskedAs("spiffe://notusmi.com/job/gate/g", 0, 3); got != "identity: asked as spiffe://notusmi.com/job/gate/g at https://narcissus:8201/mcp" {
		t.Errorf("%q", got)
	}
	if got := WitnessAskedAs("spiffe://x/job/gate/g", 1, 3); got != "identity: asked as spiffe://x/job/gate/g at https://narcissus:8201/mcp (1 of 3 ask(s) fell back to http://narcissus:8200/mcp in the clear)" {
		t.Errorf("%q", got)
	}
	if got := WitnessInTheClear("no socket"); got != "identity: none — asked http://narcissus:8200/mcp in the clear: no socket" {
		t.Errorf("%q", got)
	}
}
