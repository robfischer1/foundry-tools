package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// THE WITNESS IS ASKED ONLY WHEN IT CAN ANSWER. On 2026-10-14 narcissus shipped
// a migration that rolled back and left its store down; a revert PR for it could
// not clear the gate, because fleet:witness asked the star it was trying to fix
// (foundry-tools#18242). Every ask came back an error row, which settled 2 per
// file after the retries and the 120 s ask timeout, and a gate that cannot tell
// "the witness is down" from "the witness could not read this file" gives a
// reviewer nothing to act on.
//
// So the atom asks narcissus's /ready first (the op-contract every star serves;
// 503 until the store answers, since narcissus#readiness-follows-the-store).
// A witness that does not say ready is a COULD-NOT-RUN about the target, named
// as such, with no ask sent: state 2, never a finding, never a pass. That is the
// state the gate's own ladder already routes to a manual merge, which is how a
// revert of the tip lands while the tip is what is down.

// WitnessReadyAttempts and WitnessReadyPause bound the probe: a blip shorter
// than a few seconds is not an outage, so an unhealthy answer is asked again
// before the atom gives up on the witness.
const (
	WitnessReadyAttempts = 3
	WitnessReadyPause    = time.Duration(3e9) // 3 s, spelled with no operator a mutant could flip
)

// WitnessReadyTimeout bounds one probe: /ready answers from memory.
const WitnessReadyTimeout = time.Duration(10e9)

// WitnessReadyURL is the readiness endpoint beside an MCP endpoint: same host,
// path /ready. "" when the URL does not parse.
func WitnessReadyURL(mcpURL string) string {
	u, err := url.Parse(mcpURL)
	if err != nil || u.Host == "" {
		return ""
	}
	u.Path, u.RawQuery, u.Fragment = "/ready", "", ""
	return u.String()
}

// WitnessProbe is one GET of the readiness endpoint: status and body, or the
// error that kept it from asking.
type WitnessProbe func(ctx context.Context, url string) (int, string, error)

// WitnessReadiness asks whether the witness can answer. ok is true when it said
// ready, and ALSO when the probe could not read an answer it understands (a 404
// from a star with no /ready, a 4xx): a probe the atom cannot interpret must
// not become a new way to block a gate, so only an unreachable star or a 5xx
// readiness answer (503 is the op-contract's "not ready") is unhealthy. detail
// says why, for the verdict.
func WitnessReadiness(ctx context.Context, probe WitnessProbe, mcpURL string, attempts int, pause time.Duration, sleep func(context.Context, time.Duration) error) (ok bool, detail string) {
	u := WitnessReadyURL(mcpURL)
	if u == "" {
		return true, ""
	}
	for attempt := 1; ; attempt++ {
		status, body, err := probe(ctx, u)
		switch {
		case err != nil:
			detail = "unreachable: " + err.Error()
		case status >= 500:
			detail = fmt.Sprintf("HTTP %d%s", status, readyOrgans(body))
		default:
			return true, ""
		}
		if attempt >= attempts {
			return false, detail
		}
		if serr := sleep(ctx, pause); serr != nil {
			return false, detail + " (" + serr.Error() + ")"
		}
	}
}

// readyOrgans is the part of a /ready body that says which organ is down: the
// store's word, when the body is the op-contract's JSON.
func readyOrgans(body string) string {
	var b struct {
		Status string `json:"status"`
		Store  string `json:"store"`
	}
	if json.Unmarshal([]byte(body), &b) != nil {
		return ""
	}
	var parts []string
	if b.Status != "" {
		parts = append(parts, "status="+b.Status)
	}
	if b.Store != "" {
		parts = append(parts, "store="+b.Store)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, " ") + ")"
}

// WitnessNotReadyReason is the verdict's reason for a witness that would not say
// ready: a could-not-run about the target, not a finding about the change.
func WitnessNotReadyReason(detail string) string {
	return "CANNOT RUN - the witness (narcissus) is not ready: " + detail +
		". No file was asked. This is the target being down, not a finding in the change; " +
		"a change that repairs narcissus lands by the ladder's manual merge."
}
