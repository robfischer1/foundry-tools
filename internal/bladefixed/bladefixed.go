// Package bladefixed is the validation of the BLADE bases' RUN-free images
// (atoms/bases/blade is its atom). blade-base, blade-python-claude and
// blade-differ were made by RUNs that also asserted, at build, the permission
// split that keeps the worker (agent) out of the grading runner; a RUN-free
// Dockerfile cannot assert, so this reads the assembled filesystem of each
// image instead and holds it to the split those RUNs checked:
//
//   - /opt/blade, gate, suite and keys are root:blade (0:999) 0550, so the agent,
//     which is not in group blade, cannot traverse to the runner or the suite
//   - the canary is 0440 root:blade and the suite holds nothing else
//   - agent is 1000:1000 with home /home/agent, blade is 999:999, and agent is
//     in no group but its own
//   - /run/blade is blade:blade 0775 and /work is agent:agent
//   - the push client and entrypoint are 0755, the aperture 0444
//   - blade-python-claude carries the pytest and claude binaries, executable;
//     blade-differ carries its entrypoint
//
// It cannot run the image, so the old `runuser -u agent -- cat` probes are their
// mode-and-owner equivalents here.
package bladefixed

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"dagger/foundry-tools/internal/chairmanfixed"
)

const (
	gateGID  = 999
	agentUID = 1000
)

// Bases are the images this validates, by the directory name the assemble
// capability lays each one's OCI layout under.
var Bases = []string{"blade-base", "blade-python-claude", "blade-differ"}

// Result is what a validation found.
type Result struct {
	Problems []string // sorted
	Checked  []string // the bases whose layout was read
	Checks   int
}

// Validate reads, for each base laid at imageDir/<base>, the image's filesystem.
// A base with no layout is not checked and not a problem: the assemble stood
// down, or this lane assembles no such base. An error is a layout that could
// not be read.
func Validate(imageDir string) (Result, error) {
	var res Result
	for _, b := range Bases {
		dir := filepath.Join(imageDir, b)
		if _, err := os.Stat(filepath.Join(dir, "index.json")); err != nil {
			continue
		}
		l, err := chairmanfixed.ReadLayout(dir)
		if err != nil {
			return Result{}, fmt.Errorf("the layout of %s could not be read: %w", b, err)
		}
		res.Checked = append(res.Checked, b)
		v := &verdict{base: b, l: l}
		v.split()
		v.accounts()
		v.scaffold()
		v.lane()
		res.Problems = append(res.Problems, v.problems...)
		res.Checks += v.checks
	}
	sort.Strings(res.Problems)
	return res, nil
}

type verdict struct {
	base     string
	l        chairmanfixed.Layout
	problems []string
	checks   int
}

func (v *verdict) problem(format string, a ...any) {
	v.problems = append(v.problems, v.base+": "+fmt.Sprintf(format, a...))
}

// want holds path to the type, mode and owner given.
func (v *verdict) want(path string, typ byte, mode, uid, gid int) {
	v.checks++
	n, ok := v.l.Files[path]
	switch {
	case !ok:
		v.problem("/%s is missing", path)
	case n.Type != typ:
		v.problem("/%s is a %c, want a %c", path, n.Type, typ)
	case n.Mode != mode || n.UID != uid || n.GID != gid:
		v.problem("/%s is %d:%d %04o, want %d:%d %04o", path, n.UID, n.GID, n.Mode, uid, gid, mode)
	}
}

// split is the reserved location's permissions: the line that keeps the worker
// out of the grading runner.
func (v *verdict) split() {
	for _, d := range []string{"opt/blade", "opt/blade/gate", "opt/blade/suite", "opt/blade/keys"} {
		v.want(d, 'd', 0o550, 0, gateGID)
	}
	v.want("opt/blade/suite/.blade-canary", 'f', 0o440, 0, gateGID)
	v.want("opt/blade/gate/blade_gate.py", 'f', 0o550, 0, gateGID)
	v.want("opt/blade/aperture.toml", 'f', 0o444, 0, gateGID)
	v.checks++
	var extra []string
	for p := range v.l.Files {
		if strings.HasPrefix(p, "opt/blade/suite/") && p != "opt/blade/suite/.blade-canary" {
			extra = append(extra, "/"+p)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		v.problem("the reserved suite directory is not empty: %s", strings.Join(extra, ", "))
	}
}

// accounts is useradd/groupadd's result: the two accounts, and the agent in no
// group of the gate user's.
func (v *verdict) accounts() {
	v.checks++
	pw, gr := v.l.Accounts["etc/passwd"], v.l.Accounts["etc/group"]
	if !hasLine(pw, "agent:x:1000:1000::/home/agent:/bin/bash") {
		v.problem("/etc/passwd has no agent as 1000:1000 with home /home/agent (%q)", lineOf(pw, "agent:"))
	}
	if !hasLine(pw, "blade:x:999:999::/opt/blade:/usr/sbin/nologin") {
		v.problem("/etc/passwd has no blade as 999:999 with home /opt/blade and no login (%q)", lineOf(pw, "blade:"))
	}
	v.checks++
	if !hasLine(gr, "agent:x:1000:") {
		v.problem("/etc/group has no group agent as 1000 with no other members (%q)", lineOf(gr, "agent:"))
	}
	for _, l := range strings.Split(gr, "\n") {
		f := strings.Split(l, ":")
		if len(f) == 4 {
			for _, m := range strings.Split(f[3], ",") {
				if m == "agent" {
					v.problem("agent is a member of group %s: the split is open", f[0])
				}
			}
		}
	}
	if !hasLine(gr, "blade:x:999:") {
		v.problem("/etc/group has no group blade as 999 with no members (%q)", lineOf(gr, "blade:"))
	}
}

// scaffold is blade-base's own files: the run directory, the worker's, the clients.
func (v *verdict) scaffold() {
	v.want("run/blade", 'd', 0o775, gateGID, gateGID)
	v.want("work", 'd', 0o755, agentUID, agentUID)
	v.want("home/agent", 'd', 0o755, agentUID, agentUID)
	v.want("usr/local/bin/blade-push", 'f', 0o755, 0, 0)
	v.want("usr/local/bin/blade-entrypoint", 'f', 0o755, 0, 0)
}

// lane is what each image adds to the scaffold.
func (v *verdict) lane() {
	switch v.base {
	case "blade-python-claude":
		v.want("opt/blade/gate/venv/bin/pytest", 'f', 0o755, 0, gateGID)
		v.want("usr/local/bin/claude", 'f', 0o755, 0, 0)
	case "blade-differ":
		v.want("usr/local/bin/blade-differ", 'f', 0o755, 0, 0)
	}
}

func hasLine(text, line string) bool {
	for _, l := range strings.Split(text, "\n") {
		if l == line {
			return true
		}
	}
	return false
}

func lineOf(text, prefix string) string {
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}
