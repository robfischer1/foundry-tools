// Package svidcred is the registry login of CI workload identity v2: a docker
// credential helper that answers a JWT-SVID, fetched from the SPIRE agent's
// socket at the moment a tool asks, as the registry password.
//
// WHY A HELPER AND NOT A CONFIG FILE. A JWT-SVID lives 15 minutes. A docker
// config written at the start of a lane is a layer in the engine's cache and is
// expired by the time a long push reaches the registry. A credential helper is
// asked by oras and cosign each time they authenticate, so the token is fetched
// right before the request, held only in this process's memory and the tool's,
// and never written anywhere: the config the tools read names the helper and
// holds no credential at all.
//
// THE SOCKET MUST BE OPENABLE BY THIS PROCESS. See internal/hadescall for the
// measurement: a forwarded socket is root-owned, and go-spiffe retries a
// refused connect silently until its deadline. reach fails at once instead.
package svidcred

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

// Helper is the helper's name in a docker config: `docker-credential-` + Helper
// is the binary the tool looks up on its PATH.
const Helper = "svid"

// Binary is the file name the helper is installed under.
const Binary = "docker-credential-" + Helper

const (
	// EnvSocket is the agent's socket, as a go-spiffe address.
	EnvSocket = "SVIDCRED_SOCKET"
	// EnvAudience is the audience the registry validates.
	EnvAudience = "SVIDCRED_AUDIENCE"
	envWait     = "SVIDCRED_WAIT"
)

// Config is the docker config that sends every login to host to the helper.
// It carries no credential and is safe to cache or log.
func Config(host string) string {
	b, _ := json.Marshal(map[string]any{"credHelpers": map[string]string{host: Helper}})
	return string(b)
}

// credential is the docker credential-helper protocol's answer to `get`.
type credential struct {
	ServerURL string
	Username  string
	Secret    string
}

// Parse reads a `get` answer into the login it carries.
func Parse(out string) (user, secret string, err error) {
	var c credential
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		return "", "", fmt.Errorf("the credential helper's answer is not JSON")
	}
	if c.Username == "" || c.Secret == "" {
		return "", "", fmt.Errorf("the credential helper answered no username and secret")
	}
	return c.Username, c.Secret, nil
}

// Fetch asks the agent at socket for a JWT-SVID for audience and answers the
// identity it names (the token's sub) and the token. A variable so a test can
// hand over a token without an agent.
var Fetch = func(ctx context.Context, socket, audience string) (id, token string, err error) {
	svid, err := workloadapi.FetchJWTSVID(ctx, jwtsvid.Params{Audience: audience}, workloadapi.WithAddr(socket))
	if err != nil {
		return "", "", err
	}
	return svid.ID.String(), svid.Marshal(), nil
}

// reach answers whether this process can open a unix socket at all.
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

// Run is the helper. `get` reads the registry host on stdin and answers its
// login as JSON on stdout; list, store and erase succeed and do nothing, since
// there is nothing stored. The token is never written to stderr.
func Run(ctx context.Context, args []string, env func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: docker-credential-svid get|list|store|erase")
		return 2
	}
	switch args[0] {
	case "list":
		fmt.Fprintln(stdout, "{}")
		return 0
	case "store", "erase":
		return 0
	case "get":
	default:
		fmt.Fprintf(stderr, "docker-credential-svid: unknown command %q\n", args[0])
		return 2
	}
	raw, _ := io.ReadAll(stdin)
	server := strings.TrimSpace(string(raw))
	socket, audience := env(EnvSocket), env(EnvAudience)
	if socket == "" {
		socket = "unix:///run/spire/agent.sock"
	}
	if audience == "" {
		fmt.Fprintf(stderr, "docker-credential-svid: %s is not set; there is no audience to ask for\n", EnvAudience)
		return 1
	}
	wait := 30 * time.Second
	if w := env(envWait); w != "" {
		d, err := time.ParseDuration(w)
		if err != nil {
			fmt.Fprintf(stderr, "docker-credential-svid: %s: %v\n", envWait, err)
			return 1
		}
		wait = d
	}
	if err := reach(socket); err != nil {
		fmt.Fprintf(stderr, "docker-credential-svid: cannot connect to %s: %v\n", socket, err)
		return 1
	}
	wctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	id, token, err := Fetch(wctx, socket, audience)
	if err != nil {
		fmt.Fprintf(stderr, "docker-credential-svid: no JWT-SVID for audience %s from %s within %s: %v\n", audience, socket, wait, err)
		return 1
	}
	// Marshal cannot fail on a struct of strings.
	out, _ := json.Marshal(credential{ServerURL: server, Username: id, Secret: token})
	fmt.Fprintln(stdout, string(out))
	return 0
}
