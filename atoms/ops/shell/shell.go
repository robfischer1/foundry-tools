// Package opsshell is the atom ops:shell: shellcheck over every tracked script
// at the gating severity. One file holds the atom and both runners execute it
// (lld.md FN-085).
package opsshell

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. shellcheck is the pinned binary the tools container carries.
var Def = atomkit.Def{
	ID: "ops:shell", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every tracked shell script passes shellcheck at severity error; the warning count is reported, not gated.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"tool:shellcheck"},
	Check:     check,
}

const id = "ops:shell"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// binaryProbe is how many leading bytes git looks at for a NUL to call a file
// binary (git's buffer_is_binary), which `git grep -I` skips.
const binaryProbe = 8000

// firstLines reads, for every committable file that could be a script, its
// first line when it starts "#!": the map checks.OpsFirstLines read out of
// `git grep -I -n -z -E '^#!'`. The chain asked git; the same question is asked
// of the files because the tree the binary reads is not an index, and the
// answers agree on what matters: a binary file (a NUL in its first 8000 bytes)
// is skipped as -I skips it, and only line 1 counts. A symlink is skipped: git
// greps the link's target text, which is not a script. The files are opened
// through Input.FS (the disk when nil), so a test says what a file that will
// not open does.
func firstLines(in atomkit.In) (map[string]string, error) {
	fsys := in.FS
	if fsys == nil {
		fsys = os.DirFS(in.Root)
	}
	out := map[string]string{}
	for _, f := range in.Committable {
		switch filepath.Ext(f) {
		case ".tmpl", ".zsh":
			// OpsShellFiles never reads these two kinds' first lines.
			continue
		}
		line, err := shebangOf(fsys, in.Root, f)
		if err != nil {
			return nil, err
		}
		if line != "" {
			out[f] = line
		}
	}
	return out, nil
}

// shebangOf is a regular file's first line when it starts "#!", and "" when it
// does not (a line that starts "#!" is never empty, so "" says no).
func shebangOf(fsys fs.FS, root, rel string) (string, error) {
	fi, err := os.Lstat(filepath.Join(root, rel))
	if err != nil || !fi.Mode().IsRegular() {
		return "", err
	}
	fh, err := fsys.Open(rel)
	if err != nil {
		return "", err
	}
	defer func() { _ = fh.Close() }()
	head, err := io.ReadAll(io.LimitReader(fh, binaryProbe))
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(head, 0) >= 0 || !bytes.HasPrefix(head, []byte("#!")) {
		return "", nil
	}
	line, _, _ := strings.Cut(string(head), "\n")
	return line, nil
}

// check: shellcheck over every tracked script, at the gating severity, with
// the report severity counted and never gating.
//
// THE GATE IS `error`, a deliberate floor (atoms_ops.go has the measurement).
// A shebang tells shellcheck its dialect; a SOURCED FRAGMENT does not, so it is
// told bash. shellcheck is the pinned binary the container carries; the chain
// ran the shellcheck-py wheel through uvx, unpinned.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if stop := atomlib.OpsSurface(row, in); stop != nil {
		return *stop
	}
	lines, err := firstLines(in)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), "shell: could not read the scripts' first lines: "+err.Error())
	}
	shebang, fragments := checks.OpsShellFiles(checks.OpsFiles(in.Committable), lines)
	if len(shebang) == 0 && len(fragments) == 0 {
		return atomlib.PhaseAbsent(row, "no shell script in this tree")
	}
	if stop := atomlib.ProbeTool(ctx, row, in, "shellcheck", "--version"); stop != nil {
		return *stop
	}
	// check runs shellcheck over every batch of both groups at one severity.
	check := func(severity string) (string, int, string) {
		var out strings.Builder
		rc := 0
		for _, group := range []struct {
			files []string
			flags []string
		}{{shebang, nil}, {fragments, []string{"-s", "bash"}}} {
			for _, batch := range checks.OpsBatches(group.files) {
				args := append(append([]string{"-S", severity}, group.flags...), "-f", "gcc")
				o, c := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "shellcheck", Args: append(args, batch...), Both: true})
				if c < 0 {
					return "", 0, o
				}
				out.WriteString(o)
				rc = max(rc, c)
			}
		}
		return out.String(), rc, ""
	}
	gate, rc, never := check("error")
	if never != "" {
		return atomlib.NeverRan(row, never)
	}
	report, _, never := check("warning")
	if never != "" {
		return atomlib.NeverRan(row, never)
	}
	state, out := atomlib.OpsSettled("shell", rc, fmt.Sprintf("shell: %d script(s), gating at severity error\n%s", len(shebang)+len(fragments), gate))
	out += fmt.Sprintf("\nshellcheck -S warning: %d finding(s) — reported, not gating", checks.OpsDebt(report))
	return checks.VerdictOf(row, state, out)
}
