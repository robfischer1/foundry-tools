// Package hadescall asks hades one verb over mTLS, as the SVID the SPIRE
// agent's socket issues. A lane function execs the hadescall binary (its thin
// main, at the module's hadescall/) with the socket the calling pod forwarded.
//
// THE SOCKET IS THE CALLER'S IDENTITY. A Socket argument is dialed by the
// dagger CLI that called the function, so the agent attests the pod the CLI
// runs in. Measured 2026-09-14: a lane-shaped pod under the ca-gate account
// fetched spiffe://notusmi.com/ci/default/ca-gate through it, and hades
// answered 403 naming that id. The key stays in this process's memory; a
// fetched key file would have been a layer in the engine's cache.
//
// That probe ran spire-agent as root in its own image. A forwarded socket is
// root-owned inside the exec, so a nonroot hadescall needs the socket owned by
// its user (build.go's hadesCaller). Without that the connect is refused, and
// reach names the refusal instead of letting the identity wait time out.
//
// THE LOGIC LIVES HERE, NOT IN THE BINARY'S main, so this package's tests are
// the ones a mutant of it runs against. gremlins v0.6.0 names a mutant's
// package by walking up its directory for a name ending in the package clause
// (engine.go pkgName); a `package main` under hadescall/ finds none and is
// tested against the module root, which never runs it (foundry-tools#53's
// mutation lane: every hadescall mutant LIVED or NOT COVERED).
package hadescall

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

const usage = `usage: hadescall <verb> <json-args>

Reads HADESCALL_SOCKET (unix:///run/spire/agent.sock), HADESCALL_HADES
(https://hades.default.svc.cluster.local:8102), HADESCALL_HADES_ID
(spiffe://notusmi.com/star/hades) and HADESCALL_IDENTITY_WAIT (120s).`

// Identity is what a call needs from the agent: the SVID it presents, and the
// bundle it verifies hades against.
type Identity interface {
	x509svid.Source
	x509bundle.Source
	Close() error
}

// Open fetches the identity at the socket, waiting for the first SVID until
// ctx ends. A variable so a test can hand over an identity without an agent.
var Open = func(ctx context.Context, socket string) (Identity, error) {
	return workloadapi.NewX509Source(ctx, workloadapi.WithClientOptions(workloadapi.WithAddr(socket)))
}

// reach answers whether this process can connect to a unix socket at all,
// before the identity wait begins. go-spiffe retries a refused connect
// silently until its deadline, so a socket this process may not open costs
// the whole wait and names no cause: a root-owned forward read by a nonroot
// exec, athena b66d46f on 2026-09-14, twice. A unix connect succeeds or fails
// at once. Any other address is left to the client.
func reach(socket string) error {
	path, ok := strings.CutPrefix(socket, "unix://")
	if !ok {
		return nil
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		return err
	}
	return conn.Close()
}

// config is everything a call reads from its environment, with the fleet's
// defaults.
type config struct {
	socket, hades string
	hadesID       spiffeid.ID
	wait          time.Duration
}

func configOf(env func(string) string) (config, error) {
	c := config{
		socket: or(env("HADESCALL_SOCKET"), "unix:///run/spire/agent.sock"),
		hades:  strings.TrimRight(or(env("HADESCALL_HADES"), "https://hades.default.svc.cluster.local:8102"), "/"),
		wait:   120 * time.Second,
	}
	id, err := spiffeid.FromString(or(env("HADESCALL_HADES_ID"), "spiffe://notusmi.com/star/hades"))
	if err != nil {
		return c, fmt.Errorf("HADESCALL_HADES_ID: %w", err)
	}
	c.hadesID = id
	if w := env("HADESCALL_IDENTITY_WAIT"); w != "" {
		if c.wait, err = time.ParseDuration(w); err != nil {
			return c, fmt.Errorf("HADESCALL_IDENTITY_WAIT: %w", err)
		}
	}
	return c, nil
}

// Run is the binary: it prints "HTTP <status>" and then the body, and answers
// 0 once hades answered anything — what the answer means is the caller's to
// decide — and 2 when it could not ask.
func Run(ctx context.Context, args []string, env func(string) string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	verb, body := args[0], args[1]
	if !json.Valid([]byte(body)) {
		fmt.Fprintf(stderr, "hadescall %s: the arguments are not JSON: %s\n", verb, body)
		return 2
	}
	// HADESCALL_SVID_FIELD names a top-level field the call's own SVID is
	// written into before sending: a gate receipt names the identity that
	// delivered it, which only this process knows. Refused before dialing
	// when there is no object to write it into.
	svidField := env("HADESCALL_SVID_FIELD")
	var fields map[string]any
	if svidField != "" {
		if err := json.Unmarshal([]byte(body), &fields); err != nil || fields == nil {
			fmt.Fprintf(stderr, "hadescall %s: HADESCALL_SVID_FIELD=%s needs a JSON object to write the SVID into\n", verb, svidField)
			return 2
		}
	}
	cfg, err := configOf(env)
	if err != nil {
		fmt.Fprintf(stderr, "hadescall: %v\n", err)
		return 2
	}
	if err := reach(cfg.socket); err != nil {
		fmt.Fprintf(stderr, "hadescall %s: cannot connect to %s: %v — nothing was asked; a socket this process cannot open is not an agent with no identity for it\n", verb, cfg.socket, err)
		return 2
	}

	// A pod's SVID can lag the pod by seconds: the source waits for the first
	// one until the deadline. An entry that never arrives is a could-not-run
	// that names the socket, not a finding.
	wctx, cancel := context.WithTimeout(ctx, cfg.wait)
	defer cancel()
	id, err := Open(wctx, cfg.socket)
	if err != nil {
		fmt.Fprintf(stderr, "hadescall %s: no identity from %s within %s: %v\n", verb, cfg.socket, cfg.wait, err)
		return 2
	}
	defer id.Close()
	svid, err := id.GetX509SVID()
	if err != nil {
		fmt.Fprintf(stderr, "hadescall %s: the source holds no SVID: %v\n", verb, err)
		return 2
	}
	fmt.Fprintf(stderr, "hadescall: acting as %s, asking %s for %s\n", svid.ID, cfg.hadesID, verb)
	if svidField != "" {
		// A map decoded from valid JSON always re-encodes.
		fields[svidField] = svid.ID.String()
		stamped, _ := json.Marshal(fields)
		body = string(stamped)
	}
	return ask(ctx, clientFor(id, cfg.hadesID), cfg.hades+"/v1/call/"+verb, verb, body, stdout, stderr)
}

// clientFor is the mTLS client: this identity's SVID presented, and hades
// verified against the bundle and authorized by its SPIFFE id alone. Three
// minutes end to end, because hades answers a durable produce.
func clientFor(id Identity, hadesID spiffeid.ID) *http.Client {
	return &http.Client{
		Timeout: 180 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsconfig.MTLSClientConfig(id, id, tlsconfig.AuthorizeID(hadesID)),
		},
	}
}

// ask posts the verb's arguments and prints what came back.
func ask(ctx context.Context, client *http.Client, url, verb, body string, stdout, stderr io.Writer) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		fmt.Fprintf(stderr, "hadescall %s: %v\n", verb, err)
		return 2
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "hadescall %s: hades did not answer: %v\n", verb, err)
		return 2
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(stderr, "hadescall %s: the answer did not read: %v\n", verb, err)
		return 2
	}
	fmt.Fprintf(stdout, "HTTP %d\n%s", resp.StatusCode, raw)
	return 0
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
