package pinsync

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestMainExitsWithWhatTheRunFound(t *testing.T) {
	fixturePins(t)
	inStep, drifted := writeTree(t, fixtureFiles()), writeTree(t, bumped())
	for _, tc := range []struct {
		name   string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{"an unknown flag", []string{"-x"}, 2, "", "usage: pinsync [-C dir] [-n] [-relock]"},
		{"an argument", []string{"-C", inStep, "extra"}, 2, "", "usage: pinsync [-C dir] [-n] [-relock]"},
		{"in step", []string{"-C", inStep}, 0, "", ""},
		{"in step, dry", []string{"-n", "-C", inStep}, 0, "", ""},
		{"drift, dry", []string{"-n", "-C", drifted}, 1, "internal/checks/toolpins.go\ninternal/checks/witlane.go\npytools/requirements.in\ntools.toml\npytools/requirements.txt\n", "pinsync: WasmToolsURL"},
		{"no tree", []string{"-C", t.TempDir()}, 2, "", "pinsync: open "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			getenv := func(k string) string {
				if k == "GITHUB_COM_TOKEN" {
					return "tok"
				}
				return ""
			}
			code := Main(context.Background(), tc.args, getenv, &http.Client{Transport: &fakeWeb{}}, &stdout, &stderr)
			if code != tc.code || stdout.String() != tc.stdout || !strings.Contains(stderr.String(), tc.stderr) {
				t.Errorf("exit %d, stdout %q, stderr %q; want %d, %q, one that says %q", code, stdout.String(), stderr.String(), tc.code, tc.stdout, tc.stderr)
			}
		})
	}
	if Client().Timeout == 0 {
		t.Error("the command's client has no deadline")
	}
}
