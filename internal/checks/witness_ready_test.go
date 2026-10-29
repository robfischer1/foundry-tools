package checks

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWitnessReadyURLKeepsTheHostAndReplacesThePath(t *testing.T) {
	t.Parallel()
	if got := WitnessReadyURL("http://narcissus:8203/v1/witness?x=1#f"); got != "http://narcissus:8203/openapi.json" {
		t.Fatalf("got %q", got)
	}
	if WitnessReadyURL("::not a url") != "" || WitnessReadyURL("/mcp") != "" {
		t.Fatal("a URL with no host named a probe")
	}
}

func TestWitnessReadiness(t *testing.T) {
	t.Parallel()
	notReady := `{"service":"narcissus","status":"starting","ready":false,"store":"waiting"}`
	cases := []struct {
		name   string
		answer func(n int) (int, string, error)
		ok     bool
		calls  int
		detail string
	}{
		{"ready", func(int) (int, string, error) { return 200, "{}", nil }, true, 1, ""},
		{"not ready all along", func(int) (int, string, error) { return 503, notReady, nil }, false, 3, "HTTP 503 (status=starting store=waiting)"},
		{"unreachable", func(int) (int, string, error) { return 0, "", errors.New("dial tcp: refused") }, false, 3, "unreachable: dial tcp: refused"},
		{"a blip then ready", func(n int) (int, string, error) {
			if n < 3 {
				return 503, notReady, nil
			}
			return 200, "{}", nil
		}, true, 3, ""},
		{"a star with no /ready is not a reason to refuse", func(int) (int, string, error) { return 404, "", nil }, true, 1, ""},
		{"a 4xx the probe cannot read is not a reason either", func(int) (int, string, error) { return 401, "", nil }, true, 1, ""},
		{"a 503 whose JSON names no organ", func(int) (int, string, error) { return 503, "{}", nil }, false, 3, "HTTP 503"},
		{"a 503 whose body is not JSON", func(int) (int, string, error) { return 503, "upstream", nil }, false, 3, "HTTP 503"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var n int
			probe := func(_ context.Context, u string) (int, string, error) {
				n++
				if u != "http://narcissus:8203/openapi.json" {
					t.Errorf("probed %q", u)
				}
				return c.answer(n)
			}
			var slept int
			sleep := func(context.Context, time.Duration) error { slept++; return nil }
			ok, detail := WitnessReadiness(t.Context(), probe, WitnessRESTURL, 3, time.Second, sleep)
			if ok != c.ok || n != c.calls || detail != c.detail {
				t.Fatalf("ok=%v calls=%d detail=%q; want ok=%v calls=%d detail=%q", ok, n, detail, c.ok, c.calls, c.detail)
			}
			if want := c.calls - 1; slept != want {
				t.Fatalf("slept %d times, want %d", slept, want)
			}
		})
	}
}

func TestWitnessReadinessStopsWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	probe := func(context.Context, string) (int, string, error) { return 503, "", nil }
	sleep := func(context.Context, time.Duration) error { return context.Canceled }
	ok, detail := WitnessReadiness(t.Context(), probe, WitnessRESTURL, 3, time.Second, sleep)
	if ok || !strings.Contains(detail, "context canceled") {
		t.Fatalf("ok=%v detail=%q", ok, detail)
	}
}

func TestWitnessReadinessWithoutAParsableURLProbesNothing(t *testing.T) {
	t.Parallel()
	probe := func(context.Context, string) (int, string, error) { t.Error("probed"); return 0, "", nil }
	if ok, _ := WitnessReadiness(t.Context(), probe, "", 3, 0, nil); !ok {
		t.Fatal("no URL, no verdict about it")
	}
}

func TestWitnessNotReadyReasonIsACouldNotRunAboutTheTarget(t *testing.T) {
	t.Parallel()
	r := WitnessNotReadyReason("HTTP 503")
	for _, want := range []string{"CANNOT RUN", "HTTP 503", "No file was asked", "not a finding"} {
		if !strings.Contains(r, want) {
			t.Errorf("%q missing from %q", want, r)
		}
	}
}
