package checks

import (
	"strings"
	"testing"
)

// brokenMatch is the shape of flux 1243cf43: a quoted Alloy string holding a
// LogQL backtick string. Alloy's grammar accepts it; loki.process refuses it.
const brokenMatch = "loki.process \"k3s_audit\" {\n  forward_to = []\n  stage.match {\n    selector = \"{job=\\\"k3s-audit\\\"} !~ `\\\"verb\\\":\\\"create\\\"`\"\n    action   = \"drop\"\n  }\n}\n"

// fixedMatch is flux fee514d7's form.
const fixedMatch = "loki.process \"k3s_audit\" {\n  forward_to = []\n  stage.match {\n    selector = \"{job=\\\"k3s-audit\\\"} !~ \\\"\\\\\\\"verb\\\\\\\":\\\\\\\"create\\\\\\\"\\\"\"\n    action   = \"drop\"\n  }\n}\n"

func TestAlloyReport(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		bad       bool
		needle    string
	}{
		{"the backtick selector of 1243cf43 is a finding, with its line", brokenMatch, true, "ALLOY SELECTOR cfg.alloy:4:"},
		{"the quoted form of fee514d7 passes", fixedMatch, false, "1 config(s) parsed, 0 failed"},
		{"a syntax error is a finding with the parser's position", "loki.process \"x\" {\n  forward_to = [\n}\n", true, "ALLOY PARSE cfg.alloy"},
		{"a backtick outside a match selector is not read", "local.file \"x\" {\n  filename = `/etc/x`\n}\n", false, "0 failed"},
		{"a selector written as a raw string holds no backtick once unquoted", "loki.process \"x\" {\n  stage.match {\n    selector = `{a=\"b\"}`\n  }\n}\n", false, "0 failed"},
		{"a backtick line filter after the stream selector is a finding", "loki.process \"x\" {\n  stage.match {\n    selector = \"{a=\\\"b\\\"} |= `x`\"\n  }\n}\n", true, "ALLOY SELECTOR cfg.alloy:3"},
		{"a selector that is not a literal is not read", "loki.process \"x\" {\n  stage.match {\n    selector = env(\"S\")\n  }\n}\n", false, "0 failed"},
		{"an empty config parses", "", false, "0 failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report, bad := AlloyReport([]AlloyConfig{{Name: "cfg.alloy", Src: tc.src}})
			if bad != tc.bad || !strings.Contains(report, tc.needle) {
				t.Errorf("bad=%v, want %v; report:\n%s", bad, tc.bad, report)
			}
		})
	}
}

func TestAlloyConfigs(t *testing.T) {
	stream := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: node\ndata:\n  config.alloy: |\n    a = 1\n  other.yaml: x\n  n: 3\n---\nkind: Deployment\nmetadata:\n  name: d\ndata:\n  x.alloy: no\n"
	got, err := AlloyConfigs([]string{stream}, map[string]string{"alloy/m.alloy": "b = 2", "alloy/readme.md": "x"})
	if err != nil || len(got) != 2 || got[0].Name != "ConfigMap node:config.alloy" || got[1].Name != "alloy/m.alloy" || !strings.Contains(got[0].Src, "a = 1") {
		t.Errorf("got %+v, %v", got, err)
	}
	if _, err := AlloyConfigs([]string{"kind: [\n"}, nil); err == nil {
		t.Error("a stream that does not parse must be an error")
	}
}
