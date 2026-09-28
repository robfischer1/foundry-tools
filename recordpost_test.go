package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// THE DOOR IS DERIVED FROM THE TREE'S ORIGIN, which is the property that makes
// a separate --record-post flag unnecessary: a lane can only post to the door
// that handed it the tree, so a misconfigured endpoint is not a shape that
// exists.
func TestTheRecordEndpointIsTheDoorTheTreeCameFrom(t *testing.T) {
	for _, c := range []struct{ name, repo, want string }{
		{"in-cluster door", "http://ourea:8215/rob/infra.git", "http://ourea:8215/ci/record"},
		{"tls door", "https://git.example.com/rob/infra.git", "https://git.example.com/ci/record"},
		{"door with a port", "http://door:1234/o/r.git", "http://door:1234/ci/record"},
		{"deep path is discarded", "http://d/a/b/c/r.git", "http://d/ci/record"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := recordEndpoint(c.repo)
			if !ok {
				t.Fatal("a real repo URL must yield an endpoint, got ok=false")
			}
			if got != c.want {
				t.Fatalf("endpoint\n want %q\n  got %q", c.want, got)
			}
		})
	}
}

// A LOCAL RUN POSTS NOTHING. `dagger check` from a developer's machine builds
// the module with no repo, so there is no door — and nothing anyone has to
// remember to leave unset.
func TestNoRepoMeansNoEndpoint(t *testing.T) {
	for _, repo := range []string{"", "not a url", "/just/a/path", "relative.git"} {
		if _, ok := recordEndpoint(repo); ok {
			t.Fatalf("%q must not yield an endpoint", repo)
		}
	}
}

// THE PATH IS THE ONE OUREA SERVES. It is stated in both repositories because
// neither imports the other; if this constant drifts, every posted record 404s
// and every run silently falls back to its volume.
func TestTheRecordPathMatchesTheDoorsContract(t *testing.T) {
	if recordPostPath != "/ci/record" {
		t.Fatalf("the door serves /ci/record (gatejob.RecordPostPath); this says %q", recordPostPath)
	}
}

// takenBy stands the door up and reports what actually arrived.
type takenBy struct {
	hits   int
	method string
	auth   string
	ctype  string
	body   string
}

func doorFor(t *testing.T, status int) (*httptest.Server, *takenBy) {
	t.Helper()
	got := &takenBy{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.hits++
		got.method, got.auth = r.Method, r.Header.Get("Authorization")
		got.ctype, got.body = r.Header.Get("Content-Type"), string(b)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// THE REQUEST THE DOOR ACTUALLY RECEIVES. Everything ourea's handler keys on is
// asserted here rather than assumed: the method it allows, the bearer scheme it
// parses, and the bytes it stores verbatim.
func TestTheRecordArrivesAsThePostTheDoorExpects(t *testing.T) {
	srv, got := doorFor(t, http.StatusNoContent)
	const rec = `ourea-run-record/1 {"Stage":"gate","State":0}`

	sendRecord(t.Context(), srv.URL, "tok-abc", rec)

	if got.hits != 1 {
		t.Fatalf("the door must be posted to exactly once, got %d", got.hits)
	}
	if got.method != http.MethodPost {
		t.Fatalf("the door takes POST, sent %s", got.method)
	}
	if got.auth != "Bearer tok-abc" {
		t.Fatalf("the token rides Authorization as a bearer, got %q", got.auth)
	}
	if got.ctype != "application/octet-stream" {
		t.Fatalf("content type %q", got.ctype)
	}
	if got.body != rec {
		t.Fatalf("the record must arrive byte for byte.\n want %q\n  got %q", rec, got.body)
	}
}

// THE SENTINEL TRAVELS WITH IT. ourea reads every transport through one
// FindRunRecord, and a post that stripped the sentinel would be refused as "not
// a JSON object" — which is exactly how the collect path broke every gate run
// in the fleet on 2026-09-26.
func TestThePostedRecordKeepsItsSentinel(t *testing.T) {
	srv, got := doorFor(t, http.StatusNoContent)

	sendRecord(t.Context(), srv.URL, "tok", `ourea-run-record/1 {"State":0}`)

	if !strings.HasPrefix(got.body, "ourea-run-record/1 {") {
		t.Fatalf("the sentinel must lead the posted bytes, got %q", got.body)
	}
}

// A DOOR THAT REFUSES CHANGES NOTHING. The lane wrote its file; a 401 or a 500
// is the door's problem and the volume is the fallback. This pins that the
// caller neither retries nor panics nor cares.
func TestARefusingDoorIsNotTheLanesProblem(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusRequestEntityTooLarge, http.StatusInternalServerError} {
		srv, got := doorFor(t, status)
		sendRecord(t.Context(), srv.URL, "tok", "ourea-run-record/1 {}")
		if got.hits != 1 {
			t.Fatalf("status %d: the post is made once and the answer ignored, hits=%d", status, got.hits)
		}
	}
}

// A DOOR THAT IS NOT THERE IS THE SAME. An unreachable endpoint must return
// quietly rather than fail the run.
func TestAnUnreachableDoorIsSilent(t *testing.T) {
	srv, _ := doorFor(t, http.StatusNoContent)
	dead := srv.URL
	srv.Close() // nothing is listening now

	sendRecord(t.Context(), dead, "tok", "ourea-run-record/1 {}")
}

// NOTHING IS SENT WITHOUT A SECRET OR A RECORD. Each guard is pinned on its own
// so a mutant that flips one is not covered by the other.
func TestNothingIsSentWithoutASecret(t *testing.T) {
	srv, got := doorFor(t, http.StatusNoContent)
	sendRecord(t.Context(), srv.URL, "", "ourea-run-record/1 {}")
	if got.hits != 0 {
		t.Fatalf("an empty secret must send nothing, hits=%d", got.hits)
	}
}

func TestNothingIsSentWithoutARecord(t *testing.T) {
	srv, got := doorFor(t, http.StatusNoContent)
	sendRecord(t.Context(), srv.URL, "tok", "")
	if got.hits != 0 {
		t.Fatalf("an empty record must send nothing, hits=%d", got.hits)
	}
}

// A MALFORMED ENDPOINT SENDS NOTHING AND DOES NOT PANIC. http.NewRequest
// refuses a URL with a control character, and that refusal must be a return.
func TestAnUnbuildableRequestIsSilent(t *testing.T) {
	sendRecord(t.Context(), "http://\x7f/ci/record", "tok", "ourea-run-record/1 {}")
}

// POSTING WITHOUT A TOKEN IS A NO-OP AND NEVER A PANIC. Every lane runs this
// path with a nil token until its Call declares one, so the nil case is the
// common case rather than an edge — and a nil Secret must not be dereferenced.
func TestPostRecordIsInertWithoutAToken(t *testing.T) {
	postRecord(t.Context(), "http://ourea:8215/rob/infra.git", nil, "ourea-run-record/1 {}")
}

// AND WITHOUT A DOOR, which is the developer's local run. Pinned separately
// from the token so a mutant that flips either guard is caught by one of them.
func TestPostRecordIsInertWithoutADoor(t *testing.T) {
	postRecord(t.Context(), "", nil, "ourea-run-record/1 {}")
	postRecord(t.Context(), "not a url", nil, "ourea-run-record/1 {}")
}

// fakeSecret stands in for a *dagger.Secret so the engine-side read has both
// arms. Reading a real one is a session call and cannot happen in a unit test —
// which is precisely why the failure arm was NOT COVERED before this existed.
type fakeSecret struct {
	value string
	err   error
}

func (f fakeSecret) Plaintext(context.Context) (string, error) { return f.value, f.err }

// A SECRET THAT WILL NOT READ SENDS NOTHING. Without this the error arm is
// unreachable and a mutant that deletes it survives — which is what the lane
// reported.
func TestASecretThatCannotBeReadSendsNothing(t *testing.T) {
	srv, got := doorFor(t, http.StatusNoContent)

	postRecordWith(t.Context(), srv.URL+"/o/r.git", fakeSecret{err: errRefused}, "ourea-run-record/1 {}")

	if got.hits != 0 {
		t.Fatalf("a token that will not read must send nothing, hits=%d", got.hits)
	}
}

// AND ONE THAT READS DOES SEND, which is the other side of the same arm.
func TestASecretThatReadsIsSent(t *testing.T) {
	srv, got := doorFor(t, http.StatusNoContent)

	postRecordWith(t.Context(), srv.URL+"/o/r.git", fakeSecret{value: "tok-xyz"}, "ourea-run-record/1 {}")

	if got.hits != 1 {
		t.Fatalf("a readable token must be sent once, hits=%d", got.hits)
	}
	if got.auth != "Bearer tok-xyz" {
		t.Fatalf("the secret's VALUE must ride the header, got %q", got.auth)
	}
}

// AND A DOOR THAT CANNOT BE DERIVED IS REFUSED BEFORE THE SECRET IS EVEN READ:
// a local run must not make a session call it has no use for.
func TestNoDoorMeansTheSecretIsNeverRead(t *testing.T) {
	read := false
	postRecordWith(t.Context(), "", readSpy{&read}, "ourea-run-record/1 {}")
	if read {
		t.Fatal("with no door there is nothing to post to, so the secret must not be read")
	}
}

type readSpy struct{ read *bool }

func (r readSpy) Plaintext(context.Context) (string, error) { *r.read = true; return "t", nil }

var errRefused = errRefusedType{}

type errRefusedType struct{}

func (errRefusedType) Error() string { return "the secret refused to read" }
