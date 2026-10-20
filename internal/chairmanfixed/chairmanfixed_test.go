package chairmanfixed

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// spec is one row of the fixture's fixed-set.tsv.
type spec struct {
	path, typ, mode, owner, val string
	body                        string // the file's content (f)
}

type fixture struct {
	t    *testing.T
	root string // the tree root
	dir  string // bases/chairman
	rows []spec
}

const dockerText = `FROM docker.io/library/python:3.14-slim-bookworm@sha256:48b13b003dda20b16f9442b8475aa05fe21bf6579a8c881db92ffb4d8fd20f83
ENV PATH=/opt/go/bin:/usr/local/bin:/usr/bin
COPY bases/chairman/fixed/whiteout/ /
COPY bases/chairman/fixed/root/ /
COPY --chown=0:42 --chmod=0640 bases/chairman/fixed/shadow/shadow bases/chairman/fixed/shadow/gshadow /etc/
COPY --chmod=0440 bases/chairman/fixed/sudoers/sudoers /etc/sudoers
COPY --chmod=0440 bases/chairman/fixed/sudo/agent /etc/sudoers.d/agent
COPY --chown=0:102 --chmod=2755 bases/chairman/fixed/suid/ssh-agent /usr/bin/ssh-agent
COPY --chown=0:101 --chmod=4754 bases/chairman/fixed/suid/dbus-daemon-launch-helper /usr/lib/dbus-1.0/dbus-daemon-launch-helper
COPY --chown=1001:1001 bases/chairman/fixed/home/agent/ /home/agent/
COPY --chown=1001:1001 bases/chairman/fixed/work/ /work/
COPY --chown=1001:1001 bases/chairman/fixed/work-whiteout/.wh..gitkeep /work/
`

const unit = "[Unit]\nDescription=x\n[Service]\nExecStart=/bin/true\n[Install]\nWantedBy=multi-user.target\n"

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, root: t.TempDir()}
	f.dir = filepath.Join(f.root, "bases", "chairman")
	passwd := "root:x:0:0:root:/root:/bin/bash\nmessagebus:x:100:101::/nonexistent:/usr/sbin/nologin\nagent:x:1001:1001::/home/agent:/bin/bash\n"
	group := "root:x:0:\nshadow:x:42:\nmessagebus:x:101:\n_ssh:x:102:\nagent:x:1001:\n"
	shadow := "root:*:20000:0:99999:7:::\nmessagebus:!:20000::::::\nagent:*:20000:0:99999:7:::\n"
	gshadow := "root:*::\nshadow:*::\nmessagebus:!::\n_ssh:!::\nagent:!::\n"
	f.rows = []spec{
		{"etc/passwd", "f", "0644", "0:0", "", passwd},
		{"etc/group", "f", "0644", "0:0", "", group},
		{"etc/shadow", "f", "0640", "0:42", "", shadow},
		{"etc/gshadow", "f", "0640", "0:42", "", gshadow},
		{"etc/subuid", "f", "0644", "0:0", "", "agent:100000:65536\n"},
		{"etc/subgid", "f", "0644", "0:0", "", "agent:100000:65536\n"},
		{"etc/sudoers", "f", "0440", "0:0", "", "Defaults env_reset\n@includedir /etc/sudoers.d\n"},
		{"etc/sudoers.d/agent", "f", "0440", "0:0", "", "agent ALL=(ALL) NOPASSWD:ALL\n"},
		{"usr/bin/ssh-agent", "f", "2755", "0:102", "", "ssh-agent-bytes"},
		{"usr/lib/dbus-1.0/dbus-daemon-launch-helper", "f", "4754", "0:101", "", "helper-bytes"},
		{"var/lib/dpkg/statoverride", "f", "0644", "0:0", "", "root messagebus 4754 /usr/lib/dbus-1.0/dbus-daemon-launch-helper\n"},
		{"etc/profile.d/10-chairman-path.sh", "f", "0644", "0:0", "", "export PATH=/opt/go/bin:/usr/local/bin:/usr/bin\n"},
		{"etc/machine-id", "f", "0644", "0:0", "", ""},
		{"home/agent", "d", "0755", "1001:1001", "", ""},
		{"home/agent/.bashrc", "f", "0644", "1001:1001", "", "# rc\n"},
		{"work", "d", "0755", "1001:1001", "", ""},
		{"etc/systemd/system/multi-user.target.wants/chairman-seed.service", "l", "0777", "0:0", "/etc/systemd/system/chairman-seed.service", ""},
		{"etc/systemd/system/kmod-static-nodes.service", "l", "0777", "0:0", "/dev/null", ""},
		{"etc/systemd/system/systemd-udevd.service", "l", "0777", "0:0", "/dev/null", ""},
		{"etc/systemd/system/sys-kernel-config.mount", "l", "0777", "0:0", "/dev/null", ""},
		{"etc/systemd/system/sys-kernel-debug.mount", "l", "0777", "0:0", "/dev/null", ""},
		{"etc/systemd/system/sys-kernel-tracing.mount", "l", "0777", "0:0", "/dev/null", ""},
		{"etc/alternatives/cc", "l", "0777", "0:0", "/usr/bin/gcc", ""},
		{"etc/claude-code/managed-settings.d/50-forge.json", "l", "0777", "0:0", "/home/agent/.claude/forge/managed-settings.json", ""},
		{"usr/local/lib/python3.14/site-packages/pip", "x", "", "", "", ""},
	}
	for _, r := range f.rows {
		w := where(r.path)
		switch r.typ {
		case "f":
			f.write(w.file, r.body)
		case "l":
			f.link(w.file, r.val)
		}
	}
	f.write("Dockerfile", dockerText)
	f.write("units/chairman-seed.service", unit)
	f.write("fixed/whiteout/usr/lib/systemd/system/multi-user.target.wants/.wh.getty.target", "")
	f.write("fixed/work/.gitkeep", "")
	f.write("fixed/work-whiteout/.wh..gitkeep", "")
	f.write("packages.lock", `schema = "packages.lock@1"
[[package]]
  name = "systemd"
  version = "252"
[[package]]
  name = "dbus"
  version = "1.14"
[[package]]
  name = "sudo"
  version = "1.9"
[[package]]
  name = "openssh-client"
  version = "1:9.2"
[[package]]
  name = "tini"
  version = "0.19"
`)
	f.flush()
	return f
}

func (f *fixture) write(rel, body string) {
	f.t.Helper()
	p := filepath.Join(f.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) link(rel, target string) {
	f.t.Helper()
	p := filepath.Join(f.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	_ = os.Remove(p)
	if err := os.Symlink(target, p); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) body(rel string) string {
	b, err := os.ReadFile(filepath.Join(f.dir, filepath.FromSlash(rel)))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

// flush writes the three tables from the rows, the lock and the committed files.
func (f *fixture) flush() {
	var set strings.Builder
	set.WriteString("path\ttype\tmode\tuid:gid\tlink-or-sha256\n")
	for _, r := range f.rows {
		val := r.val
		if r.typ == "f" {
			val = sum(f.body(where(r.path).file))
		}
		fmt.Fprintf(&set, "%s\t%s\t%s\t%s\t%s\n", r.path, r.typ, r.mode, r.owner, val)
	}
	f.write("fixed-set.tsv", set.String())
	f.write("maintscripts.tsv", "package\tversion\tscript\tsha256_12\nsystemd\t252\tpostinst\tabc\ndbus\t1.14\tpostinst\tdef\nsudo\t1.9\t-\t-\nopenssh-client\t1:9.2\tpostinst\t012\ntini\t0.19\t-\t-\n")
	dbf := "package\tversion\tpath\tsha256\n"
	for _, d := range [][]string{{"openssh-client", "1:9.2", "usr/bin/ssh-agent"}, {"dbus", "1.14", "usr/lib/dbus-1.0/dbus-daemon-launch-helper"}, {"sudo", "1.9", "etc/sudoers"}} {
		dbf += strings.Join(append(d, sum(f.body(where(d[2]).file))), "\t") + "\n"
	}
	f.write("fixed-debfiles.tsv", dbf)
}

// tarEntry is one path of a layout's layer.
type tarEntry struct {
	name, typ string
	mode      int64
	uid, gid  int
	body, to  string
}

func gz(t *testing.T, ents []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range ents {
		h := &tar.Header{Name: e.name, Mode: e.mode, Uid: e.uid, Gid: e.gid}
		switch e.typ {
		case "d":
			h.Typeflag, h.Name = tar.TypeDir, e.name+"/"
		case "l":
			h.Typeflag, h.Linkname = tar.TypeSymlink, e.to
		case "h":
			h.Typeflag, h.Linkname = tar.TypeLink, e.to
		case "p":
			h.Typeflag = tar.TypeFifo
		default:
			h.Typeflag, h.Size = tar.TypeReg, int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte(e.body))
		}
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

// layout writes an OCI layout of the layers at dir/chairman.
func layout(t *testing.T, dir string, layers ...[]tarEntry) {
	t.Helper()
	root := filepath.Join(dir, "chairman")
	put := func(data []byte) string {
		h := sha256.Sum256(data)
		d := hex.EncodeToString(h[:])
		p := filepath.Join(root, "blobs", "sha256", d)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return "sha256:" + d
	}
	var m struct {
		Layers []map[string]string `json:"layers"`
	}
	for _, l := range layers {
		m.Layers = append(m.Layers, map[string]string{"digest": put(gz(t, l))})
	}
	mb, _ := json.Marshal(m)
	idx, _ := json.Marshal(map[string]any{"manifests": []map[string]string{{"digest": put(mb)}}})
	if err := os.WriteFile(filepath.Join(root, "index.json"), idx, 0o644); err != nil {
		t.Fatal(err)
	}
}

// imageLayers is a base layer holding the FROM's accounts and the layer the
// COPYs make, true to the fixture's rows.
func (f *fixture) imageLayers() [][]tarEntry {
	base := []tarEntry{
		{name: "etc/passwd", mode: 0o644, body: "root:x:0:0:root:/root:/bin/bash\n"},
		{name: "etc/group", mode: 0o644, body: "root:x:0:\n"},
		{name: "usr/bin/gcc", mode: 0o755, body: "gcc"},
		{name: "etc/systemd/system/chairman-seed.service", mode: 0o644, body: unit},
		{name: "lib", typ: "l", mode: 0o777, to: "usr/lib"},
		{name: "usr/lib/x", mode: 0o755, body: "x"},
		{name: "usr/local/lib/python3.14/site-packages/pip/__init__.py", mode: 0o644, body: "pip"},
	}
	var set []tarEntry
	for _, r := range f.rows {
		var uid, gid int
		fmt.Sscanf(r.owner, "%d:%d", &uid, &gid)
		var mode int64
		fmt.Sscanf(r.mode, "%o", &mode)
		switch r.typ {
		case "f":
			set = append(set, tarEntry{name: r.path, mode: mode, uid: uid, gid: gid, body: f.body(where(r.path).file)})
		case "d":
			set = append(set, tarEntry{name: r.path, typ: "d", mode: mode, uid: uid, gid: gid})
		case "l":
			set = append(set, tarEntry{name: r.path, typ: "l", mode: 0o777, to: r.val})
		}
	}
	whiteouts := []tarEntry{{name: "usr/local/lib/python3.14/site-packages/.wh.pip", mode: 0o644}}
	return [][]tarEntry{base, set, whiteouts}
}

const (
	statePass     = 0
	stateFindings = 1
	stateCannot   = 2
)

type verdict struct {
	State int
	Text  string
}

func validate(root, img string) verdict {
	res, err := Validate(root, img)
	switch {
	case err != nil:
		return verdict{stateCannot, err.Error()}
	case len(res.Problems) > 0:
		return verdict{stateFindings, strings.Join(res.Problems, "\n")}
	}
	return verdict{statePass, fmt.Sprintf("%d checks, %d rows, %d locked; %s", res.Checks, res.Rows, res.Locked, res.Note)}
}

func run(t *testing.T, f *fixture, withLayout bool) verdict {
	t.Helper()
	img := t.TempDir()
	if withLayout {
		layout(t, img, f.imageLayers()...)
	}
	return validate(f.root, img)
}

func expect(t *testing.T, v verdict, state int, needles ...string) {
	t.Helper()
	if v.State != state {
		t.Fatalf("state %d, want %d:\n%s", v.State, state, v.Text)
	}
	for _, n := range needles {
		if !strings.Contains(v.Text, n) {
			t.Errorf("text lacks %q:\n%s", n, v.Text)
		}
	}
}

func TestAConsistentBaseAndImagePass(t *testing.T) {
	f := newFixture(t)
	expect(t, run(t, f, false), statePass, "no assembled layout", "5 locked")
	v := run(t, f, true)
	expect(t, v, statePass, "the assembled image")
}

// Each of these is one way the committed files, the tables or the lock can
// disagree, and the atom names it.
func TestSourceChecksNameEachDisagreement(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*fixture)
		want   string
	}{
		"a lock bump leaves the script hashes behind": {func(f *fixture) {
			f.write("packages.lock", strings.Replace(f.body("packages.lock"), `"252"`, `"253"`, 1))
		}, "systemd is locked at 253 and was measured at 252"},
		"a new package": {func(f *fixture) {
			f.write("packages.lock", f.body("packages.lock")+"[[package]]\n  name = \"newpkg\"\n  version = \"1\"\n")
		}, "newpkg 1 is locked and has no row"},
		"a dropped package": {func(f *fixture) {
			f.write("maintscripts.tsv", f.body("maintscripts.tsv")+"gone\t1\t-\t-\n")
		}, "gone 1 is no longer locked"},
		"a package file taken from another version": {func(f *fixture) {
			f.write("packages.lock", strings.Replace(f.body("packages.lock"), `"1:9.2"`, `"1:9.3"`, 1))
			f.write("maintscripts.tsv", strings.Replace(f.body("maintscripts.tsv"), "1:9.2", "1:9.3", 1))
		}, "openssh-client is locked at \"1:9.3\" and usr/bin/ssh-agent was taken from 1:9.2"},
		"a package file edited": {func(f *fixture) { f.write("fixed/suid/ssh-agent", "tampered") }, "is not the file the openssh-client package ships"},
		"a fixed file edited":   {func(f *fixture) { f.write("fixed/root/etc/subuid", "agent:1:1\n") }, "does not match fixed-set.tsv"},
		"a link retargeted":     {func(f *fixture) { f.link("fixed/root/etc/alternatives/cc", "/usr/bin/other") }, "links to /usr/bin/other, fixed-set.tsv says /usr/bin/gcc"},
		"a link missing":        {func(f *fixture) { _ = os.Remove(filepath.Join(f.dir, "fixed/root/etc/alternatives/cc")) }, "fixed/root/etc/alternatives/cc"},
		"a COPY that loses the mode": {func(f *fixture) {
			f.write("Dockerfile", strings.Replace(dockerText, "--chmod=0440 bases/chairman/fixed/sudo/agent", "bases/chairman/fixed/sudo/agent", 1))
		}, "COPY bases/chairman/fixed/sudo/agent sets --chmod=\"\", fixed-set.tsv says 0440"},
		"a COPY with the wrong group": {func(f *fixture) {
			f.write("Dockerfile", strings.Replace(dockerText, "--chown=0:102", "--chown=0:103", 1))
		}, "sets --chown=\"0:103\""},
		"a tree COPY that sets a mode": {func(f *fixture) {
			f.write("Dockerfile", strings.Replace(dockerText, "COPY bases/chairman/fixed/root/ /", "COPY --chmod=0644 bases/chairman/fixed/root/ /", 1))
		}, "reaches directories too"},
		"a tree COPY gone": {func(f *fixture) {
			f.write("Dockerfile", strings.Replace(dockerText, "COPY bases/chairman/fixed/root/ /\n", "", 1))
		}, "no COPY of bases/chairman/fixed/root/ to /"},
		"a gid with no group": {func(f *fixture) {
			f.write("fixed/root/etc/passwd", f.body("fixed/root/etc/passwd")+"x:x:5:77::/:/bin/false\n")
			f.flush()
		}, "x has gid 77"},
		"an account with no shadow line": {func(f *fixture) {
			f.write("fixed/shadow/shadow", "root:*:1::::::\n")
			f.flush()
		}, "shadow: no line for agent"},
		"an agent with the wrong id": {func(f *fixture) {
			f.write("fixed/root/etc/passwd", strings.Replace(f.body("fixed/root/etc/passwd"), "agent:x:1001:1001", "agent:x:1002:1001", 1))
			f.flush()
		}, "agent must be uid and gid 1001"},
		"a group the COPY owner disagrees with": {func(f *fixture) {
			f.write("fixed/root/etc/group", strings.Replace(f.body("fixed/root/etc/group"), "_ssh:x:102", "_ssh:x:105", 1))
			f.flush()
		}, "COPY bases/chairman/fixed/suid/ssh-agent sets --chown=\"0:102\" and group _ssh is gid 105"},
		"a sudoers.d line changed": {func(f *fixture) {
			f.write("fixed/sudo/agent", "agent ALL=(ALL) ALL\n")
			f.flush()
		}, "fixed/sudo/agent must be exactly"},
		"a sudoers that includes nothing": {func(f *fixture) {
			f.write("fixed/sudoers/sudoers", "Defaults env_reset\n")
			f.flush()
		}, "must include /etc/sudoers.d"},
		"a statoverride that drifted": {func(f *fixture) {
			f.write("fixed/root/var/lib/dpkg/statoverride", "root messagebus 0755 /usr/lib/dbus-1.0/dbus-daemon-launch-helper\n")
			f.flush()
		}, "statoverride: /usr/lib/dbus-1.0/dbus-daemon-launch-helper"},
		"a committed machine id": {func(f *fixture) {
			f.write("fixed/root/etc/machine-id", "0123456789abcdef\n")
			f.flush()
		}, "holds an id"},
		"a profile PATH out of step": {func(f *fixture) {
			f.write("fixed/root/etc/profile.d/10-chairman-path.sh", "export PATH=/usr/bin\n")
			f.flush()
		}, "profile.d PATH is"},
		"a unit with no enable link": {func(f *fixture) {
			f.write("units/chairman-new.service", unit)
		}, "no enable link etc/systemd/system/multi-user.target.wants/chairman-new.service"},
		"an enable link with no [Install]": {func(f *fixture) {
			f.write("units/chairman-seed.service", "[Service]\nExecStart=/bin/true\n")
		}, "enables chairman-seed.service, and its [Install] names no such target"},
		"a mask that is not /dev/null": {func(f *fixture) {
			f.link("fixed/root/etc/systemd/system/sys-kernel-debug.mount", "/x")
			for i := range f.rows {
				if f.rows[i].path == "etc/systemd/system/sys-kernel-debug.mount" {
					f.rows[i].val = "/x"
				}
			}
			f.flush()
		}, "sys-kernel-debug.mount must be a link to /dev/null"},
		"getty not whiteout": {func(f *fixture) {
			_ = os.Remove(filepath.Join(f.dir, "fixed/whiteout/usr/lib/systemd/system/multi-user.target.wants/.wh.getty.target"))
		}, "whiteout of getty.target"},
		"a mode a tree COPY cannot lay": {func(f *fixture) {
			for i := range f.rows {
				if f.rows[i].path == "home/agent/.bashrc" {
					f.rows[i].mode = "0600"
				}
			}
			f.flush()
		}, "a tree COPY lays 0644 files"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			tc.mutate(f)
			expect(t, run(t, f, false), stateFindings, tc.want)
		})
	}
}

// The assembled image is held to the table, path by path.
func TestLayoutChecksNameWhatTheImageGotWrong(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*fixture, [][]tarEntry) [][]tarEntry
		want   string
	}{
		"a mode the COPY did not give": {func(f *fixture, l [][]tarEntry) [][]tarEntry {
			for i := range l[1] {
				if l[1][i].name == "usr/bin/ssh-agent" {
					l[1][i].mode = 0o755
				}
			}
			return l
		}, "image: usr/bin/ssh-agent is 0755, fixed-set.tsv says 2755"},
		"an owner it did not give": {func(f *fixture, l [][]tarEntry) [][]tarEntry {
			for i := range l[1] {
				if l[1][i].name == "etc/shadow" {
					l[1][i].gid = 0
				}
			}
			return l
		}, "etc/shadow is owned by 0:0, fixed-set.tsv says 0:42"},
		"a file a package layer overwrote": {func(f *fixture, l [][]tarEntry) [][]tarEntry {
			return append(l, []tarEntry{{name: "etc/sudoers", mode: 0o440, body: "from the next package"}})
		}, "image: etc/sudoers is not the file fixed-set.tsv records"},
		"a path missing": {func(f *fixture, l [][]tarEntry) [][]tarEntry {
			return append(l, []tarEntry{{name: "etc/.wh.subuid", mode: 0o644}})
		}, "image: etc/subuid is missing"},
		"a path of the wrong type": {func(f *fixture, l [][]tarEntry) [][]tarEntry {
			return append(l, []tarEntry{{name: "etc/.wh.alternatives", mode: 0o644}, {name: "etc/alternatives", mode: 0o644, body: "file"}})
		}, "image: etc/alternatives/cc is missing"},
		"a link retargeted": {func(f *fixture, l [][]tarEntry) [][]tarEntry {
			return append(l, []tarEntry{{name: "etc/alternatives/cc", typ: "l", mode: 0o777, to: "/usr/bin/gcc-12"}})
		}, "image: etc/alternatives/cc links to /usr/bin/gcc-12"},
		"a link whose target is not there": {func(f *fixture, l [][]tarEntry) [][]tarEntry {
			return append(l, []tarEntry{{name: "usr/bin/.wh.gcc", mode: 0o644}})
		}, "image: etc/alternatives/cc links to /usr/bin/gcc, which is not in the image"},
		"a unit that is not there": {func(f *fixture, l [][]tarEntry) [][]tarEntry {
			return append(l, []tarEntry{{name: "etc/systemd/system/.wh.chairman-seed.service", mode: 0o644}})
		}, "multi-user.target.wants/chairman-seed.service links to /etc/systemd/system/chairman-seed.service, which is not in the image"},
		"pip not removed": {func(f *fixture, l [][]tarEntry) [][]tarEntry { return l[:2] },
			"image: usr/local/lib/python3.14/site-packages/pip must be gone"},
		"a FROM line lost": {func(f *fixture, l [][]tarEntry) [][]tarEntry {
			l[0][0].body = "root:x:0:0:root:/root:/bin/bash\nsync:x:4:65534:sync:/bin:/bin/sync\n"
			return l
		}, "/etc/passwd lost the base's lines for sync"},
		"a machine id left behind": {func(f *fixture, l [][]tarEntry) [][]tarEntry {
			return append(l, []tarEntry{{name: "var/lib/dbus/machine-id", mode: 0o644, body: "abc"}})
		}, "image: /var/lib/dbus/machine-id holds an id"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			img := t.TempDir()
			layout(t, img, tc.mutate(f, f.imageLayers())...)
			expect(t, validate(f.root, img), stateFindings, tc.want)
		})
	}
}

func TestLinksAreFollowedThroughDirectoryLinksAndHardLinksAreFiles(t *testing.T) {
	f := newFixture(t)
	img := t.TempDir()
	l := f.imageLayers()
	// the enable link's target is reached through /lib -> usr/lib, and a hard link carries its target's mode
	l[0] = append(l[0], tarEntry{name: "usr/lib/systemd/system/u.service", mode: 0o644, body: "u"})
	l[1] = append(l[1], tarEntry{name: "etc/hl", typ: "h", mode: 0o644, to: "usr/lib/x"})
	f.rows = append(f.rows, spec{"etc/alternatives/via-lib", "l", "0777", "0:0", "/lib/systemd/system/u.service", ""})
	f.link("fixed/root/etc/alternatives/via-lib", "/lib/systemd/system/u.service")
	f.flush()
	l[1] = append(l[1], tarEntry{name: "etc/alternatives/via-lib", typ: "l", mode: 0o777, to: "/lib/systemd/system/u.service"})
	layout(t, img, l...)
	expect(t, validate(f.root, img), statePass)
}

func TestWhatCannotBeReadCannotRun(t *testing.T) {
	for name, mutate := range map[string]func(*fixture){
		"no Dockerfile":                    func(f *fixture) { _ = os.Remove(filepath.Join(f.dir, "Dockerfile")) },
		"a bad lock":                       func(f *fixture) { f.write("packages.lock", "[[package") },
		"no table":                         func(f *fixture) { _ = os.Remove(filepath.Join(f.dir, "fixed-set.tsv")) },
		"a short row":                      func(f *fixture) { f.write("fixed-set.tsv", "h\nx\ty\n") },
		"a mode in decimal":                func(f *fixture) { f.write("fixed-set.tsv", "h\na\tf\t99\t0:0\tabc\n") },
		"an owner with no gid":             func(f *fixture) { f.write("fixed-set.tsv", "h\na\tf\t0644\t0\tabc\n") },
		"an owner not a number":            func(f *fixture) { f.write("fixed-set.tsv", "h\na\tf\t0644\tx:0\tabc\n") },
		"a gid not a number":               func(f *fixture) { f.write("fixed-set.tsv", "h\na\tf\t0644\t0:x\tabc\n") },
		"a scripts table with a short row": func(f *fixture) { f.write("maintscripts.tsv", "h\na\tb\n") },
		"no scripts table":                 func(f *fixture) { _ = os.Remove(filepath.Join(f.dir, "maintscripts.tsv")) },
		"no debfiles table":                func(f *fixture) { _ = os.Remove(filepath.Join(f.dir, "fixed-debfiles.tsv")) },
		"a debfiles row that is short": func(f *fixture) {
			f.write("fixed-debfiles.tsv", "h\nsudo\t1.9\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			mutate(f)
			v := run(t, f, false)
			if name == "a debfiles row that is short" {
				expect(t, v, stateFindings, "fixed-debfiles.tsv")
				return
			}
			expect(t, v, stateCannot, "the base's tables could not be read")
		})
	}
	// a layout that is not one image, a layer that is not a tar
	f := newFixture(t)
	img := t.TempDir()
	if err := os.MkdirAll(filepath.Join(img, "chairman"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(img, "chairman", "index.json"), []byte(`{"manifests":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	expect(t, validate(f.root, img), stateCannot, "the assembled layout could not be read")
	layout(t, img, f.imageLayers()...)
	var idx struct {
		Manifests []struct{ Digest string } `json:"manifests"`
	}
	b, _ := os.ReadFile(filepath.Join(img, "chairman", "index.json"))
	_ = json.Unmarshal(b, &idx)
	var m struct{ Layers []struct{ Digest string } }
	mb, _ := os.ReadFile(filepath.Join(img, "chairman", "blobs", "sha256", strings.TrimPrefix(idx.Manifests[0].Digest, "sha256:")))
	_ = json.Unmarshal(mb, &m)
	if err := os.WriteFile(filepath.Join(img, "chairman", "blobs", "sha256", strings.TrimPrefix(m.Layers[0].Digest, "sha256:")), []byte("not a tar"), 0o644); err != nil {
		t.Fatal(err)
	}
	expect(t, validate(f.root, img), stateCannot, "the assembled layout could not be read")
}

func TestEveryProblemIsReported(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 100; i++ {
		f.write("packages.lock", f.body("packages.lock")+fmt.Sprintf("[[package]]\n  name = \"p%d\"\n  version = \"1\"\n", i))
	}
	res, err := Validate(f.root, t.TempDir())
	if err != nil || len(res.Problems) != 100 {
		t.Errorf("%d problems, %v", len(res.Problems), err)
	}
}

func TestTheDockerfileReaderJoinsContinuations(t *testing.T) {
	d := parseDockerfile("# c\nENV A=1 PATH=/a:/b\nCOPY --from=x /a \\\n  /b/\nCOPY a /b\nCOPY onlyone\nRUN x\n")
	if d.path != "/a:/b" || len(d.copies) != 2 || d.copies[0].from != "x" || d.copies[0].dst != "/b/" {
		t.Errorf("%+v", d)
	}
	if _, ok := d.copying("a", "/b"); !ok {
		t.Error("a file COPY to a name")
	}
	if _, ok := d.copying("/a", "/b/a"); ok {
		t.Error("a --from COPY is no context COPY")
	}
	if baseName("a/b/c") != "c" || baseName("c") != "c" {
		t.Error("baseName")
	}
}

// More ways the committed files can disagree, and what cannot be read.
func TestMoreSourceDisagreements(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*fixture)
		want   string
	}{
		"a package file deleted":   {func(f *fixture) { _ = os.Remove(filepath.Join(f.dir, "fixed/sudoers/sudoers")) }, "fixed/sudoers/sudoers"},
		"a committed file deleted": {func(f *fixture) { _ = os.Remove(filepath.Join(f.dir, "fixed/root/etc/subgid")) }, "fixed/root/etc/subgid"},
		"a link row that is not 0777": {func(f *fixture) {
			for i := range f.rows {
				if f.rows[i].path == "etc/alternatives/cc" {
					f.rows[i].mode = "0755"
				}
			}
			f.flush()
		}, "a link is 0777 root:root"},
		"no COPY of /work": {func(f *fixture) {
			f.write("Dockerfile", strings.Replace(dockerText, "COPY --chown=1001:1001 bases/chairman/fixed/work/ /work/\n", "", 1))
		}, "no COPY of bases/chairman/fixed/work/ to /work/"},
		"/work owned by the wrong user": {func(f *fixture) {
			f.write("Dockerfile", strings.Replace(dockerText, "COPY --chown=1001:1001 bases/chairman/fixed/work/", "COPY --chown=0:0 bases/chairman/fixed/work/", 1))
		}, "COPY bases/chairman/fixed/work/ sets --chown=\"0:0\", fixed-set.tsv says 1001:1001"},
		"the home tree COPY owned by the wrong user": {func(f *fixture) {
			f.write("Dockerfile", strings.Replace(dockerText, "COPY --chown=1001:1001 bases/chairman/fixed/home/agent/", "COPY --chown=1:1 bases/chairman/fixed/home/agent/", 1))
		}, "the tree COPY of bases/chairman/fixed/home/agent/ sets --chown=\"1:1\""},
		"the root tree COPY given an owner": {func(f *fixture) {
			f.write("Dockerfile", strings.Replace(dockerText, "COPY bases/chairman/fixed/root/ /", "COPY --chown=5:5 bases/chairman/fixed/root/ /", 1))
		}, "the tree COPY of bases/chairman/fixed/root/ sets --chown=\"5:5\""},
		"no COPY of the launcher": {func(f *fixture) {
			var kept []string
			for _, l := range strings.Split(dockerText, "\n") {
				if !strings.Contains(l, "dbus-daemon-launch-helper") {
					kept = append(kept, l)
				}
			}
			f.write("Dockerfile", strings.Join(kept, "\n"))
		}, "no COPY of bases/chairman/fixed/suid/dbus-daemon-launch-helper to /usr/lib/dbus-1.0/dbus-daemon-launch-helper"},
		"passwd gone": {func(f *fixture) { _ = os.Remove(filepath.Join(f.dir, "fixed/root/etc/passwd")) }, "fixed/root/etc/passwd is missing"},
		"a blank line and a malformed line in passwd": {func(f *fixture) {
			f.write("fixed/root/etc/passwd", "root:x:0:0:root:/root:/bin/bash\n\nbroken\nmessagebus:x:100:101::/nonexistent:/usr/sbin/nologin\nagent:x:1001:1001::/home/agent:/bin/bash\n")
			f.flush()
		}, "\"broken\" is not a well-formed line"},
		"a malformed group line": {func(f *fixture) {
			f.write("fixed/root/etc/group", f.body("fixed/root/etc/group")+"broken\n")
			f.flush()
		}, "group: \"broken\" is not a well-formed line"},
		"agent not a group": {func(f *fixture) {
			f.write("fixed/root/etc/group", strings.Replace(f.body("fixed/root/etc/group"), "agent:x:1001:\n", "", 1))
			f.flush()
		}, "group: agent must be gid 1001"},
		"a group with no gshadow line": {func(f *fixture) {
			f.write("fixed/shadow/gshadow", "root:*::\n")
			f.flush()
		}, "gshadow: no line for agent"},
		"no sub-range for agent": {func(f *fixture) {
			f.write("fixed/root/etc/subuid", "someone:1:1\n")
			f.flush()
		}, "fixed/root/etc/subuid must give agent a range"},
		"no _ssh group": {func(f *fixture) {
			f.write("fixed/root/etc/group", strings.Replace(f.body("fixed/root/etc/group"), "_ssh:x:102:\n", "", 1))
			f.flush()
		}, "group: no _ssh group, which bases/chairman/fixed/suid/ssh-agent is owned by"},
		"statoverride gone": {func(f *fixture) { _ = os.Remove(filepath.Join(f.dir, "fixed/root/var/lib/dpkg/statoverride")) }, "fixed/root/var/lib/dpkg/statoverride is missing"},
		"no machine-id":     {func(f *fixture) { _ = os.Remove(filepath.Join(f.dir, "fixed/root/etc/machine-id")) }, "fixed/root/etc/machine-id is missing"},
		"profile.d gone":    {func(f *fixture) { _ = os.Remove(filepath.Join(f.dir, "fixed/root/etc/profile.d/10-chairman-path.sh")) }, "10-chairman-path.sh is missing"},
		"no ENV PATH": {func(f *fixture) {
			f.write("Dockerfile", strings.Replace(dockerText, "ENV PATH=/opt/go/bin:/usr/local/bin:/usr/bin\n", "", 1))
		}, "Dockerfile has no ENV PATH"},
		"an enable link to the wrong unit file": {func(f *fixture) {
			f.link("fixed/root/etc/systemd/system/multi-user.target.wants/chairman-seed.service", "/etc/systemd/system/other.service")
			for i := range f.rows {
				if strings.HasSuffix(f.rows[i].path, "wants/chairman-seed.service") {
					f.rows[i].val = "/etc/systemd/system/other.service"
				}
			}
			f.flush()
		}, "links to /etc/systemd/system/other.service"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			tc.mutate(f)
			expect(t, run(t, f, false), stateFindings, tc.want)
		})
	}
}

func TestNoLockCannotRun(t *testing.T) {
	f := newFixture(t)
	_ = os.Remove(filepath.Join(f.dir, "packages.lock"))
	expect(t, run(t, f, false), stateCannot, "the base's tables could not be read")
}

// More ways the assembled image can disagree, and the shapes a layer can take.
func TestMoreLayoutDisagreements(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*fixture, [][]tarEntry) [][]tarEntry
		want   string
	}{
		"a path of another type": {func(f *fixture, l [][]tarEntry) [][]tarEntry {
			return append(l, []tarEntry{{name: "etc/.wh.alternatives", mode: 0o644}, {name: "etc/alternatives/cc", mode: 0o644, body: "a file where a link goes"}})
		}, "image: etc/alternatives/cc is a f, fixed-set.tsv says l"},
		"an opaque directory": {func(f *fixture, l [][]tarEntry) [][]tarEntry {
			return append(l, []tarEntry{{name: "etc/systemd/system/.wh..wh..opq", mode: 0o644}})
		}, "image: etc/systemd/system/kmod-static-nodes.service is missing"},
		"a hard link to nothing is a file": {func(f *fixture, l [][]tarEntry) [][]tarEntry {
			return append(l, []tarEntry{{name: "etc/subuid", typ: "h", mode: 0o644, to: "nowhere/at/all"}})
		}, "image: etc/subuid is not the file fixed-set.tsv records"},
		"a link with a relative target that is there": {func(f *fixture, l [][]tarEntry) [][]tarEntry {
			return append(l, []tarEntry{{name: "etc/alternatives/cc", typ: "l", mode: 0o777, to: "/usr/bin/gcc"}})
		}, ""},
		"a link loop": {func(f *fixture, l [][]tarEntry) [][]tarEntry {
			return append(l, []tarEntry{{name: "usr/bin/gcc", typ: "l", mode: 0o777, to: "gcc"}})
		}, "image: etc/alternatives/cc links to /usr/bin/gcc, which is not in the image"},
		"a relative link target": {func(f *fixture, l [][]tarEntry) [][]tarEntry {
			return append(l, []tarEntry{{name: "etc/alternatives/cc", typ: "l", mode: 0o777, to: "/usr/bin/gcc"}, {name: "etc/other", typ: "l", mode: 0o777, to: "../usr/bin/gcc"}})
		}, ""},
		"a fifo and a dot entry are not files": {func(f *fixture, l [][]tarEntry) [][]tarEntry {
			l[0] = append(l[0], tarEntry{name: "./", typ: "d", mode: 0o755}, tarEntry{name: "run/fifo", typ: "p"})
			return l
		}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			img := t.TempDir()
			f.rows = append(f.rows, spec{"etc/other", "l", "0777", "0:0", "../usr/bin/gcc", ""})
			f.link("fixed/root/etc/other", "../usr/bin/gcc")
			f.flush()
			layers := tc.mutate(f, f.imageLayers())
			layout(t, img, layers...)
			v := validate(f.root, img)
			if tc.want == "" {
				expect(t, v, statePass)
				return
			}
			expect(t, v, stateFindings, tc.want)
		})
	}
}

// A layout whose pieces are broken is the substrate's, not the tree's.
func TestABrokenLayoutCannotRun(t *testing.T) {
	for name, mutate := range map[string]func(t *testing.T, root string){
		"an index that is a directory": func(t *testing.T, root string) {
			_ = os.Remove(filepath.Join(root, "index.json"))
			_ = os.Mkdir(filepath.Join(root, "index.json"), 0o755)
		},
		"a missing manifest": func(t *testing.T, root string) {
			var idx struct {
				Manifests []struct{ Digest string } `json:"manifests"`
			}
			b, _ := os.ReadFile(filepath.Join(root, "index.json"))
			_ = json.Unmarshal(b, &idx)
			_ = os.Remove(blobPath(root, idx.Manifests[0].Digest))
		},
		"a missing layer": func(t *testing.T, root string) {
			for _, d := range layerDigests(t, root)[:1] {
				_ = os.Remove(blobPath(root, d))
			}
		},
		"a layer that is a bad gzip": func(t *testing.T, root string) {
			d := layerDigests(t, root)[0]
			if err := os.WriteFile(blobPath(root, d), []byte{0x1f, 0x8b, 1, 2, 3}, 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"a layer cut in the middle of a file": func(t *testing.T, root string) {
			d := layerDigests(t, root)[1]
			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)
			_ = tw.WriteHeader(&tar.Header{Name: "etc/passwd", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1000})
			_, _ = tw.Write([]byte("short"))
			if err := os.WriteFile(blobPath(root, d), buf.Bytes()[:600], 0o644); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			img := t.TempDir()
			layout(t, img, f.imageLayers()...)
			mutate(t, filepath.Join(img, "chairman"))
			expect(t, validate(f.root, img), stateCannot, "the assembled layout could not be read")
		})
	}
}

func layerDigests(t *testing.T, root string) []string {
	t.Helper()
	var idx struct {
		Manifests []struct{ Digest string } `json:"manifests"`
	}
	b, _ := os.ReadFile(filepath.Join(root, "index.json"))
	_ = json.Unmarshal(b, &idx)
	var m struct{ Layers []struct{ Digest string } }
	mb, _ := os.ReadFile(blobPath(root, idx.Manifests[0].Digest))
	_ = json.Unmarshal(mb, &m)
	var out []string
	for _, l := range m.Layers {
		out = append(out, l.Digest)
	}
	return out
}

func TestResolveOfTheRootIsTheRoot(t *testing.T) {
	img := &image{files: map[string]*entry{}}
	if p, ok := img.resolve("/", 0); !ok || p != "" {
		t.Errorf("%q %v", p, ok)
	}
}
