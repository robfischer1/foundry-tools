package buildlane

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseSmokeReadsTheLabel(t *testing.T) {
	s, ok, err := ParseSmoke("port=8204 path=/mcp wait=5 env=A=1 env=B=x=y")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	want := Smoke{Port: 8204, Path: "/mcp", Wait: 5, Env: [][2]string{{"A", "1"}, {"B", "x=y"}}}
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("got %+v want %+v", s, want)
	}
}

func TestParseSmokeDefaults(t *testing.T) {
	s, ok, err := ParseSmoke(" port=80 ")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if s.Path != "/" || s.Wait != DefaultSmokeWait || s.Env != nil {
		t.Fatalf("defaults: %+v", s)
	}
}

func TestParseSmokeWaitBounds(t *testing.T) {
	for _, w := range []string{"0", "60"} {
		if _, _, err := ParseSmoke("port=1 wait=" + w); err != nil {
			t.Errorf("wait=%s refused: %v", w, err)
		}
	}
	for _, p := range []string{"1", "65535"} {
		if _, _, err := ParseSmoke("port=" + p); err != nil {
			t.Errorf("port=%s refused: %v", p, err)
		}
	}
}

func TestNoLabelIsNoSmoke(t *testing.T) {
	for _, l := range []string{"", "  "} {
		if _, ok, err := ParseSmoke(l); ok || err != nil {
			t.Errorf("%q: ok=%v err=%v", l, ok, err)
		}
	}
}

func TestParseSmokeRefusesWhatItCannotRead(t *testing.T) {
	for label, said := range map[string]string{
		"path=/mcp":          "no port",
		"port=0":             "not a port",
		"port=65536":         "not a port",
		"port=http":          "not a port",
		"port=1 path=mcp":    "does not start with /",
		"port=1 wait=-1":     "not 0-60",
		"port=1 wait=61":     "not 0-60",
		"port=1 wait=x":      "not 0-60",
		"port=1 env=A":       "not NAME=value",
		"port=1 env==1":      "not NAME=value",
		"port=1 colour=blue": "unknown key",
		"port=1 bare":        "not key=value",
		"port=1 path=":       "not key=value",
	} {
		_, ok, err := ParseSmoke(label)
		if ok || err == nil || !strings.Contains(err.Error(), said) {
			t.Errorf("%q: ok=%v err=%v, want an error saying %q", label, ok, err, said)
		}
	}
}

func TestProbeScriptAsksTheBoundImageAfterItsWait(t *testing.T) {
	got := Smoke{Port: 8204, Path: "/mcp", Wait: 8}.ProbeScript()
	for _, want := range []string{
		"time.sleep(8)\n",
		`urlopen("http://smoke:8204/mcp", timeout=10)`,
		"except urllib.error.HTTPError as e:",
		"answered %d after 8s up' % e.code",
		"did not answer after 8s",
		"    sys.exit(1)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("probe lacks %q:\n%s", want, got)
		}
	}
}

func TestSmokeFailedFilesADeadImageAsFindings(t *testing.T) {
	for _, out := range []string{
		"start service: process exited with code 1",
		"service smoke: health check failed: dial tcp 10.0.0.1:8204: connect: connection refused",
		"Healthcheck errored",
		"exit code: 1",
	} {
		if code, why := SmokeFailed(out); code != Findings || !strings.Contains(why, "exited instead of serving") {
			t.Errorf("%q: %d %q", out, code, why)
		}
	}
	if code, _ := SmokeFailed("dial tcp: lookup registry: no such host"); code != CouldNotRun {
		t.Errorf("a network fault is could-not-run, got %d", code)
	}
	if code, _ := SmokeFailed("something else broke"); code != Findings {
		t.Errorf("an unclassified failure is findings, got %d", code)
	}
}
