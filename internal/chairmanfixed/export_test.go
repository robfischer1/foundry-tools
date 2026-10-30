package chairmanfixed

import (
	"path/filepath"
	"testing"
)

// ReadLayout hands another base's validation (bases:blade) the merged
// filesystem and the final account files of one OCI layout.
func TestReadLayoutMergesTheLayersAndKeepsTheAccounts(t *testing.T) {
	img := t.TempDir()
	layout(t, img,
		[]tarEntry{
			{name: "etc/passwd", mode: 0o644, body: "root:x:0:0:root:/root:/bin/bash\n"},
			{name: "etc/group", mode: 0o644, body: "root:x:0:\n"},
			{name: "opt/blade", typ: "d", mode: 0o755},
		},
		[]tarEntry{
			{name: "opt/blade", typ: "d", mode: 0o550, uid: 0, gid: 999},
			{name: "opt/blade/gate.py", mode: 0o550, uid: 0, gid: 999, body: "gate"},
			{name: "usr/bin/x", typ: "l", mode: 0o777, to: "y"},
		},
	)
	l, err := ReadLayout(filepath.Join(img, "chairman"))
	if err != nil {
		t.Fatal(err)
	}
	if n := l.Files["opt/blade"]; n.Type != 'd' || n.Mode != 0o550 || n.UID != 0 || n.GID != 999 {
		t.Errorf("opt/blade merged as %+v, want the upper layer's d 0550 0:999", n)
	}
	if n := l.Files["opt/blade/gate.py"]; n.Type != 'f' || n.Mode != 0o550 {
		t.Errorf("gate.py read as %+v", n)
	}
	if n := l.Files["usr/bin/x"]; n.Type != 'l' || n.Link != "y" {
		t.Errorf("the link read as %+v", n)
	}
	if l.Accounts["etc/passwd"] == "" || l.Accounts["etc/group"] == "" {
		t.Errorf("accounts not kept: %v", l.Accounts)
	}
}

func TestReadLayoutOfNothingIsAnError(t *testing.T) {
	if _, err := ReadLayout(t.TempDir()); err == nil {
		t.Error("a directory with no index.json read as a layout")
	}
}
