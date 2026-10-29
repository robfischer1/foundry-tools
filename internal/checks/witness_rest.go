package checks

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// WitnessRESTURL is narcissus's attributed openapi-http door (rest-mesh P4,
// wonka68-rm2). fleet:witness asks here, and only here: POST /v1/witness with
// the verb's arguments as the body, in plaintext from the lane pod. Cilium L7 by
// ServiceAccount is the authz: flux grants-narcissus admits forge/ci on
// /v1/witness and the REST protocol routes, stamping X-Forge-Peer forge-ci, and
// narcissus (attributed) reads the caller from that stamp. Who asked, per run,
// rides the request's own `caller` argument (ci:gate:<star>@HEAD). A bare name:
// it resolves through the client's search path in any namespace (forge's
// prime-aliases ExternalName), see checks.GoProxy.
const WitnessRESTURL = "http://narcissus:8203"

// WitnessAskURL is the witness verb's route on that door.
const WitnessAskURL = WitnessRESTURL + "/v1/witness"

// ErrWitnessNoAnswer marks an ask that WENT OUT and drew no answer in time
// (deadline, or an answer cut short). It is never retried: the witness is slow,
// and a repeat only doubles its load.
var ErrWitnessNoAnswer = errors.New("the witness did not answer the ask in time")

// WitnessAskedAs is the atom's identity line: the class it asked as, and where.
func WitnessAskedAs() string {
	return "identity: asked as forge/ci (Cilium L7 by ServiceAccount, stamped forge-ci) at " + WitnessAskURL
}

// WitnessClient is the plaintext client an ask goes out on: never through an
// environment proxy (a relayed call would reach narcissus as the proxy's
// ServiceAccount, which no grant admits), no redirect followed, and one ask
// bounded at witnessTimeout.
func WitnessClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	return &http.Client{
		Timeout:       witnessTimeout,
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// resultEnvelope is the stellar REST door's media type for a result that
// carries structured content beside its text.
const resultEnvelope = "application/vnd.stellar.result+json"

// WitnessResult reads narcissus's REST answer to one ask into the shape
// ClassifyWitness reads (an MCP tools/call result, JSON-encoded), with the status
// it should judge. A 2xx is the verb's text; a 4xx that names its star (X-Star)
// is the verb's own refusal, so it is an isError result; anything else (no
// X-Star: the proxy or nothing at all answered, or a 5xx) passes through as the
// status it was.
func WitnessResult(status int, star, contentType, body string) (int, string) {
	text := body
	if strings.EqualFold(strings.TrimSpace(strings.Split(contentType, ";")[0]), resultEnvelope) {
		var env struct {
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(body), &env) == nil {
			text = env.Text
		}
	}
	isError := false
	switch {
	case status >= 200 && status < 300:
	case status >= 400 && status < 500 && star != "":
		isError = true
	default:
		return status, body
	}
	out, _ := json.Marshal(map[string]any{
		"isError": isError,
		"content": []map[string]string{{"type": "text", "text": text}},
	})
	return http.StatusOK, string(out)
}
