package checks

import (
	"net/http"
	"strings"
	"testing"
)

func TestWitnessResultReadsTheRESTAnswer(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, star, ctype, body string
		status, wantStatus      int
		want                    string
	}{
		{"a 200 is the verb's text", "narcissus", "application/json", `{"verdict":"novel"}`, 200, 200,
			`{"content":[{"text":"{\"verdict\":\"novel\"}","type":"text"}],"isError":false}`},
		{"a result envelope is its text", "narcissus", "application/vnd.stellar.result+json; charset=utf-8", `{"text":"t","structuredContent":{}}`, 200, 200,
			`{"content":[{"text":"t","type":"text"}],"isError":false}`},
		{"an envelope that is not json is the body", "narcissus", "application/vnd.stellar.result+json", `oops`, 200, 200,
			`{"content":[{"text":"oops","type":"text"}],"isError":false}`},
		{"a star's 4xx is the verb's refusal", "narcissus", "application/json", `{"code":"bad_args","detail":"x"}`, 400, 200,
			`{"content":[{"text":"{\"code\":\"bad_args\",\"detail\":\"x\"}","type":"text"}],"isError":true}`},
		{"a 403 with no star is the proxy, passed through", "", "text/plain", "Access denied", 403, 403, "Access denied"},
		{"a 503 passes through", "narcissus", "text/plain", "busy", 503, 503, "busy"},
		{"a 3xx passes through", "", "", "", 302, 302, ""},
	} {
		status, got := WitnessResult(c.status, c.star, c.ctype, c.body)
		if status != c.wantStatus || got != c.want {
			t.Errorf("%s: (%d, %s), want (%d, %s)", c.name, status, got, c.wantStatus, c.want)
		}
	}
	_, refused := WitnessResult(400, "narcissus", "application/json", "no such unit")
	if row := ClassifyWitness("a.py", http.StatusOK, refused); !strings.Contains(row.Reason, "the witness verb errored: no such unit") {
		t.Errorf("refusal: %+v", row)
	}
}

func TestWitnessClientIsPlainBoundedAndUnproxied(t *testing.T) {
	t.Parallel()
	c := WitnessClient()
	tr := c.Transport.(*http.Transport)
	if c.Timeout != witnessTimeout || tr.Proxy != nil || c.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Errorf("client %+v", c)
	}
	if WitnessAskURL != "http://narcissus:8203/v1/witness" || !strings.Contains(WitnessAskedAs(), "forge/ci") || !strings.HasSuffix(WitnessAskedAs(), WitnessAskURL) {
		t.Errorf("%s / %s", WitnessAskURL, WitnessAskedAs())
	}
}
