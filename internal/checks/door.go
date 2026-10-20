package checks

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// OureaDoor is the git door, and the one place a contract's bytes are read
// from. It replaced forgejo.notusmi.com's raw API on 2026-10-04 (Rob: "If it
// currently points at Forgejo, point it at Ourea"); Forgejo is being retired
// and a gate that reads its raw API goes red the day it goes.
const OureaDoor = "https://git.notusmi.com"

// DoorTimeout bounds one read. The same thirty seconds the script it replaced
// allowed.
const DoorTimeout = 30 * time.Second

// Door reads one file at the door's default ref over its anonymous archive
// endpoint: GET <Base>/archive?repo=<owner/name>&path=<tree path>. The door
// answers the blob's bytes and nothing else — no JSON envelope, no base64 —
// 404 for a repo or path it does not hold, 400 for an ambiguous bare name and
// 503 for a store that is not ready. It is read-open; no credential is sent.
type Door struct {
	// Base is the scheme and host, no trailing slash.
	Base string
	// Client makes the request; nil means a client bounded by DoorTimeout.
	Client *http.Client
	// NoRetry asks once and answers whatever came back. The zero value retries a
	// transient failure (DoorWaits); a test of the failure itself sets this.
	NoRetry bool
}

// DoorWaits are the waits between a read's attempts: 1, 2, 4 and 8 s, each moved
// up to DoorJitter either way — the schedule daedalus#394 and foundry-tools#535
// use. One transient answer from the door (a resolver a beat late, a pod
// restarting) is not a verdict about the file, and settled a dies atom
// CANNOT RUN (blade-runner 7bb21c6, 09:40Z).
var DoorWaits = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}

// DoorJitter is the fraction either way a wait moves.
const DoorJitter = 0.3

// The seams a test replaces: how a wait sleeps and the draw behind its jitter.
var (
	doorSleep = func(ctx context.Context, d time.Duration) {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
		}
	}
	doorRand = rand.Float64
)

// transientDoor says whether a read's outcome is worth asking again: a network
// error (DNS, refused or reset, a timeout, a body cut short) or a 5xx. A 404, a
// 400 or any other status is the door's answer and is final.
func transientDoor(status int, err error) bool {
	if err == nil {
		return status >= 500
	}
	var dns *net.DNSError
	var nerr net.Error
	switch {
	case errors.As(err, &dns), errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.ECONNRESET),
		errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF), errors.Is(err, context.DeadlineExceeded):
		return true
	case errors.As(err, &nerr):
		return nerr.Timeout()
	}
	return false
}

// NewDoor is the production door.
func NewDoor() Door { return Door{Base: OureaDoor} }

// URL is the address of one file. The slashes in the query stay literal, which
// the query grammar allows and a reader of a verdict can read.
func (d Door) URL(repo, path string) string {
	q := "repo=" + url.QueryEscape(repo) + "&path=" + url.QueryEscape(path)
	return strings.TrimRight(d.Base, "/") + "/archive?" + strings.ReplaceAll(q, "%2F", "/")
}

// Get answers the HTTP status and body of one file, or the error that kept it
// from asking. Redirects are followed. A status is returned whatever it is: the
// caller decides what a 404 means, because it differs per atom. A transient
// failure (transientDoor) is asked again after each of DoorWaits, and the last
// answer stands; the caller's own cancelled context ends the asking at once.
func (d Door) Get(ctx context.Context, repo, path string) (int, []byte, error) {
	status, body, err := d.get(ctx, repo, path)
	for _, w := range DoorWaits {
		if d.NoRetry || ctx.Err() != nil || !transientDoor(status, err) {
			break
		}
		doorSleep(ctx, time.Duration(float64(w)*(1+DoorJitter*(2*doorRand()-1))))
		status, body, err = d.get(ctx, repo, path)
	}
	return status, body, err
}

// get is one ask.
func (d Door) get(ctx context.Context, repo, path string) (int, []byte, error) {
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: DoorTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.URL(repo, path), nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}
