package opsflux

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
)

const alloyBrokenCfg = "loki.process \"k3s_audit\" {\n  forward_to = []\n  stage.match {\n    selector = \"{job=\\\"k3s-audit\\\"} !~ `x`\"\n    action   = \"drop\"\n  }\n}\n"
const alloyGoodCfg = "loki.process \"k3s_audit\" {\n  forward_to = []\n  stage.match {\n    selector = \"{job=\\\"k3s-audit\\\"} !~ \\\"x\\\"\"\n    action   = \"drop\"\n  }\n}\n"

// configMap is a built ConfigMap carrying one Alloy config under config.alloy.
func configMap(cfg string) string {
	var b strings.Builder
	b.WriteString("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: node\n  namespace: apps\ndata:\n  config.alloy: |\n")
	for _, l := range strings.Split(strings.TrimRight(cfg, "\n"), "\n") {
		b.WriteString("    " + l + "\n")
	}
	return b.String()
}

func TestAlloyPass(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   map[string]string
		built   string
		state   int
		result  string
		needles []string
	}{
		{"a ConfigMap's backtick selector fails the atom", fluxTree(), configMap(alloyBrokenCfg), 1, atomtest.Findings,
			[]string{"ALLOY SELECTOR ConfigMap node:config.alloy:", "1 failed"}},
		{"a ConfigMap's quoted selector passes", fluxTree(), configMap(alloyGoodCfg), 0, atomtest.Pass, []string{"Alloy pass: 1 config(s) parsed, 0 failed"}},
		{"a tracked .alloy file that does not parse fails the atom", withFile(fluxTree(), "alloy/fx/x.alloy", "a = [\n"), builtStream, 1, atomtest.Findings,
			[]string{"ALLOY PARSE alloy/fx/x.alloy"}},
		{"a tracked .alloy file that parses passes", withFile(fluxTree(), "alloy/fx/x.alloy", "a = 1\n"), builtStream, 0, atomtest.Pass,
			[]string{"Alloy pass: 1 config(s) parsed, 0 failed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := atomtest.ToolTree(t, tc.files, nil)
			tools := &fluxTools{build: func(string) (string, int) { return tc.built, 0 }}
			f.Answer = tools.answer(t)
			atomtest.Expect(t, run(in), atomtest.StateOf(tc.state), tc.result, tc.needles...)
		})
	}
}

func withFile(m map[string]string, name, src string) map[string]string {
	m[name] = src
	return m
}

func TestAlloyFileThatWillNotRead(t *testing.T) {
	in, f := atomtest.ToolTree(t, fluxTree(), nil)
	in.Committable = append(in.Committable, "alloy/ghost.alloy")
	f.Answer = (&fluxTools{}).answer(t)
	atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the atom never ran", "ghost.alloy")
}
