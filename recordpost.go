package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"dagger/foundry-tools/internal/dagger"
)

// The record's third transport: the stage POSTS it to the door.
//
// WHY, when RecordFile already exists. The file path costs a PVC minted per
// run, a collect Job to mount it and chunk framing to get the bytes back out —
// three moving parts and a replicated block device to move a median 8.9 KB of
// JSON. This is the same bytes over a connection the run already has: the door
// it fetched its own tree from.
//
// BOTH, FOR NOW. The file is still written and still exported. A post that
// fails, or a door that is not listening, leaves the volume exactly as it was,
// and ourea prefers whichever arrives. Deleting the file path is a later change
// with its own evidence.
//
// THE DOOR IS DERIVED, NOT DECLARED, and that is a property rather than a
// shortcut. The endpoint is built from the repo URL the run was constructed
// with, so a lane can only ever post to the door that handed it the tree. A
// separate --record-post flag would be one more thing to misconfigure, and its
// failure — posting a real record at the wrong address — would be silent.

// recordPostPath is ourea's endpoint. It is stated in both repositories because
// it is a contract between them and neither imports the other; ourea says so at
// gatejob.RecordPostPath.
const recordPostPath = "/ci/record"

// recordPostTimeout bounds one post. The record is small and the door is in the
// same cluster; a door that cannot take it in fifteen seconds is a door this
// run will not wait on, because the file it already wrote is the fallback.
const recordPostTimeout = 15 * time.Second

// postRecord hands the stage's record to the door.
//
// EVERY FAILURE IS A NIL, NOT AN ERROR, and the caller ignores the answer. A
// lane's verdict must never turn on whether an HTTP request succeeded: the file
// is written either way and ourea falls back to it. Returning an error here
// would invite a caller to fail a run over its own telemetry.
func postRecord(ctx context.Context, repo string, token *dagger.Secret, record string) {
	// A TYPED NIL IS NOT A NIL INTERFACE, so the nil check happens HERE, on the
	// concrete type, before the value is ever widened. A (*dagger.Secret)(nil)
	// assigned to plaintexter is a non-nil interface holding a nil pointer, and
	// calling through it panics — which is the common path, since every lane
	// runs with no token until its call declares one.
	if token == nil {
		return
	}
	postRecordWith(ctx, repo, token, record)
}

// plaintexter is the one thing postRecord needs a live engine for.
//
// AN INTERFACE SO THE ERROR ARM IS REACHABLE. *dagger.Secret satisfies it, and
// a test can hand over one that refuses — without which nothing executes the
// failure path at all, which the mutation lane said in exactly those words
// (recordpost.go NOT COVERED, 2026-09-28). Reading a Secret is a session call;
// everything after it is ordinary net/http.
type plaintexter interface {
	Plaintext(context.Context) (string, error)
}

// postRecordWith is postRecord once the token is known to be there.
func postRecordWith(ctx context.Context, repo string, token plaintexter, record string) {
	endpoint, ok := recordEndpoint(repo)
	if !ok {
		return
	}
	secret, err := token.Plaintext(ctx)
	if err != nil {
		return
	}
	sendRecord(ctx, endpoint, secret, record)
}

// sendRecord makes the request. It is postRecord without the engine.
//
// EVERY REFUSAL IS A RETURN, NOT AN ERROR, and the caller ignores the answer. A
// lane's verdict must never turn on whether an HTTP request succeeded: the file
// is written either way and ourea falls back to it. Returning an error here
// would invite a caller to fail a run over its own telemetry.
func sendRecord(ctx context.Context, endpoint, secret, record string) {
	if secret == "" || record == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, recordPostTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(record))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	// DRAINED, NOT READ. The answer carries nothing this caller acts on — the
	// file is the fallback whatever the status — but a body left unread holds
	// the connection out of the pool for no reason.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, recordReplyDrain))
}

// recordReplyDrain is how much of the door's answer is read before the body is
// closed. The answer is a status and at most a sentence; this is enough to let
// the connection go back to the pool and small enough that a door answering
// something enormous cannot make a lane read it.
const recordReplyDrain = 4096

// recordEndpoint turns the repo URL a run was constructed with into the door's
// record endpoint: scheme and host, then the path.
//
// A LOCAL RUN POSTS NOTHING, which is the second reason to derive rather than
// declare. `dagger check` from a developer's machine constructs the module with
// no repo at all, so there is no door, nothing to post to, and no configuration
// anyone has to remember to leave unset.
func recordEndpoint(repo string) (string, bool) {
	u, err := url.Parse(repo)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}
	return fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, recordPostPath), true
}
