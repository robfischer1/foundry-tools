package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/buildlane"
)

// smokeNeedle is in the boot smoke's label read and in no other query.
const smokeNeedle = `label(name:"` + buildlane.SmokeLabel + `")`

// smokeProbeNeedle is in the boot smoke's probe exec and in no other query.
const smokeProbeNeedle = `"/smoke.py"`

// AN IMAGE THAT DECLARES NO SMOKE IS NOT STARTED, and the phase says so rather
// than claiming it booted.
func TestAnImageWithNoSmokeLabelIsNotStarted(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	l, code, _ := laneFor(t, m, false)
	if code != buildlane.Clean {
		t.Fatalf("settled %d", code)
	}
	if got := atomNamed(t, l, "build:smoke"); got.State != buildlane.Clean || got.Reason != "no boot smoke declared (org.notusmi.smoke)" {
		t.Fatalf("smoke atom: %+v", got)
	}
	if engine.chain(smokeProbeNeedle) != "" {
		t.Fatal("the lane probed an image that declared no smoke")
	}
}

// A DECLARED SMOKE STARTS THE IMAGE AS A SERVICE with the label's env and asks
// it on the label's port and path; an answer passes.
func TestADeclaredSmokeStartsTheImageAndAsksIt(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	engine.label(smokeNeedle, "port=8204 path=/mcp wait=3 env=CALLIOPE_MCP_BACKEND=fixture")
	engine.stdout(smokeProbeNeedle, "smoke: http://smoke:8204/mcp answered 405 after 3s up\n")
	l, code, _ := laneFor(t, m, false)
	if code != buildlane.Clean {
		t.Fatalf("settled %d", code)
	}
	answered := "smoke: http://smoke:8204/mcp answered 405 after 3s up"
	if got := atomNamed(t, l, "build:smoke"); got.State != buildlane.Clean || got.Reason != answered || !contains(got.Logs, answered) {
		t.Fatalf("smoke atom: %+v", got)
	}
	probe := engine.chain(smokeProbeNeedle)
	for _, want := range []string{"withServiceBinding", `alias:"smoke"`, "http://smoke:8204/mcp", "FOUNDRY_SMOKE_STAMP"} {
		if !strings.Contains(probe, want) {
			t.Errorf("the probe chain lacks %s:\n%s", want, probe)
		}
	}
	// The service is the built image itself, with the label's env, its port
	// exposed and its own entrypoint.
	svc := engine.chain("asService", "CALLIOPE_MCP_BACKEND")
	for _, kv := range smokeTelemetryOff {
		if !hasCall(svc, "withEnvVariable", `name:"`+kv[0]+`"`, `value:"`+kv[1]+`"`) {
			t.Errorf("the smoked service does not start with %s=%s:\n%s", kv[0], kv[1], svc)
		}
	}
	if len(smokeTelemetryOff) != 4 {
		t.Errorf("the smoke declines the SDK and all three exporters, got %v", smokeTelemetryOff)
	}
	for _, want := range []string{`value:"fixture"`, "withExposedPort", "8204", "useEntrypoint:true"} {
		if !strings.Contains(svc, want) {
			t.Errorf("the service chain lacks %s:\n%s", want, svc)
		}
	}
	atomNamed(t, l, "build:verify")
}

// AN IMAGE THAT DOES NOT ANSWER IS FINDINGS, and nothing after the smoke runs:
// it is never scanned, never published.
func TestAnImageThatDoesNotAnswerIsFindings(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	engine.label(smokeNeedle, "port=8204")
	engine.stdout(smokeProbeNeedle, "smoke: http://smoke:8204/ did not answer after 8s: connection refused\n")
	engine.exitCode(smokeProbeNeedle, 1)
	l, code, _ := laneFor(t, m, true)
	if code != buildlane.Findings {
		t.Fatalf("settled %d, want findings", code)
	}
	refused := "smoke: http://smoke:8204/ did not answer after 8s: connection refused"
	if got := atomNamed(t, l, "build:smoke"); got.State != buildlane.Findings || got.Reason != "findings in the boot smoke: "+refused || !contains(got.Logs, refused) {
		t.Fatalf("smoke atom: %+v", got)
	}
	for _, after := range []string{"build:verify", "build:publish"} {
		if contains(atomNames(l), after) {
			t.Fatalf("%s ran after a smoke that failed", after)
		}
	}
}

// AN IMAGE THAT DIES BEFORE IT SERVES fails the service start, which is the
// engine's error rather than the probe's exit: findings, naming the port.
func TestAnImageThatDiesBeforeServingIsFindings(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	engine.label(smokeNeedle, "port=8204")
	engine.fail(smokeProbeNeedle, "start service: exited with code 1: ENOENT: no such file or directory")
	l, code, _ := laneFor(t, m, false)
	if code != buildlane.Findings {
		t.Fatalf("settled %d, want findings", code)
	}
	got := atomNamed(t, l, "build:smoke")
	if !strings.Contains(got.Reason, "exited instead of serving: the image did not start and serve on port 8204") {
		t.Fatalf("smoke atom: %+v", got)
	}
	if len(got.Logs) == 0 || !strings.Contains(got.Logs[0], "ENOENT") {
		t.Fatalf("the engine's error is not in the smoke's log: %+v", got.Logs)
	}
}

// A LABEL THAT CANNOT BE READ IS FINDINGS: the star asked for a check.
func TestAnUnreadableSmokeLabelIsFindings(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	engine.label(smokeNeedle, "path=/mcp")
	l, code, _ := laneFor(t, m, false)
	if code != buildlane.Findings {
		t.Fatalf("settled %d, want findings", code)
	}
	if got := atomNamed(t, l, "build:smoke"); !strings.Contains(got.Reason, "no port") {
		t.Fatalf("smoke atom: %+v", got)
	}
	if engine.chain(smokeProbeNeedle) != "" {
		t.Fatal("the lane probed on a label it could not read")
	}
}

// A LABEL READ THE ENGINE CANNOT ANSWER is classified like any failed step.
func TestASmokeLabelReadFaultIsClassified(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	engine.fail(smokeNeedle, "connection reset by peer")
	_, code, _ := laneFor(t, m, false)
	if code != buildlane.CouldNotRun {
		t.Fatalf("settled %d, want could-not-run", code)
	}
}
