package chairmanfixed

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// THE FIXED-FILE SET'S TABLES. fixed-set.tsv is what the Dockerfile's COPYs and
// the locked packages must add up to, path by path; maintscripts.tsv and
// fixed-debfiles.tsv are what the lock was reviewed against.

// setRow is one row of fixed-set.tsv.
type setRow struct {
	path, typ string
	mode      int // permission bits with setuid, setgid and sticky
	uid, gid  int
	val       string // the link target (l), the sha256 (f)
}

// source is bases/chairman as committed.
type source struct {
	dir        string
	docker     dockerfile
	lock       map[string]string // package -> version
	set        []setRow
	scripts    map[string]string // package -> version, from maintscripts.tsv
	debfiles   [][]string        // package, version, path, sha256
	problems   []string
	checkCount int
}

func (s *source) problem(format string, a ...any) {
	s.problems = append(s.problems, fmt.Sprintf(format, a...))
}

// tsv reads a tab-separated table without its header line.
func tsv(path string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rows [][]string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		if line := sc.Text(); line != "" {
			rows = append(rows, strings.Split(line, "\t"))
		}
	}
	return rows, sc.Err()
}

// load reads everything the checks need; a file that is missing or unreadable is
// returned as the error (the atom's could-not-run).
func load(root string) (*source, error) {
	s := &source{dir: root, lock: map[string]string{}, scripts: map[string]string{}}
	body, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		return nil, err
	}
	s.docker = parseDockerfile(string(body))
	raw, err := os.ReadFile(filepath.Join(root, "packages.lock"))
	if err != nil {
		return nil, err
	}
	var lock struct {
		Package []struct{ Name, Version string }
	}
	if _, err := toml.Decode(string(raw), &lock); err != nil {
		return nil, fmt.Errorf("packages.lock: %w", err)
	}
	for _, p := range lock.Package {
		s.lock[p.Name] = p.Version
	}
	rows, err := tsv(filepath.Join(root, "fixed-set.tsv"))
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if len(r) != 5 {
			return nil, fmt.Errorf("fixed-set.tsv: %q has %d columns, want 5", r, len(r))
		}
		row := setRow{path: r[0], typ: r[1], val: r[4]}
		if r[1] != "x" {
			m, err := strconv.ParseUint(r[2], 8, 32)
			if err != nil {
				return nil, fmt.Errorf("fixed-set.tsv: %s: mode %q is not octal", r[0], r[2])
			}
			row.mode = int(m)
			u, g, ok := strings.Cut(r[3], ":")
			if row.uid, err = strconv.Atoi(u); err != nil || !ok {
				return nil, fmt.Errorf("fixed-set.tsv: %s: owner %q is not uid:gid", r[0], r[3])
			}
			if row.gid, err = strconv.Atoi(g); err != nil {
				return nil, fmt.Errorf("fixed-set.tsv: %s: owner %q is not uid:gid", r[0], r[3])
			}
		}
		s.set = append(s.set, row)
	}
	scripts, err := tsv(filepath.Join(root, "maintscripts.tsv"))
	if err != nil {
		return nil, err
	}
	for _, r := range scripts {
		if len(r) != 4 {
			return nil, fmt.Errorf("maintscripts.tsv: %q has %d columns, want 4", r, len(r))
		}
		s.scripts[r[0]] = r[1]
	}
	if s.debfiles, err = tsv(filepath.Join(root, "fixed-debfiles.tsv")); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *source) read(rel string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(s.dir, rel))
	return string(b), err == nil
}

func fileSHA(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// committedAt names where a path of the fixed set is committed and the COPY that
// lays it: the group directory under fixed/, the file there, and the Dockerfile
// source and destination (a directory source ends in a slash).
type committedAt struct {
	file     string // under bases/chairman
	src, dst string // as the Dockerfile spells them
	tree     bool   // laid by a tree COPY, whose files carry the defaults
	owner    string // the tree COPY's --chown, when it sets one
}

const fx = "bases/chairman/fixed/"

func where(path string) committedAt {
	switch {
	case path == "etc/shadow" || path == "etc/gshadow":
		return committedAt{file: "fixed/shadow/" + baseName(path), src: fx + "shadow/" + baseName(path), dst: "/etc/"}
	case path == "etc/sudoers":
		return committedAt{file: "fixed/sudoers/sudoers", src: fx + "sudoers/sudoers", dst: "/etc/sudoers"}
	case path == "etc/sudoers.d/agent":
		return committedAt{file: "fixed/sudo/agent", src: fx + "sudo/agent", dst: "/etc/sudoers.d/agent"}
	case path == "usr/bin/ssh-agent" || path == "usr/lib/dbus-1.0/dbus-daemon-launch-helper":
		return committedAt{file: "fixed/suid/" + baseName(path), src: fx + "suid/" + baseName(path), dst: "/" + path}
	case strings.HasPrefix(path, "home/agent/"):
		return committedAt{file: "fixed/" + path, src: fx + "home/agent/", dst: "/home/agent/", tree: true, owner: "1001:1001"}
	default:
		return committedAt{file: "fixed/root/" + path, src: fx + "root/", dst: "/", tree: true}
	}
}

// checkLockVsScripts holds the lock to the table of maintainer-script hashes: a
// package added, dropped or moved to another version leaves the table stale, and
// the table is how a changed postinst is noticed.
func (s *source) checkLockVsScripts() {
	s.checkCount++
	for p, v := range s.lock {
		switch got, ok := s.scripts[p]; {
		case !ok:
			s.problem("maintscripts.tsv: %s %s is locked and has no row: run bases/chairman/measure.py and review the diff", p, v)
		case got != v:
			s.problem("maintscripts.tsv: %s is locked at %s and was measured at %s: run bases/chairman/measure.py and review the diff", p, v, got)
		}
	}
	for p, v := range s.scripts {
		if _, ok := s.lock[p]; !ok {
			s.problem("maintscripts.tsv: %s %s is no longer locked: run bases/chairman/measure.py", p, v)
		}
	}
}

// checkDebFiles holds the three files taken from the locked packages to the lock
// and to their hashes.
func (s *source) checkDebFiles() {
	s.checkCount++
	for _, r := range s.debfiles {
		if len(r) != 4 {
			s.problem("fixed-debfiles.tsv: %q has %d columns, want 4", r, len(r))
			continue
		}
		pkg, ver, path, sha := r[0], r[1], r[2], r[3]
		if locked := s.lock[pkg]; locked != ver {
			s.problem("fixed-debfiles.tsv: %s is locked at %q and %s was taken from %s: run bases/chairman/measure.py and review the diff", pkg, locked, path, ver)
		}
		got, err := fileSHA(filepath.Join(s.dir, where(path).file))
		switch {
		case err != nil:
			s.problem("%s: %v", where(path).file, err)
		case got != sha:
			s.problem("%s is not the file the %s package ships (sha256 %.12s, want %.12s): run bases/chairman/measure.py", where(path).file, pkg, got, sha)
		}
	}
}

// checkCommitted holds fixed-set.tsv to the committed files: a file's hash, a
// link's target, and that the Dockerfile's COPY gives the file the mode and owner
// the row says (a tree COPY lays the defaults; anything else needs a COPY of its
// own with --chmod and --chown).
func (s *source) checkCommitted() {
	s.checkCount++
	for _, r := range s.set {
		if r.typ == "x" || r.typ == "d" && !strings.HasPrefix(r.path, "work") && !strings.HasPrefix(r.path, "home/agent") {
			continue
		}
		w := where(r.path)
		switch r.typ {
		case "l":
			got, err := os.Readlink(filepath.Join(s.dir, w.file))
			switch {
			case err != nil:
				s.problem("%s: %v", w.file, err)
			case got != r.val:
				s.problem("%s links to %s, fixed-set.tsv says %s", w.file, got, r.val)
			}
			if r.mode != 0o777 || r.uid != 0 || r.gid != 0 {
				s.problem("fixed-set.tsv: %s: a link is 0777 root:root", r.path)
			}
		case "f":
			got, err := fileSHA(filepath.Join(s.dir, w.file))
			switch {
			case err != nil:
				s.problem("%s: %v", w.file, err)
			case got != r.val:
				s.problem("%s does not match fixed-set.tsv (sha256 %.12s, want %.12s)", w.file, got, r.val)
			}
			s.checkCopyFlags(r, w)
		case "d":
			// /work and the agent's home are laid by a tree COPY with --chown
			tree := map[string]string{"work": fx + "work/", "home/agent": fx + "home/agent/"}[r.path]
			dst := "/" + r.path + "/"
			c, ok := s.docker.copyingTree(tree, dst)
			switch {
			case !ok:
				s.problem("Dockerfile: no COPY of %s to %s", tree, dst)
			case c.chown != fmt.Sprintf("%d:%d", r.uid, r.gid):
				s.problem("Dockerfile: COPY %s sets --chown=%q, fixed-set.tsv says %d:%d", tree, c.chown, r.uid, r.gid)
			}
		}
	}
}

// checkCopyFlags is the Dockerfile half of a file row.
func (s *source) checkCopyFlags(r setRow, w committedAt) {
	owner := fmt.Sprintf("%d:%d", r.uid, r.gid)
	if w.tree {
		c, ok := s.docker.copyingTree(w.src, w.dst)
		switch {
		case !ok:
			s.problem("Dockerfile: no COPY of %s to %s", w.src, w.dst)
		case c.chmod != "":
			s.problem("Dockerfile: the tree COPY of %s sets --chmod=%s, which reaches directories too", w.src, c.chmod)
		case r.mode != 0o644:
			s.problem("fixed-set.tsv: %s is %04o: a tree COPY lays 0644 files, give it a COPY of its own", r.path, r.mode)
		case owner != "0:0" && c.chown != owner, owner == "0:0" && c.chown != "":
			s.problem("Dockerfile: the tree COPY of %s sets --chown=%q, fixed-set.tsv says %s for %s", w.src, c.chown, owner, r.path)
		}
		return
	}
	c, ok := s.docker.copying(w.src, "/"+r.path)
	switch {
	case !ok:
		s.problem("Dockerfile: no COPY of %s to /%s", w.src, r.path)
	case atoiOctal(c.chmod) != r.mode:
		s.problem("Dockerfile: COPY %s sets --chmod=%q, fixed-set.tsv says %04o", w.src, c.chmod, r.mode)
	case (c.chown == "" && owner != "0:0") || (c.chown != "" && c.chown != owner):
		s.problem("Dockerfile: COPY %s sets --chown=%q, fixed-set.tsv says %s", w.src, c.chown, owner)
	}
}

func atoiOctal(s string) int {
	n, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return -1
	}
	return int(n)
}

// account is a passwd or group line.
type account struct{ name, id, gid string }

func (s *source) accounts(rel string) (map[string]account, []string) {
	body, ok := s.read(rel)
	if !ok {
		s.problem("%s is missing", rel)
		return nil, nil
	}
	out := map[string]account{}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if l == "" {
			continue
		}
		f := strings.Split(l, ":")
		lines = append(lines, l)
		if strings.HasSuffix(rel, "passwd") && len(f) == 7 {
			out[f[0]] = account{f[0], f[2], f[3]}
		} else if strings.HasSuffix(rel, "group") && len(f) == 4 {
			out[f[0]] = account{f[0], f[2], ""}
		} else {
			s.problem("%s: %q is not a well-formed line", rel, l)
		}
	}
	return out, lines
}

// checkAccounts holds the whole account files to each other and to the owners
// the Dockerfile's COPYs name.
func (s *source) checkAccounts() {
	s.checkCount++
	passwd, _ := s.accounts("fixed/root/etc/passwd")
	group, _ := s.accounts("fixed/root/etc/group")
	if passwd == nil || group == nil {
		return
	}
	gids := map[string]string{}
	for _, g := range group {
		gids[g.id] = g.name
	}
	names := make([]string, 0, len(passwd))
	for n := range passwd {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if _, ok := gids[passwd[n].gid]; !ok {
			s.problem("passwd: %s has gid %s and group has no such gid", n, passwd[n].gid)
		}
	}
	if a, ok := passwd["agent"]; !ok || a.id != "1001" || a.gid != "1001" {
		s.problem("passwd: agent must be uid and gid 1001")
	}
	if g, ok := group["agent"]; !ok || g.id != "1001" {
		s.problem("group: agent must be gid 1001")
	}
	if body, _ := s.read("fixed/root/etc/passwd"); !strings.Contains(body, "agent:x:1001:1001:") || !strings.Contains(body, ":/home/agent:/bin/bash\n") {
		s.problem("passwd: agent must have /home/agent and /bin/bash")
	}
	// every account has a shadow line and every group a gshadow line
	shadow, _ := s.read("fixed/shadow/shadow")
	have := map[string]bool{}
	for _, l := range strings.Split(shadow, "\n") {
		have[strings.SplitN(l, ":", 2)[0]] = true
	}
	for _, n := range names {
		if !have[n] {
			s.problem("shadow: no line for %s", n)
		}
	}
	gshadow, _ := s.read("fixed/shadow/gshadow")
	haveG := map[string]bool{}
	for _, l := range strings.Split(gshadow, "\n") {
		haveG[strings.SplitN(l, ":", 2)[0]] = true
	}
	for n := range group {
		if !haveG[n] {
			s.problem("gshadow: no line for %s", n)
		}
	}
	for _, f := range []string{"fixed/root/etc/subuid", "fixed/root/etc/subgid"} {
		if body, ok := s.read(f); !ok || !strings.HasPrefix(body, "agent:") {
			s.problem("%s must give agent a range", f)
		}
	}
	// the owners the COPYs name are the groups the account files define
	want := map[string]string{fx + "shadow/shadow": "shadow", fx + "suid/ssh-agent": "_ssh", fx + "suid/dbus-daemon-launch-helper": "messagebus"}
	for src, name := range want {
		g, ok := group[name]
		if !ok {
			s.problem("group: no %s group, which %s is owned by", name, src)
			continue
		}
		var c copyLine
		var found bool
		for _, cp := range s.docker.copies {
			for _, x := range cp.srcs {
				if x == src {
					c, found = cp, true
				}
			}
		}
		if !found {
			continue // checkCommitted names the missing COPY
		}
		if c.chown != "0:"+g.id {
			s.problem("Dockerfile: COPY %s sets --chown=%q and group %s is gid %s", src, c.chown, name, g.id)
		}
	}
}

// checkSudo holds the sudoers files to what visudo -c would read in them.
func (s *source) checkSudo() {
	s.checkCount++
	if body, ok := s.read("fixed/sudo/agent"); !ok || body != "agent ALL=(ALL) NOPASSWD:ALL\n" {
		s.problem("fixed/sudo/agent must be exactly `agent ALL=(ALL) NOPASSWD:ALL`")
	}
	if body, ok := s.read("fixed/sudoers/sudoers"); !ok || !strings.Contains(body, "\n@includedir /etc/sudoers.d\n") {
		s.problem("fixed/sudoers/sudoers must include /etc/sudoers.d")
	}
}

// checkStatoverride holds dpkg's statoverride to the modes the COPYs give.
func (s *source) checkStatoverride() {
	s.checkCount++
	body, ok := s.read("fixed/root/var/lib/dpkg/statoverride")
	if !ok {
		s.problem("fixed/root/var/lib/dpkg/statoverride is missing")
		return
	}
	got := map[string]string{}
	for _, l := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		f := strings.Fields(l)
		if len(f) == 4 {
			got[f[3]] = f[0] + " " + f[1] + " " + f[2]
		}
	}
	group, _ := s.accounts("fixed/root/etc/group")
	for _, o := range []struct{ path, owner, mode, group string }{
		{"/usr/bin/ssh-agent", "root", "2755", "_ssh"},
		{"/usr/lib/dbus-1.0/dbus-daemon-launch-helper", "root", "4754", "messagebus"},
	} {
		if want := o.owner + " " + o.group + " " + o.mode; got[o.path] != want {
			s.problem("statoverride: %s is %q, want %q", o.path, got[o.path], want)
		}
		if _, ok := group[o.group]; !ok {
			s.problem("group: no %s group", o.group)
		}
	}
}

// checkMachineID: the id is generated at first boot; a committed one would be
// every Chairman's.
func (s *source) checkMachineID() {
	s.checkCount++
	_ = filepath.WalkDir(filepath.Join(s.dir, "fixed"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "machine-id" {
			return nil
		}
		if fi, err := os.Stat(p); err == nil && fi.Size() != 0 {
			s.problem("%s holds an id: a machine id is generated at first boot, commit it empty", strings.TrimPrefix(p, s.dir+"/"))
		}
		return nil
	})
	if _, err := os.Stat(filepath.Join(s.dir, "fixed/root/etc/machine-id")); err != nil {
		s.problem("fixed/root/etc/machine-id is missing: commit it empty so systemd initialises it at first boot")
	}
}

// checkProfilePath holds the login shells' PATH to the image's.
func (s *source) checkProfilePath() {
	s.checkCount++
	body, ok := s.read("fixed/root/etc/profile.d/10-chairman-path.sh")
	if !ok {
		s.problem("fixed/root/etc/profile.d/10-chairman-path.sh is missing")
		return
	}
	if s.docker.path == "" {
		s.problem("Dockerfile has no ENV PATH")
	} else if body != "export PATH="+s.docker.path+"\n" {
		s.problem("profile.d PATH is %q and the Dockerfile's ENV PATH is %q", strings.TrimSpace(body), s.docker.path)
	}
}

// installLine reads a unit's [Install] section's WantedBy and RequiredBy.
func installTargets(body string) []string {
	var out []string
	in := false
	for _, l := range strings.Split(body, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "[") {
			in = l == "[Install]"
			continue
		}
		if !in {
			continue
		}
		for _, k := range []string{"WantedBy=", "RequiredBy="} {
			if v, ok := strings.CutPrefix(l, k); ok {
				out = append(out, strings.Fields(v)...)
			}
		}
	}
	return out
}

// masks are the units systemd cannot run in a container.
var masks = []string{"kmod-static-nodes.service", "systemd-udevd.service", "sys-kernel-config.mount", "sys-kernel-debug.mount", "sys-kernel-tracing.mount"}

// checkUnits holds each chairman unit's [Install] and the enable links to each
// other, the masks to /dev/null, and getty's wants link to its whiteout.
func (s *source) checkUnits() {
	s.checkCount++
	units, _ := filepath.Glob(filepath.Join(s.dir, "units", "chairman-*"))
	want := map[string]string{} // link path -> unit
	for _, u := range units {
		name := filepath.Base(u)
		body, _ := os.ReadFile(u)
		for _, t := range installTargets(string(body)) {
			want["etc/systemd/system/"+t+".wants/"+name] = name
		}
	}
	for _, r := range s.set {
		if r.typ == "l" && strings.HasPrefix(r.path, "etc/systemd/system/") && strings.Contains(r.path, ".wants/chairman-") {
			unit := baseName(r.path)
			if want[r.path] != unit {
				s.problem("fixed-set.tsv: %s enables %s, and its [Install] names no such target", r.path, unit)
			}
			if r.val != "/etc/systemd/system/"+unit {
				s.problem("fixed-set.tsv: %s links to %s", r.path, r.val)
			}
		}
	}
	have := map[string]bool{}
	for _, r := range s.set {
		have[r.path] = true
	}
	var missing []string
	for l, unit := range want {
		if !have[l] {
			missing = append(missing, fmt.Sprintf("%s (for %s's [Install])", l, unit))
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		s.problem("fixed-set.tsv has no enable link %s", m)
	}
	for _, m := range masks {
		p := "etc/systemd/system/" + m
		found := false
		for _, r := range s.set {
			if r.path == p {
				found = r.typ == "l" && r.val == "/dev/null"
			}
		}
		if !found {
			s.problem("fixed-set.tsv: %s must be a link to /dev/null", p)
		}
	}
	if _, err := os.Lstat(filepath.Join(s.dir, "fixed/whiteout/usr/lib/systemd/system/multi-user.target.wants/.wh.getty.target")); err != nil {
		s.problem("the whiteout of getty.target in multi-user.target.wants is missing")
	}
}

// sourceChecks runs every check that reads only the committed files.
func (s *source) sourceChecks() {
	s.checkLockVsScripts()
	s.checkDebFiles()
	s.checkCommitted()
	s.checkAccounts()
	s.checkSudo()
	s.checkStatoverride()
	s.checkMachineID()
	s.checkProfilePath()
	s.checkUnits()
}
