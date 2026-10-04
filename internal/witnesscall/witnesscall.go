// Package witnesscall asks the code witness (narcissus) over its mTLS door,
// as the SVID the SPIRE agent's forwarded socket issues to the CI lane:
// spiffe://notusmi.com/job/<lane>/<job> (flux identity/spire-clusterspiffeid-ci-jobs.yaml).
//
// WHY AN EXEC AND NOT THE MODULE. fleet:witness used to POST from the
// module's own Go process. That process runs in the dagger ENGINE, and so did
// the dial: measured 2026-10-04, narcissus saw the engine's pod IP and no
// certificate, so every gate's witness read `unidentified`. A module cannot
// dial a forwarded *dagger.Socket itself; a container can mount it. So the
// atom execs this binary with the lane pod's socket mounted, and the agent
// attests the lane pod (the dagger CLI that forwarded it), exactly as
// hadescall does for the cast lane.
//
// THE KEY STAYS IN THIS PROCESS'S MEMORY. No certificate or key is written,
// exported or passed as a secret.
//
// Exit codes: 0 narcissus answered (whatever it said), 2 could not ask,
// 3 no identity — the caller asks in the clear on anything but 0.
package witnesscall

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"

	"dagger/foundry-tools/internal/hadescall"
)

const usage = `usage: witnesscall whoami
       witnesscall post <url> <request-file>

Reads WITNESSCALL_SOCKET (unix:///run/spire/agent.sock),
WITNESSCALL_SERVER_ID (spiffe://notusmi.com/star/narcissus) and
WITNESSCALL_IDENTITY_WAIT (20s).`

// The exit codes the atom reads.
const (
	Answered    = 0
	CouldNotAsk = 2
	NoIdentity  = 3
)

// config is what a call reads from its environment, with the fleet's defaults.
type config struct {
	socket   string
	serverID spiffeid.ID
	wait     time.Duration
}

func configOf(env func(string) string) (config, error) {
	c := config{socket: or(env("WITNESSCALL_SOCKET"), "unix:///run/spire/agent.sock"), wait: 20 * time.Second}
	id, err := spiffeid.FromString(or(env("WITNESSCALL_SERVER_ID"), "spiffe://notusmi.com/star/narcissus"))
	if err != nil {
		return c, fmt.Errorf("WITNESSCALL_SERVER_ID: %w", err)
	}
	c.serverID = id
	if w := env("WITNESSCALL_IDENTITY_WAIT"); w != "" {
		if c.wait, err = time.ParseDuration(w); err != nil {
			return c, fmt.Errorf("WITNESSCALL_IDENTITY_WAIT: %w", err)
		}
	}
	return c, nil
}

func or(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// wellFormed reports whether args is one of the two calls.
func wellFormed(args []string) bool {
	return len(args) == 1 && args[0] == "whoami" || len(args) == 3 && args[0] == "post"
}

// Run is the binary. whoami prints the SVID's id; post sends the request
// file's bytes to the url and prints "HTTP <status>", the content type, then
// the body, one per line.
func Run(ctx context.Context, args []string, env func(string) string, readFile func(string) ([]byte, error), stdout, stderr io.Writer) int {
	if !wellFormed(args) {
		fmt.Fprintln(stderr, usage)
		return CouldNotAsk
	}
	cfg, err := configOf(env)
	if err != nil {
		fmt.Fprintf(stderr, "witnesscall: %v\n", err)
		return CouldNotAsk
	}
	wctx, cancel := context.WithTimeout(ctx, cfg.wait)
	defer cancel()
	id, err := hadescall.Open(wctx, cfg.socket)
	if err != nil {
		fmt.Fprintf(stderr, "witnesscall: no identity from %s within %s: %v\n", cfg.socket, cfg.wait, err)
		return NoIdentity
	}
	defer id.Close()
	svid, err := id.GetX509SVID()
	if err != nil {
		fmt.Fprintf(stderr, "witnesscall: the source holds no SVID: %v\n", err)
		return NoIdentity
	}
	if args[0] == "whoami" {
		fmt.Fprintln(stdout, svid.ID)
		return Answered
	}
	body, err := readFile(args[2])
	if err != nil {
		fmt.Fprintf(stderr, "witnesscall: %v\n", err)
		return CouldNotAsk
	}
	return post(ctx, clientFor(id, cfg.serverID), args[1], body, stdout, stderr)
}

// clientFor presents this identity's SVID and verifies the server by its
// SPIFFE id alone. A witness answer takes 30-40 s; two minutes bounds it.
func clientFor(id hadescall.Identity, serverID spiffeid.ID) *http.Client {
	return &http.Client{
		Timeout:   120 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tlsconfig.MTLSClientConfig(id, id, tlsconfig.AuthorizeID(serverID))},
	}
}

// post sends one MCP request and prints what came back.
func post(ctx context.Context, client *http.Client, url string, body []byte, stdout, stderr io.Writer) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(stderr, "witnesscall: %v\n", err)
		return CouldNotAsk
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "witnesscall: the witness did not answer: %v\n", err)
		return CouldNotAsk
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(stderr, "witnesscall: the answer was cut short: %v\n", err)
		return CouldNotAsk
	}
	fmt.Fprintf(stdout, "HTTP %d\n%s\n%s", resp.StatusCode, resp.Header.Get("Content-Type"), raw)
	return Answered
}
