package checks

import (
	"strings"
	"testing"
)

// A star manifest in the flux tree's shape: a flow-style env list, and a
// comment that names both variables (a text scan would count the prose).
func starManifest(env string) string {
	return `# STELLAR_PEP_MODE and STELLAR_PDP_URL are explained here and set below.
apiVersion: apps/v1
kind: Deployment
metadata: {name: star}
spec:
  template:
    spec:
      containers:
        - name: star
          env:
` + env + `
        - name: sidecar
          env:
            - {name: LOG_LEVEL, value: info}
`
}

const (
	witness = `            - {name: STELLAR_PEP_MODE, value: "witness"}`
	pdpURL  = `            - {name: STELLAR_PDP_URL, value: "http://ouranos-opa:8181/v1/data/authz/decision"}`
)

func TestPDPWiredGreen(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"armed and wired":              {"prime/star-a.yaml": starManifest(witness + "\n" + pdpURL)},
		"mode off needs no url":        {"prime/star-a.yaml": starManifest(`            - {name: STELLAR_PEP_MODE, value: "off"}`)},
		"no mode at all is off":        {"prime/star-a.yaml": starManifest(`            - {name: OTHER, value: x}`)},
		"url from a secret counts":     {"prime/star-a.yaml": starManifest(witness + "\n" + `            - name: STELLAR_PDP_URL` + "\n" + `              valueFrom: {configMapKeyRef: {name: c, key: k}}`)},
		"enforce mode wired":           {"prime/star-a.yaml": starManifest(`            - {name: STELLAR_PEP_MODE, value: "enforce"}` + "\n" + pdpURL)},
		"several stars, all wired":     {"prime/star-a.yaml": starManifest(witness + "\n" + pdpURL), "prime/star-b.yaml": starManifest(witness + "\n" + pdpURL)},
		"comment naming both is inert": {"prime/star-a.yaml": "# STELLAR_PEP_MODE=witness STELLAR_PDP_URL=x\nkind: ConfigMap\n"},
	} {
		state, report := PDPWired(files)
		if state != int(StatePass) {
			t.Errorf("%s: state %d, want pass:\n%s", name, state, report)
		}
	}
}

func TestPDPWiredRed(t *testing.T) {
	files := map[string]string{
		"prime/star-themis.yaml": starManifest(witness),
		"prime/star-ok.yaml":     starManifest(witness + "\n" + pdpURL),
		"prime/star-empty.yaml":  starManifest(witness + "\n" + `            - {name: STELLAR_PDP_URL, value: ""}`),
	}
	state, report := PDPWired(files)
	if state != int(StateFindings) {
		t.Fatalf("state %d, want findings:\n%s", state, report)
	}
	for _, want := range []string{
		"prime/star-themis.yaml (container star)",
		"prime/star-empty.yaml (container star)",
		"2 of 3 container(s)",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "star-ok.yaml") {
		t.Errorf("a wired star was named:\n%s", report)
	}
}

// A manifest the atom could not read is not a pass: a file never read is a
// file never judged.
func TestPDPWiredCannotRunOnUnparseableYAML(t *testing.T) {
	state, report := PDPWired(map[string]string{"prime/star-bad.yaml": "a: [unclosed\n"})
	if state != int(StateCannotRun) || !strings.Contains(report, "prime/star-bad.yaml would not parse") {
		t.Errorf("state %d, report %q", state, report)
	}
}

func TestIsPDPWiredFile(t *testing.T) {
	for path, want := range map[string]bool{
		"prime/star-themis.yaml":     true,
		"prime/star-themis.yml":      false,
		"prime/ourea-config.yaml":    false,
		"prime/sub/star-x.yaml":      false,
		"other/prime/star-x.yaml":    false,
		"prime/star-orbits/x.yaml":   false,
		"prime/not-star-themis.yaml": false,
	} {
		if got := IsPDPWiredFile(path); got != want {
			t.Errorf("IsPDPWiredFile(%q) = %v, want %v", path, got, want)
		}
	}
}
