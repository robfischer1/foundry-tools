package checks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const (
	renRefA = "ghcr.io/renovatebot/renovate:43.261.4@sha256:adc66d8ffba5d835b9ef27768bd3176d07e7ea98c8dba274b07f511beea5fe81"
	renRefB = "ghcr.io/renovatebot/renovate:43.262.0@sha256:2a7c054e0864e297671c24a0726ebf1c9747d70291b8843b1726daaff200a334"
)

func renFlux(a, b string) string {
	return "            - name: config\n              image: " + a + "\n              command: [\"git\"]\n" +
		"            - name: renovate\n              image: " + b + "\n"
}

func renImages(ref string) string {
	return "const (\n\tRenovateTag   = \"43.261.4\"\n\tImageRenovate = \"" + ref + "\"\n)\n"
}

// renDoor answers flux's renovate.yaml with body (404 when empty) and counts asks.
func renDoor(t *testing.T, body string) (Door, *int) {
	t.Helper()
	var mu sync.Mutex
	asks := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asks++
		mu.Unlock()
		if r.URL.Query().Get("repo") != RenovateFluxRepo || r.URL.Query().Get("path") != RenovateFluxSource || body == "" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return Door{Base: srv.URL, Client: srv.Client()}, &asks
}

func TestRenovateLockstepJudgesTheImageAgainstFlux(t *testing.T) {
	for _, tc := range []struct {
		name      string
		files     map[string]string
		door      string
		state     int
		has, not  []string
		doorAsked bool
	}{
		{"no pin is absent", map[string]string{"internal/checks/images.go": "package checks\n"}, "", 0,
			[]string{"ABSENT"}, nil, false},
		{"flux, both containers agree", map[string]string{RenovateFluxSource: renFlux(renRefA, renRefA)}, "", 0,
			[]string{"2 pin(s) agree with the Renovate " + renRefA}, []string{"ABSENT"}, false},
		{"flux, one container moved alone", map[string]string{RenovateFluxSource: renFlux(renRefA, renRefB)}, "", 1,
			[]string{"1 pin(s) out of lockstep", "move in one pull"}, nil, false},
		{"flux, same tag at two digests", map[string]string{RenovateFluxSource: renFlux(renRefA, strings.Replace(renRefA, "adc66d8f", "00000000", 1))}, "", 1,
			[]string{"1 pin(s) out of lockstep"}, nil, false},
		{"flux, spelling moved", map[string]string{RenovateFluxSource: "image: renovate/renovate:latest\n"}, "", 1,
			[]string{"names no ghcr.io/renovatebot/renovate", "compared nothing there"}, nil, false},
		{"this repo equals flux main", map[string]string{RenovateImagesSource: renImages(renRefA)}, renFlux(renRefA, renRefA), 0,
			[]string{"1 pin(s) agree", "foundry/flux main renovate/renovate.yaml"}, nil, true},
		{"this repo is behind flux", map[string]string{RenovateImagesSource: renImages(renRefA)}, renFlux(renRefB, renRefB), 1,
			[]string{"ImageRenovate " + renRefA + ", but Renovate runs " + renRefB, "land flux first"}, nil, true},
		{"this repo is ahead of flux", map[string]string{RenovateImagesSource: renImages(renRefB)}, renFlux(renRefA, renRefA), 1,
			[]string{"out of lockstep"}, nil, true},
		{"the digest alone differs", map[string]string{RenovateImagesSource: renImages(renRefA)}, renFlux(strings.Replace(renRefA, "adc66d8f", "00000000", 1), renRefA), 1,
			[]string{"out of lockstep"}, nil, true},
		{"flux's source names no ref", map[string]string{RenovateImagesSource: renImages(renRefA)}, "image: nothing\n", 2,
			[]string{"CANNOT RUN", "deployed Renovate is unknown"}, nil, true},
		{"the door does not answer", map[string]string{RenovateImagesSource: renImages(renRefA)}, "", 2,
			[]string{"CANNOT RUN", "HTTP 404"}, nil, true},
		{"a tree with both judges itself and never asks", map[string]string{RenovateFluxSource: renFlux(renRefA, renRefA), RenovateImagesSource: renImages(renRefB)}, "", 1,
			[]string{"ImageRenovate " + renRefB + ", but Renovate runs " + renRefA + " (renovate/renovate.yaml (this tree))"}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			door, asks := renDoor(t, tc.door)
			state, out := RenovateLockstep(context.Background(), tc.files, door)
			if state != tc.state {
				t.Fatalf("state %d, want %d:\n%s", state, tc.state, out)
			}
			for _, h := range tc.has {
				if !strings.Contains(out, h) {
					t.Errorf("report lacks %q:\n%s", h, out)
				}
			}
			for _, n := range tc.not {
				if strings.Contains(out, n) {
					t.Errorf("report has %q:\n%s", n, out)
				}
			}
			if asked := *asks > 0; asked != tc.doorAsked {
				t.Errorf("door asked=%v (%d asks), want %v", asked, *asks, tc.doorAsked)
			}
		})
	}
}

// The real constant must be readable by the real pattern, or the atom would
// compare nothing and report ABSENT forever.
func TestRenovateLockstepReadsTheRealConstant(t *testing.T) {
	src := "const (\n\tImageRenovate = \"" + ImageRenovate + "\"\n)\n"
	if !renovateConstRe.MatchString(src) {
		t.Fatalf("renovateConstRe does not read ImageRenovate %q", ImageRenovate)
	}
}

func TestRenovateLockstepIsCannotRunWhenTheDoorIsDown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	door := Door{Base: srv.URL, Client: srv.Client()}
	srv.Close()
	state, out := RenovateLockstep(context.Background(), map[string]string{RenovateImagesSource: renImages(renRefA)}, door)
	if state != 2 || !strings.Contains(out, "the door is unreachable") {
		t.Fatalf("state %d:\n%s", state, out)
	}
}
