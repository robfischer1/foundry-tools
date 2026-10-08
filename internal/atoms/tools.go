package atoms

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"

	"dagger/foundry-tools/internal/checks"
)

// THE ATOMS THAT EXEC A THIRD-PARTY BINARY. The chains provisioned each tool in
// a container of their own, per atom, per gate; the binary runs in ONE
// container whose PATH carries them all (atoms_tools.go in the module builds
// it), so an atom here only execs by name, through Input.Exec.
//
// THE ORDER OF AN ATOM'S WORK IS THE POINT OF THIS FILE'S RULES. A tree the
// atom has no question about is ABSENT before any tool is touched: on infra
// ops:chezmoi and ops:flux paid 11.2s and 7.1s to provision tools and then
// settled absent (F0 trace). Here the surface and the population are read
// first, the tool's version probe comes next, and the tool's real work last,
// so an absent atom runs no program at all.

// Programs is every program name the atoms exec (Cmd.Name), so the container
// that runs the binary can be held to carrying each one (the module's
// TestToolsContainerCarriesEveryProgram). git and tar come from the base
// system; the rest are pinned layers. uv is not here: dies:refusal-codes is
// the one atom that execs it, and it keeps its Tool marker until uv is
// provisioned (the next feature's).
var Programs = []string{
	"git", "tar", "opa", "orbitparse",
	"opengrep", "hadolint", "shellcheck", "chezmoi", "kubectl", "kubeconform",
	"docker-compose", "wasm-tools", "just",
}

// tempSeq numbers the scratch paths of one process. The atoms run at once, so
// a name made from the pid alone is shared between two of them.
var tempSeq atomic.Int64

// tempPath is a scratch path no other atom of this process uses, nothing made
// yet: no error to branch on, the first write says if the directory is gone.
func tempPath(kind, ext string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("atoms-%s-%d-%d%s", kind, os.Getpid(), tempSeq.Add(1), ext))
}

// neverRan is a program that would not start, or an engine-side failure the
// chains filed as "the atom never ran": a 2, with the reason.
func neverRan(a checks.AtomDef, why string) checks.Verdict {
	return checks.VerdictOf(a, int(checks.StateCannotRun), "the atom never ran: "+why)
}

// unprovisioned is the verdict for a tool that did not pass its version probe,
// in the wording the ops atoms use for it.
func unprovisioned(a checks.AtomDef, why string) checks.Verdict {
	return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the phase's tool could not be provisioned: %s", a.ID, why))
}

// opsSettled is a phase's state and report from its tools' exit and output.
func opsSettled(phase string, rc int, out string) (int, string) {
	state, line := checks.OpsSettle(phase, rc, out)
	if line != "" {
		out += "\n" + line
	}
	return state, out
}
