package bladefixed

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type ent struct {
	name     string
	typ      byte
	mode     int64
	uid, gid int
	body     string
}

func d(name string, mode int64, uid, gid int) ent { return ent{name, tar.TypeDir, mode, uid, gid, ""} }
func f(name string, mode int64, uid, gid int) ent { return ent{name, tar.TypeReg, mode, uid, gid, "x"} }

const (
	passwd = "root:x:0:0::/root:/bin/bash\nblade:x:999:999::/opt/blade:/usr/sbin/nologin\nagent:x:1000:1000::/home/agent:/bin/bash\n"
	group  = "root:x:0:\nblade:x:999:\nagent:x:1000:\n"
)

// good is the published blade-python-claude's relevant filesystem.
func good() []ent {
	return []ent{
		d("opt/blade", 0o550, 0, 999), d("opt/blade/gate", 0o550, 0, 999),
		d("opt/blade/suite", 0o550, 0, 999), d("opt/blade/keys", 0o550, 0, 999),
		f("opt/blade/suite/.blade-canary", 0o440, 0, 999),
		f("opt/blade/gate/blade_gate.py", 0o550, 0, 999),
		f("opt/blade/aperture.toml", 0o444, 0, 999),
		f("opt/blade/gate/venv/bin/pytest", 0o755, 0, 999),
		d("run/blade", 0o775, 999, 999), d("work", 0o755, 1000, 1000), d("home/agent", 0o755, 1000, 1000),
		f("usr/local/bin/blade-push", 0o755, 0, 0), f("usr/local/bin/blade-entrypoint", 0o755, 0, 0),
		f("usr/local/bin/claude", 0o755, 0, 0), f("usr/local/bin/blade-differ", 0o755, 0, 0),
		{"etc/passwd", tar.TypeReg, 0o644, 0, 0, passwd},
		{"etc/group", tar.TypeReg, 0o644, 0, 0, group},
	}
}

// with replaces the entry of the same name, or appends it.
func with(es []ent, e ent) []ent {
	out := append([]ent(nil), es...)
	for i := range out {
		if out[i].name == e.name {
			out[i] = e
			return out
		}
	}
	return append(out, e)
}

func without(es []ent, name string) []ent {
	var out []ent
	for _, e := range es {
		if e.name != name {
			out = append(out, e)
		}
	}
	return out
}

func blob(t *testing.T, dir string, b []byte) string {
	t.Helper()
	sum := sha256.Sum256(b)
	h := hex.EncodeToString(sum[:])
	p := filepath.Join(dir, "blobs", "sha256")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, h), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return "sha256:" + h
}

// lay writes an OCI layout of one image with one layer of es at imageDir/base.
func lay(t *testing.T, imageDir, base string, es []ent) {
	t.Helper()
	dir := filepath.Join(imageDir, base)
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range es {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Typeflag: e.typ, Mode: e.mode, Uid: e.uid, Gid: e.gid, Size: int64(len(e.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	layer := blob(t, dir, buf.Bytes())
	man := blob(t, dir, []byte(fmt.Sprintf(`{"layers":[{"digest":%q}]}`, layer)))
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(fmt.Sprintf(`{"manifests":[{"digest":%q}]}`, man)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func validate(t *testing.T, base string, es []ent) Result {
	t.Helper()
	dir := t.TempDir()
	lay(t, dir, base, es)
	res, err := Validate(dir)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestThePublishedShapeHolds(t *testing.T) {
	for _, b := range Bases {
		res := validate(t, b, good())
		if len(res.Problems) != 0 || len(res.Checked) != 1 || res.Checked[0] != b || res.Checks == 0 {
			t.Errorf("%s: %+v", b, res)
		}
	}
}

// RED FIRST: /opt/blade as root:root 0755 is the reopened split the dropped RUNs
// guarded against.
func TestAnOpenOptBladeIsRefused(t *testing.T) {
	res := validate(t, "blade-python-claude", with(good(), d("opt/blade", 0o755, 0, 0)))
	if len(res.Problems) != 1 || !strings.Contains(res.Problems[0], "/opt/blade is 0:0 0755, want 0:999 0550") {
		t.Errorf("%q", res.Problems)
	}
}

func TestEachBreakIsNamed(t *testing.T) {
	cases := []struct {
		name string
		es   []ent
		base string
		want string
	}{
		{"gate dir", with(good(), d("opt/blade/gate", 0o755, 0, 999)), "blade-base", "/opt/blade/gate is 0:999 0755"},
		{"suite dir", with(good(), d("opt/blade/suite", 0o550, 0, 0)), "blade-base", "/opt/blade/suite is 0:0 0550"},
		{"missing keys", without(good(), "opt/blade/keys"), "blade-base", "/opt/blade/keys is missing"},
		{"canary readable", with(good(), f("opt/blade/suite/.blade-canary", 0o444, 0, 999)), "blade-base", "canary is 0:999 0444"},
		{"a file is a dir", with(good(), d("opt/blade/aperture.toml", 0o444, 0, 999)), "blade-base", "/opt/blade/aperture.toml is a d, want a f"},
		{"suite not empty", with(good(), f("opt/blade/suite/test_x.py", 0o440, 0, 999)), "blade-base", "suite directory is not empty: /opt/blade/suite/test_x.py"},
		{"no agent", with(good(), ent{"etc/passwd", tar.TypeReg, 0o644, 0, 0, "blade:x:999:999::/opt/blade:/usr/sbin/nologin\n"}), "blade-base", "no agent as 1000:1000"},
		{"agent home", with(good(), ent{"etc/passwd", tar.TypeReg, 0o644, 0, 0, strings.Replace(passwd, "/home/agent", "/root", 1)}), "blade-base", "no agent as 1000:1000"},
		{"no blade", with(good(), ent{"etc/passwd", tar.TypeReg, 0o644, 0, 0, "agent:x:1000:1000::/home/agent:/bin/bash\n"}), "blade-base", "no blade as 999:999"},
		{"agent in blade", with(good(), ent{"etc/group", tar.TypeReg, 0o644, 0, 0, "blade:x:999:agent\nagent:x:1000:\n"}), "blade-base", "agent is a member of group blade"},
		{"agent group shared", with(good(), ent{"etc/group", tar.TypeReg, 0o644, 0, 0, "blade:x:999:\nagent:x:1000:bob\n"}), "blade-base", "no group agent as 1000"},
		{"no blade group", with(good(), ent{"etc/group", tar.TypeReg, 0o644, 0, 0, "agent:x:1000:\n"}), "blade-base", "no group blade as 999"},
		{"run dir", with(good(), d("run/blade", 0o755, 0, 0)), "blade-base", "/run/blade is 0:0 0755, want 999:999 0775"},
		{"work", with(good(), d("work", 0o755, 0, 0)), "blade-base", "/work is 0:0 0755, want 1000:1000 0755"},
		{"push", with(good(), f("usr/local/bin/blade-push", 0o700, 0, 0)), "blade-base", "blade-push is 0:0 0700"},
		{"no pytest", without(good(), "opt/blade/gate/venv/bin/pytest"), "blade-python-claude", "/opt/blade/gate/venv/bin/pytest is missing"},
		{"pytest not executable", with(good(), f("opt/blade/gate/venv/bin/pytest", 0o640, 0, 999)), "blade-python-claude", "pytest is 0:999 0640"},
		{"no claude", without(good(), "usr/local/bin/claude"), "blade-python-claude", "/usr/local/bin/claude is missing"},
		{"differ mode", with(good(), f("usr/local/bin/blade-differ", 0o644, 0, 0)), "blade-differ", "blade-differ is 0:0 0644"},
	}
	for _, c := range cases {
		res := validate(t, c.base, c.es)
		if !strings.Contains(strings.Join(res.Problems, "\n"), c.want) {
			t.Errorf("%s: want %q in %q", c.name, c.want, res.Problems)
		}
	}
}

// The lane's own files are asked of its own image only.
func TestAnotherLanesBinariesAreNotAsked(t *testing.T) {
	es := without(without(good(), "opt/blade/gate/venv/bin/pytest"), "usr/local/bin/claude")
	if res := validate(t, "blade-base", es); len(res.Problems) != 0 {
		t.Errorf("%q", res.Problems)
	}
	if res := validate(t, "blade-differ", es); len(res.Problems) != 0 {
		t.Errorf("%q", res.Problems)
	}
}

func TestNoLayoutIsNothingChecked(t *testing.T) {
	res, err := Validate(t.TempDir())
	if err != nil || len(res.Checked) != 0 || len(res.Problems) != 0 {
		t.Errorf("%+v %v", res, err)
	}
}

func TestAnUnreadableLayoutIsAnError(t *testing.T) {
	dir := t.TempDir()
	lay(t, dir, "blade-base", good())
	if err := os.WriteFile(filepath.Join(dir, "blade-base", "index.json"), []byte(`{"manifests":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(dir); err == nil || !strings.Contains(err.Error(), "blade-base could not be read") {
		t.Errorf("%v", err)
	}
}
