package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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

// postRecord hands the stage's record to the door and SAYS what it got.
//
// EVERY FAILURE IS A LINE, NOT AN ERROR, and the caller ignores the answer. A
// lane's verdict must never turn on whether an HTTP request succeeded: the file
// is written either way and ourea falls back to it. Returning an error here
// would invite a caller to fail a run over its own telemetry.
//
// BUT A SILENT NIL IS HOW A DEAD ROUTE HID FOR 2 h 37 m. On 2026-10-03 the
// door's /ci/record relay was deleted and every lane in the fleet posted into a
// 404 and settled no_record with nothing in any log saying why. So the outcome
// is printed — one line, on the lane's stderr, near the end of its log where
// Daedalus's log_tail and repo_ci_logs show it — and still decides nothing.
func postRecord(ctx context.Context, repo string, token *dagger.Secret, record string) {
	// A TYPED NIL IS NOT A NIL INTERFACE, so the nil check happens HERE, on the
	// concrete type, before the value is ever widened. A (*dagger.Secret)(nil)
	// assigned to plaintexter is a non-nil interface holding a nil pointer, and
	// calling through it panics — which is the common path, since every lane
	// runs with no token until its call declares one.
	var secret plaintexter
	if token != nil {
		secret = token
	}
	_, _ = fmt.Fprintln(recordPostOut, "record post: "+recordPostOutcome(ctx, repo, secret, record))
}

// recordPostOut is where postRecord's one line goes: the lane's stderr, the
// same writer sayLine uses. A variable so a test can read the line back.
var recordPostOut io.Writer = os.Stderr

// recordPostOutcome is postRecord without the printing: what the post got, as
// the line a reader of the lane's log needs. A nil token is a lane whose call
// declared none.
func recordPostOutcome(ctx context.Context, repo string, token plaintexter, record string) string {
	if token == nil {
		return "not posted: no record token"
	}
	return postRecordWith(ctx, repo, token, record)
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
func postRecordWith(ctx context.Context, repo string, token plaintexter, record string) string {
	endpoint, ok := recordEndpoint(repo)
	if !ok {
		return "not posted: no door to post to (the run was built with no repo URL)"
	}
	secret, err := token.Plaintext(ctx)
	if err != nil {
		return fmt.Sprintf("not posted: the record token could not be read: %v", err)
	}
	return sendRecord(ctx, endpoint, secret, record)
}

// sendRecord makes the request and answers what it got. It is postRecord
// without the engine.
//
// THE TOKEN IS NEVER IN THE ANSWER. The line carries the endpoint (scheme and
// host only — recordEndpoint drops any userinfo), the status and at most one
// line of the door's reply with the secret scrubbed out of it.
func sendRecord(ctx context.Context, endpoint, secret, record string) string {
	if secret == "" {
		return "not posted: the record token is empty"
	}
	if record == "" {
		return "not posted: the record is empty"
	}
	ctx, cancel := context.WithTimeout(ctx, recordPostTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(record))
	if err != nil {
		return fmt.Sprintf("not posted: no request could be built for %q: %v", endpoint, err)
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("POST %s → could not reach the door: %v", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	// READ AT MOST recordReplyDrain, NEVER MORE. The reply is a status and at
	// most a sentence; reading it to the limit also returns the connection to
	// the pool, and a door answering something enormous cannot make a lane
	// read it.
	reply, _ := io.ReadAll(io.LimitReader(resp.Body, recordReplyDrain))
	return postAnswer(endpoint, resp.StatusCode, scrub(string(reply), secret))
}

// postAnswer is the line for a post the door answered.
func postAnswer(endpoint string, status int, reply string) string {
	line := fmt.Sprintf("POST %s → %d", endpoint, status)
	if why, ok := postStatusMeans[status]; ok {
		line += " (" + why + ")"
	}
	if status/100 != 2 {
		if first := firstLine(reply); first != "" {
			line += ": " + first
		}
	}
	return line
}

// postStatusMeans names the answers whose cause is known, so the line says
// where to look rather than just a number.
var postStatusMeans = map[int]string{
	http.StatusUnauthorized: "Daedalus refused this run's record token",
	http.StatusNotFound:     "the door serves no " + recordPostPath,
	http.StatusBadGateway:   "the door could not reach Daedalus",
}

// firstLine is the reply's first line, trimmed and capped at replyLineCap
// bytes: enough for a refusal's reason, never a page of HTML.
func firstLine(reply string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(reply), "\n")
	line = strings.TrimSpace(line)
	if len(line) > replyLineCap {
		line = line[:replyLineCap] + "…"
	}
	return line
}

// replyLineCap bounds how much of the door's reply reaches the log.
const replyLineCap = 200

// scrub takes the secret out of anything the door said, in case a reply ever
// echoes the header back. The token never reaches a log.
func scrub(reply, secret string) string {
	return strings.ReplaceAll(reply, secret, "<token>")
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
