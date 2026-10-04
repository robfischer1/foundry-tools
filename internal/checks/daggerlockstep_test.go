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
	lockDigest = "sha256:3c4e0cedaea400d7e30155e050f94910ea5f381b6f5942dd2172382657628cb4"
	lockOther  = "sha256:2a7c054e0864e297671c24a0726ebf1c9747d70291b8843b1726daaff200a334"
)

// helmPins is forge/dagger-engine-helm.yaml in the shape flux carries it.
func helmPins(chart, tag, digest string) string {
	return "  chart:\n    spec:\n      chart: dagger-helm\n      version: " + chart + "\n" +
		"      image:\n        ref: registry.dagger.io/engine:" + tag + "@" + digest + "\n"
}

// jobPins is prime/daedalus-jobs.yaml's two engine images.
func jobPins(build, lane string) string {
	return "  BUILD_JOB_IMAGE: \"registry.dagger.io/engine:" + build + "\"\n" +
		"  LANE_CALL_IMAGE: \"registry.dagger.io/engine:" + lane + "\"\n"
}

func cliPin(v string) string {
	return "# renovate: datasource=custom.dagger-engine depName=dagger-engine\nARG DAGGER_VERSION=" + v + "\nARG GRPC_VERSION=v1.83.2\n"
}

// countingDoor answers the engine source with body (404 when body is empty)
// and counts the asks.
func countingDoor(t *testing.T, body string) (Door, *int) {
	t.Helper()
	var mu sync.Mutex
	asks := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asks++
		mu.Unlock()
		if r.URL.Query().Get("repo") != DaggerEngineRepo || r.URL.Query().Get("path") != DaggerEngineSource || body == "" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return Door{Base: srv.URL, Client: srv.Client()}, &asks
}

func TestDaggerLockstepJudgesEveryPinAgainstTheEngine(t *testing.T) {
	agreeing := map[string]string{
		DaggerEngineSource:         helmPins("0.21.9", "v0.21.9", lockDigest),
		"prime/daedalus-jobs.yaml": jobPins("v0.21.9@"+lockDigest, "v0.21.9@"+lockDigest),
	}
	with := func(over map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range agreeing {
			out[k] = v
		}
		for k, v := range over {
			if v == "" {
				delete(out, k)
				continue
			}
			out[k] = v
		}
		return out
	}
	fluxMain := helmPins("0.21.9", "v0.21.9", lockDigest)

	for _, tc := range []struct {
		name      string
		files     map[string]string
		door      string
		state     int
		has, not  []string
		doorAsked bool
	}{
		{"no pins is absent", map[string]string{}, fluxMain, 0,
			[]string{"fleet:dagger-lockstep: ABSENT - this tree pins no dagger"}, nil, false},
		{"a consumer's dagger.json is not a pin", map[string]string{"dagger.json": `{"name":"x","engineVersion":"v9.9.9"}`}, fluxMain, 0,
			[]string{"ABSENT"}, nil, false},

		// flux: its own tree is the engine, and the door is never asked.
		{"flux, four pins agree", agreeing, "", 0,
			[]string{"lockstep: 4 pin(s) agree with the engine v0.21.9 (forge/dagger-engine-helm.yaml (this tree))"}, []string{"ABSENT"}, false},
		{"flux, the chart moved alone", with(map[string]string{DaggerEngineSource: helmPins("0.21.10", "v0.21.9", lockDigest)}), "", 1,
			[]string{"1 pin(s) out of lockstep", "forge/dagger-engine-helm.yaml: dagger-helm chart v0.21.10, but the engine is v0.21.9", "move in one pull"}, nil, false},
		{"flux, the engine moved but not the gate jobs", with(map[string]string{DaggerEngineSource: helmPins("0.21.10", "v0.21.10", lockOther)}), "", 1,
			[]string{"2 pin(s) out of lockstep", "prime/daedalus-jobs.yaml: engine image v0.21.9, but the engine is v0.21.10"}, nil, false},
		{"flux, one tag at two digests", with(map[string]string{"prime/daedalus-jobs.yaml": jobPins("v0.21.9@"+lockDigest, "v0.21.9@"+lockOther)}), "", 1,
			[]string{"1 pin(s) out of lockstep", "engine image v0.21.9@" + lockOther + ", but forge/dagger-engine-helm.yaml pins the same tag at " + lockDigest, "One version, one digest"}, nil, false},
		{"flux, the first image in the source is the engine", with(map[string]string{DaggerEngineSource: helmPins("0.21.9", "v0.21.9", lockDigest) + "  other: registry.dagger.io/engine:v0.21.10@" + lockOther + "\n"}), "", 1,
			[]string{"1 pin(s) out of lockstep", "engine image v0.21.10, but the engine is v0.21.9"}, nil, false},
		{"flux, a second chart in the source is read too", with(map[string]string{DaggerEngineSource: helmPins("0.21.9", "v0.21.9", lockDigest) + "  chart: dagger-helm\n  version: 0.21.10\n"}), "", 1,
			[]string{"1 pin(s) out of lockstep", "dagger-helm chart v0.21.10, but the engine is v0.21.9"}, nil, false},
		{"flux, the gate jobs without the source", with(map[string]string{DaggerEngineSource: ""}), fluxMain, 1,
			[]string{"forge/dagger-engine-helm.yaml names no engine image"}, nil, false},
		{"flux, a source with only the chart", with(map[string]string{DaggerEngineSource: "chart: dagger-helm\n  version: 0.21.9\n"}), fluxMain, 1,
			[]string{"names no engine image, so there is no engine"}, []string{"agree"}, false},
		{"a pin file whose spelling moved", with(map[string]string{"prime/daedalus-jobs.yaml": "BUILD_JOB_IMAGE: registry.dagger.io/engine:latest\n"}), "", 1,
			[]string{"prime/daedalus-jobs.yaml: names no dagger version this atom can read", "compared nothing there"}, nil, false},

		// base-images: the CLI is held to flux main's engine, read through the door.
		{"the CLI equals flux main", map[string]string{"bases/layer-dagger-cli/Dockerfile": cliPin("v0.21.9")}, fluxMain, 0,
			[]string{"lockstep: 1 pin(s) agree with the engine v0.21.9 (foundry/flux main forge/dagger-engine-helm.yaml)"}, nil, true},
		{"a bare CLI version reads as the tag", map[string]string{"bases/layer-dagger-cli/Dockerfile": cliPin("0.21.9")}, fluxMain, 0,
			[]string{"lockstep: 1 pin(s) agree"}, nil, true},
		{"every CLI pin in the Dockerfile is read", map[string]string{"bases/layer-dagger-cli/Dockerfile": cliPin("v0.21.9") + "ARG DAGGER_VERSION=v0.21.10\n"}, fluxMain, 1,
			[]string{"1 pin(s) out of lockstep", "CLI (ARG DAGGER_VERSION) v0.21.10"}, nil, true},
		{"two CLI pins at the engine", map[string]string{"bases/layer-dagger-cli/Dockerfile": cliPin("v0.21.9") + "ARG DAGGER_VERSION=v0.21.9\n"}, fluxMain, 0,
			[]string{"lockstep: 2 pin(s) agree"}, nil, true},
		{"the CLI moved ahead of the engine", map[string]string{"bases/layer-dagger-cli/Dockerfile": cliPin("v0.21.10")}, fluxMain, 1,
			[]string{"bases/layer-dagger-cli/Dockerfile: CLI (ARG DAGGER_VERSION) v0.21.10, but the engine is v0.21.9 (foundry/flux main", "custom.dagger-engine"}, nil, true},
		{"the engine landed and the CLI has not followed", map[string]string{"bases/layer-dagger-cli/Dockerfile": cliPin("v0.21.9")}, helmPins("0.21.10", "v0.21.10", lockOther), 1,
			[]string{"CLI (ARG DAGGER_VERSION) v0.21.9, but the engine is v0.21.10"}, nil, true},
		{"a Dockerfile with no DAGGER_VERSION", map[string]string{"bases/layer-dagger-cli/Dockerfile": "FROM scratch\n"}, fluxMain, 1,
			[]string{"names no dagger version"}, nil, true},

		// modules: never newer than the engine.
		{"a module at the engine", map[string]string{"dagger.json": `{"name":"m","sdk":{"source":"go"},"engineVersion":"v0.21.9"}`}, fluxMain, 0,
			[]string{"lockstep: 1 pin(s) agree"}, nil, true},
		{"a module behind the engine", map[string]string{"dagger.json": `{"name":"m","sdk":{"source":"go"},"engineVersion":"v0.20.12"}`}, fluxMain, 0,
			[]string{"lockstep: 1 pin(s) agree"}, nil, true},
		{"a module ahead of the engine", map[string]string{"dagger.json": `{"name":"m","sdk":{"source":"go"},"engineVersion":"v0.21.10"}`}, fluxMain, 1,
			[]string{"dagger.json: module engineVersion v0.21.10 is newer than the engine v0.21.9", "Land the engine first"}, nil, true},
		{"a module with no engineVersion", map[string]string{"dagger.json": `{"name":"m","sdk":{"source":"go"}}`}, fluxMain, 1,
			[]string{"a module with no readable engineVersion (\"\")"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			door, asks := countingDoor(t, tc.door)
			state, out := DaggerLockstep(context.Background(), tc.files, door)
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
					t.Errorf("report carries %q:\n%s", n, out)
				}
			}
			if (*asks > 0) != tc.doorAsked {
				t.Errorf("door asked %d time(s), want asked=%v", *asks, tc.doorAsked)
			}
		})
	}
}

// Findings are sorted, so a report is the same whatever order the files came in.
func TestDaggerLockstepSortsItsFindings(t *testing.T) {
	// The unreadable jobs file is found first and the chart second; the
	// report still reads forge/ before prime/.
	_, out := DaggerLockstep(context.Background(), map[string]string{
		DaggerEngineSource:         helmPins("0.21.8", "v0.21.9", lockDigest),
		"prime/daedalus-jobs.yaml": "BUILD_JOB_IMAGE: none\n",
	}, Door{})
	lines := strings.Split(out, "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[1], "forge/") || !strings.HasPrefix(lines[2], "prime/") {
		t.Errorf("want the header then forge/ before prime/:\n%s", out)
	}
}

func TestDaggerLockstepSettlesWhatItCannotReadAsCannotRun(t *testing.T) {
	cli := map[string]string{"bases/layer-dagger-cli/Dockerfile": cliPin("v0.21.9")}
	ctx := context.Background()

	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	if state, out := DaggerLockstep(ctx, cli, Door{Base: base}); state != 2 || !strings.Contains(out, "CANNOT RUN - the door is unreachable") {
		t.Errorf("dead door: %d %s", state, out)
	}

	door, _ := countingDoor(t, "")
	if state, out := DaggerLockstep(ctx, cli, door); state != 2 || !strings.Contains(out, "answered HTTP 404") || !strings.Contains(out, "archive?repo=foundry/flux&path=forge/dagger-engine-helm.yaml") {
		t.Errorf("404: %d %s", state, out)
	}

	door, _ = countingDoor(t, "<html>login</html>")
	if state, out := DaggerLockstep(ctx, cli, door); state != 2 || !strings.Contains(out, "answered no registry.dagger.io/engine:<version>@<digest> pin") {
		t.Errorf("not a pin: %d %s", state, out)
	}

	if state, out := DaggerLockstep(ctx, map[string]string{"dagger.json": "{"}, Door{}); state != 2 || !strings.Contains(out, "dagger.json did not parse") {
		t.Errorf("bad dagger.json: %d %s", state, out)
	}
}

func TestDaggerVersionsCompareNumerically(t *testing.T) {
	for _, tc := range []struct {
		a, b  string
		newer bool
	}{
		{"v0.21.9", "v0.21.9", false},
		{"v0.21.10", "v0.21.9", true},
		{"v0.21.9", "v0.21.10", false},
		{"v0.22.0", "v0.21.10", true},
		{"v0.21.10", "v0.22.0", false},
		{"v1.0.0", "v0.99.99", true},
		{"v0.99.99", "v1.0.0", false},
		{"0.22.0", "v0.21.9", false},
		{"v0.22.0", "v0.21", false},
	} {
		if got := daggerNewer(tc.a, tc.b); got != tc.newer {
			t.Errorf("daggerNewer(%s, %s) = %v, want %v", tc.a, tc.b, got, tc.newer)
		}
	}
	for v, ok := range map[string]bool{
		"v0.21.9": true, "v0.0.0": true, "0.21.9": false, "v0.21": false, "v0.21.9.1": false,
		"v0.x.9": false, "v-1.2.3": false, "": false,
	} {
		if daggerSemver(v) != ok {
			t.Errorf("daggerSemver(%q) = %v, want %v", v, !ok, ok)
		}
	}
	if vPrefixed("0.1.2") != "v0.1.2" || vPrefixed("v0.1.2") != "v0.1.2" {
		t.Errorf("vPrefixed spells the tag")
	}
}
