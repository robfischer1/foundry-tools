package atoms

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// THE OPS ATOMS THAT EXEC A TOOL over the tracked files: ops:shell, ops:chezmoi
// and (tools_flux.go) ops:flux. They share the chain's shape (atoms_ops.go
// opsAtom): the ops surface first, the phase's own absence second, the tool's
// version probe third, the work last. The chain provisioned the tool BEFORE the
// phase could say it had nothing to do; here the order is the other way round,
// so an absent atom runs no program (tools_ops_test.go holds that).

// opsPhaseAbsent is a phase that found its facet missing, in the chain's words.
func opsPhaseAbsent(a checks.AtomDef, why string) checks.Verdict {
	return checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - "+why)
}

// probeTool runs the tool's version probe and answers a verdict when it did not
// pass: the chain's provisioning exec, which carried the default Expect.
func probeTool(ctx context.Context, a checks.AtomDef, in Input, name string, args ...string) *checks.Verdict {
	out, code := in.run(ctx, Cmd{Dir: in.Root, Name: name, Args: args, Both: true})
	if code == 0 {
		return nil
	}
	v := unprovisioned(a, fmt.Sprintf("%s %s exited %d: %s", name, strings.Join(args, " "), code, strings.TrimSpace(out)))
	return &v
}

// binaryProbe is how many leading bytes git looks at for a NUL to call a file
// binary (git's buffer_is_binary), which `git grep -I` skips.
const binaryProbe = 8000

// firstLines reads, for every committable file that could be a script, its
// first line when it starts "#!": the map checks.OpsFirstLines read out of
// `git grep -I -n -z -E '^#!'`. The chain asked git; the same question is asked
// of the files because the tree the binary reads is not an index, and the
// answers agree on what matters: a binary file (a NUL in its first 8000 bytes)
// is skipped as -I skips it, and only line 1 counts. A symlink is skipped: git
// greps the link's target text, which is not a script.
func firstLines(in Input) (map[string]string, error) {
	out := map[string]string{}
	buf := make([]byte, binaryProbe)
	for _, f := range in.Committable {
		switch filepath.Ext(f) {
		case ".tmpl", ".zsh":
			// OpsShellFiles never reads these two kinds' first lines.
			continue
		}
		line, ok, err := shebangOf(filepath.Join(in.Root, f), buf)
		if err != nil {
			return nil, err
		}
		if ok {
			out[f] = line
		}
	}
	return out, nil
}

// shebangOf is a regular file's first line when it starts "#!".
func shebangOf(path string, buf []byte) (string, bool, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return "", false, err
	}
	if !fi.Mode().IsRegular() {
		return "", false, nil
	}
	fh, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = fh.Close() }()
	n, err := io.ReadFull(fh, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", false, err
	}
	head := buf[:n]
	if bytes.IndexByte(head, 0) >= 0 || !bytes.HasPrefix(head, []byte("#!")) {
		return "", false, nil
	}
	line, _, _ := strings.Cut(string(head), "\n")
	return line, true, nil
}

// opsShell: shellcheck over every tracked script, at the gating severity, with
// the report severity counted and never gating.
//
// THE GATE IS `error`, a deliberate floor (atoms_ops.go has the measurement).
// A shebang tells shellcheck its dialect; a SOURCED FRAGMENT does not, so it is
// told bash. shellcheck is the pinned binary the container carries; the chain
// ran the shellcheck-py wheel through uvx, unpinned.
func opsShell(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if stop := opsSurface(a, in); stop != nil {
		return *stop
	}
	lines, err := firstLines(in)
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), "shell: could not read the scripts' first lines: "+err.Error())
	}
	shebang, fragments := checks.OpsShellFiles(checks.OpsFiles(in.Committable), lines)
	if len(shebang) == 0 && len(fragments) == 0 {
		return opsPhaseAbsent(a, "no shell script in this tree")
	}
	if stop := probeTool(ctx, a, in, "shellcheck", "--version"); stop != nil {
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
				o, c := in.run(ctx, Cmd{Dir: in.Root, Name: "shellcheck", Args: append(args, batch...), Both: true})
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
		return neverRan(a, never)
	}
	report, _, never := check("warning")
	if never != "" {
		return neverRan(a, never)
	}
	state, out := opsSettled("shell", rc, fmt.Sprintf("shell: %d script(s), gating at severity error\n%s", len(shebang)+len(fragments), gate))
	out += fmt.Sprintf("\nshellcheck -S warning: %d finding(s) — reported, not gating", checks.OpsDebt(report))
	return checks.VerdictOf(a, state, out)
}

// opsChezmoi: every tracked *.tmpl of a chezmoi source tree renders, the check a
// dotfiles tree has no other way to make.
//
// `--source .` IS LOAD-BEARING: chezmoi resolves `include` against its SOURCE
// directory, which defaults to ~/.local/share/chezmoi. Rendering has no host
// config, so a template that reads custom data fails here and works on the host
// that has it; none does today.
func opsChezmoi(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if stop := opsSurface(a, in); stop != nil {
		return *stop
	}
	templates := checks.OpsChezmoiTemplates(checks.OpsFiles(in.Committable))
	if len(templates) == 0 {
		return opsPhaseAbsent(a, "no chezmoi template in this tree")
	}
	if stop := probeTool(ctx, a, in, "chezmoi", "--version"); stop != nil {
		return *stop
	}
	t := in.tree()
	var out strings.Builder
	rc := 0
	for _, tmpl := range templates {
		src, err := t.read(tmpl)
		if err != nil {
			return neverRan(a, err.Error())
		}
		o, c := in.run(ctx, Cmd{Dir: in.Root, Name: "chezmoi", Args: []string{"--source", ".", "execute-template"}, Stdin: src, Both: true})
		if c < 0 {
			return neverRan(a, o)
		}
		if c != 0 {
			rc = 1
			fmt.Fprintf(&out, "FAIL %s\n    %s\n", tmpl, strings.ReplaceAll(strings.TrimRight(o, "\n"), "\n", "\n    "))
		}
	}
	fmt.Fprintf(&out, "chezmoi: %d template(s) checked", len(templates))
	state, report := opsSettled("chezmoi", rc, out.String())
	return checks.VerdictOf(a, state, report)
}
