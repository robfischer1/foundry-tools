package releasekit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
)

// OutEnv names the directory a release atom writes its artefacts into. Unset,
// it is release/ under the atom's own work directory (ATOM_WORK, which the pod
// gives each atom). The executor's release capability sets it to the directory
// it assembles the image from, or reads ATOM_WORK/release back.
//
// What lands there is what the Dockerfile COPYs from release/: a Go or Rust
// star's binaries at the top, a bun star's release/ as its steps left it, and a
// python star's venv at app/.venv.
const OutEnv = "ATOM_RELEASE_OUT"

// OutDir is where this atom run writes its artefacts.
func OutDir() string {
	if d := os.Getenv(OutEnv); d != "" {
		return d
	}
	return filepath.Join(atoms.WorkDir(), buildlane.ReleaseDir)
}

// StepVerdict reads one base-built release step's exit (the bun and python
// releases): a verdict, and whether the step passed. The tree's failure is a
// finding and the substrate's and the run's a could-not-run
// (checks.ReleaseStepState); the step is named in the reason, so a failure says
// which of the record's steps it was. A step that never started is the atom's
// could-not-run.
func StepVerdict(a checks.AtomDef, argv []string, code int, out string) (checks.Verdict, bool) {
	if code < 0 {
		return checks.VerdictOf(a, 2, "the atom never ran: "+out), false
	}
	state := checks.ReleaseStepState(code, out)
	if state == 0 {
		return checks.VerdictOf(a, 0, out), true
	}
	return checks.VerdictOf(a, state, fmt.Sprintf("`%s` exited %d\n%s", strings.Join(argv, " "), code, out)), false
}

// TSMade is the verdict of a bun release whose steps ran clean: what the
// release script left under release/ is what the Dockerfile's COPY gets, so
// nothing there is a finding.
func TSMade(a checks.AtomDef, ref string, made []string, steps checks.Verdict) checks.Verdict {
	if len(made) == 0 {
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - the release steps ran clean and left nothing under "+buildlane.ReleaseDir+"/ — the Dockerfile's COPY would have nothing to copy")
	}
	steps.Reason = fmt.Sprintf("release build: `bun run release` on %s; release/ holds %s\n%s", ref, strings.Join(made, ", "), steps.Reason)
	return steps
}

// PythonMade is the verdict of a python release whose sync ran clean: the venv
// the Dockerfile's COPY carries has to be there.
func PythonMade(a checks.AtomDef, ref string, made []string, steps checks.Verdict) checks.Verdict {
	if len(made) == 0 {
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - uv sync ran clean and left no "+checks.PythonReleaseApp+"/.venv — the Dockerfile's COPY would have no venv to copy")
	}
	steps.Reason = fmt.Sprintf("release build: the venv, with no extras, on %s\n%s", ref, steps.Reason)
	return steps
}

// Scoped puts the line that says what was built and where its names came from
// in front of a compile's verdict (checks.ReleaseScope).
func Scoped(plan checks.ReleasePlan, v checks.Verdict) checks.Verdict {
	v.Reason = checks.ReleaseScope(plan) + "\n" + v.Reason
	return v
}

// Release steps' argvs, in the order they run, so both runners name the same
// commands. The bun release is the frozen install, then the repo's own release
// script; the python release is the sync that builds the venv from the lock.
var (
	// TSInstall is the bun release's first step.
	TSInstall = []string{"bun", "install", "--frozen-lockfile"}
)

// TSSteps is the bun release's steps.
func TSSteps() [][]string { return [][]string{TSInstall, checks.TSReleaseArgs} }

// RunSteps runs a base-built release's steps one after another in dir and
// answers the last step's verdict; the first failure stops the chain, so the
// verdict names the step that failed. The program runs through the input's seam.
func RunSteps(ctx context.Context, in atoms.Input, a checks.AtomDef, dir string, env []string, steps [][]string) checks.Verdict {
	v := checks.VerdictOf(a, 0, "")
	for _, argv := range steps {
		out, code := atoms.RunCmd(ctx, in, atoms.Cmd{Dir: dir, Name: argv[0], Args: argv[1:], Env: env})
		var ok bool
		if v, ok = StepVerdict(a, argv, code, out); !ok {
			return v
		}
	}
	return v
}

// AppEnv names the directory a python release builds its venv in. A venv is not
// relocatable: its console scripts name <app>/.venv/bin/python in their shebang,
// so it is built where the image will carry it (checks.PythonReleaseApp).
const AppEnv = "ATOM_RELEASE_APP"

// AppDir is where this run builds the venv.
func AppDir() string {
	if d := os.Getenv(AppEnv); d != "" {
		return d
	}
	return checks.PythonReleaseApp
}

// BuildDir is where a bun release builds: a copy of the tree in the atom's work
// directory, so the steps write into it and the checkout is left as it was.
func BuildDir() string { return filepath.Join(atoms.WorkDir(), "release-src") }

// SrcEnv names the directory a bun build runs in when the executor has given the
// container a tree of its own to build in (DEF-B6-013). A bun bundle embeds the
// absolute directory it was built in (node_modules paths), and the engine's was
// /src, so for byte-equal bundles the executor's must be too. Unset, a bun
// release builds in BuildDir, a copy, and leaves the checkout as it found it:
// the Dagger path and the gate lane's release atoms are as they were.
const SrcEnv = "ATOM_RELEASE_SRC"

// BuildRoot is where a bun release builds, and whether that is the caller's own
// tree (built in place, nothing copied) or the copy in the atom's work directory.
func BuildRoot() (dir string, inPlace bool) {
	if d := os.Getenv(SrcEnv); d != "" {
		return d, true
	}
	return BuildDir(), false
}

// SrcRoot is the tree an atom that builds in the checkout reads and builds in:
// SrcEnv's when the executor names one, else root, the tree the atom was handed.
func SrcRoot(root string) string {
	if d := os.Getenv(SrcEnv); d != "" {
		return d
	}
	return root
}
