package atoms

import (
	"context"
	"strings"
	"testing"
)

// uv under UV_PYTHON_DOWNLOADS=never answers a tree that asks for a python the
// runner does not carry with "No interpreter found". That is a fact about the
// runner and the tree, not a finding in the code: the atom never ran, and the
// sentence names both versions (stellar-core-python c47e397, .python-version
// 3.15 on a 3.14.8 runner).
func TestNoPyDL_AnInterpreterUvWillNotFetchIsCannotRunNamingBothVersions(t *testing.T) {
	uvOut := "error: No interpreter found for Python 3.15 in search path\n\nhint: A managed Python download is available for Python 3.15, but Python downloads are set to 'never'\n"
	s := (&scripted{}).then(uvOut, 2).then("Python 3.14.8\n", 0)
	in := Input{Root: "/src", Exec: s.exec}
	v := SettleTool(context.Background(), in, toolRow, Cmd{Name: "uv", Args: []string{"run", "mypy"}})
	want := "the atom never ran: the tree asks for Python 3.15 but this runner provides Python 3.14.8 and never fetches an interpreter"
	if v.State != 2 || !strings.HasSuffix(v.Reason, "\n"+want) {
		t.Errorf("state %d reason %q, want 2 ending %q", v.State, v.Reason, want)
	}
	if len(s.calls) != 2 || s.calls[1].Name != "python3" || strings.Join(s.calls[1].Args, " ") != "--version" || !s.calls[1].Both {
		t.Errorf("the provisioned version is python3 --version: %+v", s.calls)
	}
}

func TestNoPyDL_TheProvisionedVersionUnreadableStillSettlesCannotRun(t *testing.T) {
	s := (&scripted{}).then("error: No interpreter found for Python >=3.15 in search path", 2).then("not found", 127)
	v := SettleTool(context.Background(), Input{Root: "/src", Exec: s.exec}, toolRow, Cmd{Name: "uv", Args: []string{"run", "pytest"}})
	want := "the atom never ran: the tree asks for Python >=3.15 but this runner provides an unreadable python and never fetches an interpreter"
	if v.State != 2 || !strings.HasSuffix(v.Reason, "\n"+want) {
		t.Errorf("state %d reason %q", v.State, v.Reason)
	}
}

func TestNoPyDL_OtherUvFailuresAndOtherToolsAreUntouched(t *testing.T) {
	for name, c := range map[string]Cmd{"a uv finding": {Name: "uv"}, "not uv": {Name: "ruff"}} {
		out := "x.py:1: F401"
		if c.Name == "ruff" {
			out = "error: No interpreter found for Python 3.15 in search path"
		}
		s := (&scripted{}).then(out, 1)
		v := SettleTool(context.Background(), Input{Root: "/src", Exec: s.exec}, toolRow, c)
		if v.State != 1 || len(s.calls) != 1 {
			t.Errorf("%s: state %d after %d calls", name, v.State, len(s.calls))
		}
	}
	s := (&scripted{}).then("error: No interpreter found for Python 3.15 in search path", 0)
	if v := SettleTool(context.Background(), Input{Root: "/src", Exec: s.exec}, toolRow, Cmd{Name: "uv"}); v.State != 0 {
		t.Errorf("a passing run is a pass: %+v", v)
	}
}
