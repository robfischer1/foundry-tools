package checks

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OureaDoor is the git door, and the one place a contract's bytes are read
// from. It replaced forgejo.notusmi.com's raw API on 2026-10-04 (Rob: "If it
// currently points at Forgejo, point it at Ourea"); Forgejo is being retired
// and a gate that reads its raw API goes red the day it goes.
const OureaDoor = "https://git.notusmi.com"

// OrbitsRepo is the repository the canonical seam contracts live in, written
// owner-qualified: a bare name that two owners hold is refused by the door
// (400 and the candidates), and a refusal is not an answer about a contract.
const OrbitsRepo = "foundry/foundry-dies"

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
// caller decides what a 404 means, because it differs per atom.
func (d Door) Get(ctx context.Context, repo, path string) (int, []byte, error) {
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
