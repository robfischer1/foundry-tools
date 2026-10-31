package checks

import (
	"strings"
	"testing"
)

const ciliumCNP = `apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: charon-ingress
spec:
  ingress:
    - toPorts:
        - rules:
            http:
              - method: "POST"
                path: "^/a/?$"
                headerMatches:
                  - {name: "X-Forge-Peer", value: "^traefik$"}
              - {method: "GET", host: "^h\\.example$"}
---
apiVersion: cilium.io/v2
kind: CiliumClusterwideNetworkPolicy
metadata:
  name: wide
specs:
  - egress:
      - toPorts:
          - rules:
              http:
                - path: "^/b$"
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: not-a-policy
data:
  http:
    - path: "^/ignored$"
`

func TestCiliumRegexes(t *testing.T) {
	got, err := CiliumRegexes(map[string]string{"network": ciliumCNP})
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, r := range got {
		seen = append(seen, r.Policy+" "+r.Field+" "+r.Pattern)
	}
	want := []string{"charon-ingress method POST", "charon-ingress path ^/a/?$", "charon-ingress method GET", "charon-ingress host ^h\\.example$", "wide path ^/b$"}
	if len(seen) != len(want) {
		t.Fatalf("got %q", seen)
	}
	for _, w := range want {
		found := false
		for _, s := range seen {
			found = found || s == w
		}
		if !found {
			t.Errorf("missing %q in %q", w, seen)
		}
	}
	if _, err := CiliumRegexes(map[string]string{"x": "kind: [\n"}); err == nil {
		t.Error("a stream that does not parse must be an error")
	}
}

// The sizes below were measured with google-re2 1.1.20251105 (the pytools lock),
// Latin-1, on the 84f6efa5 incident and its split form.
func TestCiliumRE2Report(t *testing.T) {
	rs := []CiliumRegex{
		{Tree: "network", Policy: "incident", Field: "path", Pattern: "P102"},
		{Tree: "network", Policy: "incident2", Field: "path", Pattern: "P102"},
		{Tree: "network", Policy: "split", Field: "path", Pattern: "P80"},
		{Tree: "network", Policy: "edge", Field: "path", Pattern: "P100"},
	}
	patterns, req := CiliumRE2Patterns(rs)
	if len(patterns) != 3 || !strings.Contains(req, `"P102"`) {
		t.Fatalf("patterns %q request %s", patterns, req)
	}
	sizes := map[string]string{"P102": `{"size":102,"error":""}`, "P80": `{"size":80,"error":""}`, "P100": `{"size":100,"error":""}`}
	var parts []string
	for _, p := range patterns {
		parts = append(parts, sizes[p])
	}
	state, report := CiliumRE2Report(rs, patterns, "["+strings.Join(parts, ",")+"]")
	if state != 1 || strings.Count(report, "RE2 program size 102 > 100") != 2 || strings.Contains(report, "network split") || strings.Contains(report, "network edge path:") {
		t.Errorf("state %d:\n%s", state, report)
	}
	if !strings.Contains(report, "2 over the cap of 100; largest 102") {
		t.Errorf("head line: %s", report)
	}
	// Exactly the cap passes.
	state, report = CiliumRE2Report(rs[3:], []string{"P100"}, `[{"size":100,"error":""}]`)
	if state != 0 || !strings.Contains(report, "largest 100 (network edge path)") {
		t.Errorf("at the cap: %d %s", state, report)
	}
	// A regex RE2 cannot compile is a finding; an answer of the wrong shape is a 2.
	state, report = CiliumRE2Report(rs[3:], []string{"P100"}, `[{"size":0,"error":"missing )"}]`)
	if state != 1 || !strings.Contains(report, "does not compile in RE2 (missing ))") {
		t.Errorf("compile error: %d %s", state, report)
	}
	for _, bad := range []string{"not json", "[]"} {
		if state, report := CiliumRE2Report(rs[3:], []string{"P100"}, bad); state != 2 || !strings.Contains(report, "UNANALYZABLE") {
			t.Errorf("%q: %d %s", bad, state, report)
		}
	}
}

// A pattern over 90 characters is cut in the report line, and the cut is marked.
func TestCiliumRE2ReportShortensALongPattern(t *testing.T) {
	long := "^/" + strings.Repeat("a", 120) + "$"
	rs := []CiliumRegex{{Tree: "network", Policy: "long", Field: "path", Pattern: long}}
	state, report := CiliumRE2Report(rs, []string{long}, `[{"size":101,"error":""}]`)
	if state != 1 || !strings.Contains(report, long[:90]+"...") || strings.Contains(report, long) {
		t.Errorf("state %d:\n%s", state, report)
	}
}
