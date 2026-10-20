package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// retryRig shortens the schedule to n waits, counts the sleeps, and keeps what
// the retries said on the lane's stderr.
func retryRig(t *testing.T, n int) (slept *[]time.Duration, said *bytes.Buffer) {
	t.Helper()
	oldSched, oldSleep, oldOut := recordRetrySchedule, recordSleep, recordPostOut
	recordRetrySchedule = make([]time.Duration, n)
	for i := range recordRetrySchedule {
		recordRetrySchedule[i] = time.Millisecond * time.Duration(i+1)
	}
	slept, said = &[]time.Duration{}, &bytes.Buffer{}
	recordSleep = func(_ context.Context, d time.Duration) { *slept = append(*slept, d) }
	recordPostOut = said
	t.Cleanup(func() { recordRetrySchedule, recordSleep, recordPostOut = oldSched, oldSleep, oldOut })
	return slept, said
}

// scriptedDoor answers each request with the next status (the last repeats).
func scriptedDoor(t *testing.T, statuses ...int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		i := int(n.Add(1)) - 1
		w.WriteHeader(statuses[min(i, len(statuses)-1)])
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// A POST INTO A ROLLING RUNNER LANDS ON THE RETRY: 502, 503, then taken.
func TestARecordPostedIntoARollIsRetriedUntilTaken(t *testing.T) {
	slept, said := retryRig(t, 5)
	srv, n := scriptedDoor(t, 502, 503, 204)
	got := sendRecordRetrying(t.Context(), srv.URL, "tok-1", "ourea-run-record/1 {}")
	if !strings.Contains(got, "→ 204") || !strings.Contains(got, "attempt 3") || n.Load() != 3 || len(*slept) != 2 {
		t.Fatalf("outcome %q, requests %d, slept %v", got, n.Load(), *slept)
	}
	if line := said.String(); !strings.Contains(line, "attempt 1:") || !strings.Contains(line, "502") || strings.Contains(line, "tok-1") {
		t.Fatalf("the retry said %q", line)
	}
}

// A DOOR THAT IS DOWN, THEN BACK, ON THE SAME ADDRESS: the shape of a roll.
func TestARecordWaitsForADoorThatComesBack(t *testing.T) {
	retryRig(t, 4)
	probe := httptest.NewServer(http.NotFoundHandler())
	addr := probe.Listener.Addr().String()
	probe.Close()
	var served atomic.Int32
	waits := 0
	recordSleep = func(context.Context, time.Duration) {
		if waits++; waits == 2 {
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { served.Add(1); w.WriteHeader(204) })}
			go func() { _ = srv.Serve(ln) }()
			t.Cleanup(func() { _ = srv.Close() })
		}
	}
	got := sendRecordRetrying(t.Context(), "http://"+addr+"/ci/record", "tok", "ourea-run-record/1 {}")
	if !strings.Contains(got, "→ 204") || served.Load() != 1 || waits != 2 {
		t.Fatalf("outcome %q, served %d, waited %d", got, served.Load(), waits)
	}
}

// A DOOR THAT STAYS DOWN ENDS THE POST WITH ITS LAST ANSWER, once per scheduled
// retry plus the first.
func TestARecordGivesUpWhenTheScheduleIsSpent(t *testing.T) {
	slept, _ := retryRig(t, 3)
	srv, n := scriptedDoor(t, 503)
	got := sendRecordRetrying(t.Context(), srv.URL, "tok", "ourea-run-record/1 {}")
	if !strings.Contains(got, "→ 503") || n.Load() != 4 || len(*slept) != 3 {
		t.Fatalf("outcome %q, requests %d, slept %v", got, n.Load(), *slept)
	}
}

// AN ANSWER IS NOT A ROLL: 200, 204, 400, 404, 413 end the post at once.
func TestARecordIsNotRetriedOnAnAnswer(t *testing.T) {
	for _, status := range []int{200, 204, 400, 404, 413} {
		slept, _ := retryRig(t, 3)
		srv, n := scriptedDoor(t, status)
		got := sendRecordRetrying(t.Context(), srv.URL, "tok", "ourea-run-record/1 {}")
		if n.Load() != 1 || len(*slept) != 0 || strings.Contains(got, "attempt") {
			t.Fatalf("%d: requests %d, slept %v, outcome %q", status, n.Load(), *slept, got)
		}
	}
}

// A 401 IS A FRESH RUNNER'S UNREAD TOKEN for a few tries, then an unknown token.
func TestAnUnknownTokenIsRetriedOnlyAFewTimes(t *testing.T) {
	slept, _ := retryRig(t, 9)
	srv, n := scriptedDoor(t, 401)
	got := sendRecordRetrying(t.Context(), srv.URL, "tok", "ourea-run-record/1 {}")
	if int(n.Load()) != recordUnknownTokenRetries+1 || len(*slept) != recordUnknownTokenRetries || !strings.Contains(got, "401") {
		t.Fatalf("requests %d, slept %d, outcome %q", n.Load(), len(*slept), got)
	}
	retryRig(t, 9)
	srv, n = scriptedDoor(t, 401, 401, 204)
	if got := sendRecordRetrying(t.Context(), srv.URL, "tok", "ourea-run-record/1 {}"); !strings.Contains(got, "→ 204") || n.Load() != 3 {
		t.Fatalf("a runner that learns the token: requests %d, outcome %q", n.Load(), got)
	}
}

// NOTHING SENT IS NOT RETRIED: an empty token, an empty record, a bad URL.
func TestARecordThatWasNeverSentIsNotRetried(t *testing.T) {
	slept, _ := retryRig(t, 3)
	for _, c := range []struct{ endpoint, secret, record string }{
		{"http://d/ci/record", "", "r"}, {"http://d/ci/record", "t", ""}, {"http://\x7f/ci/record", "t", "r"},
	} {
		if got := sendRecordRetrying(t.Context(), c.endpoint, c.secret, c.record); !strings.HasPrefix(got, "not posted: ") {
			t.Fatalf("outcome %q", got)
		}
	}
	if len(*slept) != 0 {
		t.Fatalf("a post that was never sent waited %v", *slept)
	}
}

// A CANCELLED LANE STOPS WAITING: the retry loop honours ctx.
func TestARetryingPostStopsWhenTheLaneIsCancelled(t *testing.T) {
	retryRig(t, 5)
	srv, n := scriptedDoor(t, 503)
	ctx, cancel := context.WithCancel(t.Context())
	recordSleep = func(context.Context, time.Duration) { cancel() }
	got := sendRecordRetrying(ctx, srv.URL, "tok", "ourea-run-record/1 {}")
	// The first attempt reached the door; the cancelled lane's second never did.
	if n.Load() != 1 || !strings.Contains(got, "attempt 2") || !strings.Contains(got, "canceled") {
		t.Fatalf("requests %d, outcome %q", n.Load(), got)
	}
}

// THE LANE'S OWN POST RETRIES; THE ONE-SHOT SENDER STAYS ONE ATTEMPT (sendRecord;
// postRecordOutcome uses it).
func TestOnlyTheLanesOwnPostRetries(t *testing.T) {
	slept, _ := retryRig(t, 3)
	srv, n := scriptedDoor(t, 503)
	repo := srv.URL + "/o/r.git"
	_ = recordPostOutcome(t.Context(), repo, fakeSecret{value: "t"}, "ourea-run-record/1 {}")
	if n.Load() != 1 {
		t.Fatalf("the rider's post made %d requests", n.Load())
	}
	postRecordViaLane := recordPostOutcomeWith(t.Context(), repo, fakeSecret{value: "t"}, "ourea-run-record/1 {}", sendRecordRetrying)
	if n.Load() != 5 || len(*slept) != 3 || !strings.Contains(postRecordViaLane, "attempt 4") {
		t.Fatalf("the lane's post: requests %d, slept %v, %q", n.Load(), *slept, postRecordViaLane)
	}
}

// THE SHIPPED SCHEDULE OUTLASTS A ROLL without letting the lane's stderr go
// quiet for the 5 min silence bound.
func TestTheShippedRetryScheduleOutlastsARoll(t *testing.T) {
	var total, longest time.Duration
	for _, d := range recordRetrySchedule {
		total += d
		longest = max(longest, d)
	}
	if total < 2*time.Minute || total > 4*time.Minute || longest+recordPostTimeout >= 5*time.Minute {
		t.Fatalf("total %v, longest %v", total, longest)
	}
}
