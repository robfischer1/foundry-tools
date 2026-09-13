package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE OPS LANE: the gate for the trees the cluster and the hosts converge TO,
// ported off Tekton's ci-ops-pipeline (retired 2026-09-09) as atoms.
//
// infra (flux, ansible), foundry-dies, renovate-config and the host stacks are
// not stars: nothing builds an image from them, and until 2026-09-06 nothing
// gated them either. foundry-stocks ci/lib/ops/ops.sh became that gate — one
// script, one PHASE per facet, each facet ABSENT or PRESENT by what the tree
// carries — and Tekton ran it one step per phase at the pinned stocks-ref.
// Tekton left the cluster with the pipeline, and from 2026-09-09 to today an
// infra pull was gated by the fleet atoms alone: check-yaml, detect-secrets,
// hadolint, SAST, the witness. Not one playbook was syntax-checked, not one
// Kustomization built, not one script shellchecked. MEASURED 2026-09-13: the
// ops body still sat in foundry-stocks with one caller, its own test.
//
// EACH PHASE IS ONE ATOM, and the body is STILL ops.sh, run from /stocks at the
// pin the door declared. The atom's own work is three things the script cannot
// do from inside a container: decide the surface from the tracked tree so a
// repo with no ops shape runs no container at all; put the tool the phase
// needs on PATH, pinned and probed, the way the compose and dies atoms do;
// and turn the phase's three-state file into the lane's verdict. Every rule
// in the body — what is a shell script, which profile gates, what "absent"
// means — stays in the one place it was written, which is what keeps the
// pre-push hook and the gate grading with the same eyes (a second copy of
// any of it here would be free to drift).
//
// THE SURFACE IS THE OPS SHAPE, NOT THE FACET. ops.sh gated the repos that are
// not stars, and it gated their shell scripts and YAML *because* they were
// those repos. Running ops:shell over every star's scripts would be a new
// gate the fleet never had, turned on at once across 80 repos at profile
// "error" — so the atoms first ask whether the tree is an ops tree at all
// (flux/, ansible/, a chezmoi source, a compose spec, a rego policy, or one
// of infra's own tools) and stand down ABSENT on a star. Inside an ops tree
// the phase's own detect decides the facet, as before.
//
// tofu IS NOT HERE. The retired pipeline carried a tofu phase; Rob, 2026-09-13:
// "you can skip tofu, as we no longer use it". compose, policy and the two
// kube linters are not here either — compose:*, dies:* and sweep:kubeconform /
// sweep:kube-linter already are.

func init() {
	register("ops:shell", opsShell)
	register("ops:chezmoi", opsChezmoi)
	register("ops:yaml", opsYAML)
	register("ops:dup", opsDup)
	register("ops:declaration", opsDeclaration)
	register("ops:specs", opsSpecs)
	register("ops:ansible", opsAnsible)
	register("ops:flux", opsFlux)
}

// opsSurface answers the tracked files and whether the tree is an ops tree —
// the shape ops.sh was written for. A scan failure is a stop verdict rather
// than an empty surface, for the reason composeSurface gives: an ABSENT read
// off a broken scan is an absence the repository never declared.
func opsSurface(ctx context.Context, r *run, a checks.AtomDef) (files []string, ops bool, stop *checks.Verdict) {
	files, err := r.population(ctx)
	if err != nil {
		v := cannotEnumerate(a, err)
		return nil, false, &v
	}
	return files, checks.IsOpsTree(files), nil
}

// opsAbsent is the stand-down every ops atom shares on a tree that is not an
// ops tree: state 0, no container, and the reason says which shape was looked
// for so a reader does not take the green for coverage.
func opsAbsent(a checks.AtomDef) checks.Verdict {
	return checks.VerdictOf(a, 0, a.ID+": ABSENT - this repository has no ops shape (no flux/, ansible/, chezmoi source, compose spec, rego policy or infra tool), so the ops lane does not gate it. A star is gated by its language lane.")
}

// opsDir is where the body keeps its state — one file per phase: <phase>.rc
// (0/1/2), <phase>.absent (the facet is not in this tree, with why) and, on a
// stand-down before any phase, reason. The atom reads those files rather than
// an exit code, because the body's phases return the status of their last
// command and the rc file is the answer they were written to give.
const opsDir = "/tmp/ops"

// opsContainer is the phase's container: the fleet lane image with the
// TRACKED tree and the stocks mounted, a fresh index over it for the body's
// `git ls-files`, and the script's own environment. OPS_BASE is deliberately
// NOT set (rule 8): the base only changes detect's printed file list, and
// keying the atom on the pull would key its cache on the pull.
//
// THE TREE IS THE GITIGNORE-FILTERED ONE, not the directory as mounted.
// MEASURED 2026-09-13 on the first live run over infra from a developer's
// checkout: ops:dup reported every fleet fact "duplicated in code, 13
// occurrences" — thirteen being the number of linked worktrees under
// .claude/worktrees/, each carrying its own modules/fleet, all inside the
// mounted directory and none of them tracked (dup-check on a clean export
// of the same main: 0 findings). The gate's Job clones clean and would never
// see them; the pre-push hook on a session's machine sees exactly this. The
// body walks the filesystem it is given (dup-check, declaration-integrity,
// ansible-lint), so the filter has to be on the mount.
//
// AND THE INDEX IS BUILT HERE, NOT BY gitReady. gitReady re-mounts /src from
// the unfiltered source whenever .git is a file (a linked worktree — the
// developer case again), which would undo the filter; and on a real clone it
// leaves the checkout's own .git in place, which the filter has excluded.
// So the ops tree is always the same shape: the tracked files, no .git, and
// a fresh `git init` + `git add -A` over them, which is what gitReady does
// for a worktree and what makes `git ls-files` answer the tracked set on
// either kind of checkout.
func (r *run) opsContainer(image string) *dagger.Container {
	tracked := r.src.Filter(dagger.DirectoryFilterOpts{Gitignore: true, Exclude: []string{".git"}})
	return r.withStocks(r.lane(image)).
		WithMountedDirectory("/src", tracked).
		WithExec([]string{"git", "config", "--global", "--add", "safe.directory", "*"}).
		WithExec([]string{"git", "init", "-q", "."}).
		WithExec([]string{"git", "config", "--local", "ca.snapshot", "ops-tracked"}).
		WithExec([]string{"git", "add", "-A"}).
		WithEnvVariable("OPS_DIR", opsDir).
		WithEnvVariable("OPS_LIB", "/stocks/ci/lib/ops").
		WithEnvVariable("OPS_HEAD", "HEAD").
		WithEnvVariable("OPS_UV_INDEX", checks.OpsUVIndex)
}

// opsRun runs detect and then one phase as two plain execs (rule 7: no shell
// between the engine and the body), and reads the phase's answer from the
// files the body wrote. prep puts the phase's tool on PATH; a prep that fails
// is a provisioning failure (2), never a finding.
func opsRun(ctx context.Context, r *run, a checks.AtomDef, phase string,
	prep func(*dagger.Container) (*dagger.Container, error)) checks.Verdict {
	ctr := r.opsContainer(a.Image)
	if prep != nil {
		var err error
		if ctr, err = prep(ctr); err != nil {
			return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the phase's tool could not be provisioned: %v", a.ID, err))
		}
	}
	ran := ctr.
		WithExec([]string{"bash", "/stocks/ci/lib/ops/ops.sh", "detect"}, anyExit).
		WithExec([]string{"bash", "/stocks/ci/lib/ops/ops.sh", phase}, anyExit)
	out, code, err := output(ctx, ran)
	if err != nil {
		return neverRan(a, err)
	}
	if why, err := ran.File(opsDir + "/" + phase + ".absent").Contents(ctx); err == nil {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - "+strings.TrimSpace(why))
	}
	rc, err := ran.File(opsDir + "/" + phase + ".rc").Contents(ctx)
	if err != nil {
		// No verdict file: the body stood down before the phase (detect found
		// no facet at all, or the merge-tree stand-down) and left its reason.
		if reason, rerr := ran.File(opsDir + "/reason").Contents(ctx); rerr == nil && strings.TrimSpace(reason) != "" {
			return checks.VerdictOf(a, 0, a.ID+": ABSENT - "+strings.TrimSpace(reason))
		}
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the %s phase wrote no verdict (exit %d): a kill, a missing interpreter, or a body that is not the one this pin names.\n%s", a.ID, phase, code, out))
	}
	switch strings.TrimSpace(rc) {
	case "0":
		return checks.VerdictOf(a, 0, out)
	case "1":
		return checks.VerdictOf(a, 1, out)
	case "2":
		return checks.VerdictOf(a, 2, out)
	}
	return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the %s phase wrote %q where 0, 1 or 2 was expected.\n%s", a.ID, phase, strings.TrimSpace(rc), out))
}

// opsAtom is the shape all eight share: surface, stand-down, run.
func opsAtom(ctx context.Context, r *run, id, phase string, prep func(*dagger.Container) (*dagger.Container, error)) checks.Verdict {
	a := checks.AtomByID(id)
	_, ops, stop := opsSurface(ctx, r, a)
	if stop != nil {
		return *stop
	}
	if !ops {
		return opsAbsent(a)
	}
	return opsRun(ctx, r, a, phase, prep)
}

// ops:shell — shellcheck over every tracked script, at the body's profiles.
// shellcheck reaches PATH as a wrapper over the shellcheck-py wheel, the one
// distribution of the binary the fleet's index carries; the --version probe
// under the default Expect is the provisioning step, so a wheel that did not
// resolve is 2 before a script is judged.
func opsShell(ctx context.Context, r *run) checks.Verdict {
	return opsAtom(ctx, r, "ops:shell", "shell", func(ctr *dagger.Container) (*dagger.Container, error) {
		return ctr.
			WithNewFile("/usr/local/bin/shellcheck", "#!/bin/sh\nexec uvx --from shellcheck-py shellcheck \"$@\"\n",
				dagger.ContainerWithNewFileOpts{Permissions: 0o755}).
			WithExec([]string{"shellcheck", "--version"}), nil
	})
}

// ops:chezmoi — every tracked *.tmpl renders. The client is fetched pinned,
// mirror first, the same way the compose client and opa are.
func opsChezmoi(ctx context.Context, r *run) checks.Verdict {
	return opsAtom(ctx, r, "ops:chezmoi", "chezmoi", func(ctr *dagger.Container) (*dagger.Container, error) {
		f, err := fetchTool(ctx, checks.ChezmoiMirror, checks.ChezmoiURL)
		if err != nil {
			return nil, err
		}
		return ctr.
			WithFile("/usr/local/bin/chezmoi", f, dagger.ContainerWithFileOpts{Permissions: 0o755}).
			WithExec([]string{"chezmoi", "--version"}), nil
	})
}

// ops:yaml — duplicate keys under flux/, ansible/ and compose/, the defect a
// loader resolves last-wins and never reports. python3 and uv are the lane
// image's own.
func opsYAML(ctx context.Context, r *run) checks.Verdict {
	return opsAtom(ctx, r, "ops:yaml", "yaml", nil)
}

// ops:dup, ops:declaration, ops:specs — infra's own tools, run by the body
// when the tree carries them and ABSENT otherwise.
func opsDup(ctx context.Context, r *run) checks.Verdict {
	return opsAtom(ctx, r, "ops:dup", "dup", nil)
}

func opsDeclaration(ctx context.Context, r *run) checks.Verdict {
	return opsAtom(ctx, r, "ops:declaration", "declaration", nil)
}

func opsSpecs(ctx context.Context, r *run) checks.Verdict {
	return opsAtom(ctx, r, "ops:specs", "specs", nil)
}

// ops:ansible — every playbook syntax-checked, then ansible-lint at the gating
// profile and a count at the report profile. The body brings ansible-core and
// ansible-lint through `uv run --with`, so the only provisioning is uv, which
// the lane image already carries.
func opsAnsible(ctx context.Context, r *run) checks.Verdict {
	return opsAtom(ctx, r, "ops:ansible", "ansible", nil)
}

// ops:flux — every Flux Kustomization under flux/ built with kubectl
// kustomize. kubectl is fetched pinned from the release host; there is no
// Nexus mirror of dl.k8s.io today, so the mirror and the upstream are the
// same URL and fetchTool's second try is the retry.
func opsFlux(ctx context.Context, r *run) checks.Verdict {
	return opsAtom(ctx, r, "ops:flux", "flux", func(ctr *dagger.Container) (*dagger.Container, error) {
		f, err := fetchTool(ctx, checks.KubectlMirror, checks.KubectlURL)
		if err != nil {
			return nil, err
		}
		return ctr.
			WithFile("/usr/local/bin/kubectl", f, dagger.ContainerWithFileOpts{Permissions: 0o755}).
			WithExec([]string{"kubectl", "version", "--client=true"}), nil
	})
}
