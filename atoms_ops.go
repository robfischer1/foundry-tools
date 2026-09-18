package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
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
// EACH PHASE IS ONE ATOM, AND NOW NO SCRIPT. ops.sh ran here from /stocks, one
// bash phase per atom writing a three-state file. Each phase is Go now: its
// tools (shellcheck, chezmoi, kubectl, ansible through uv, infra's own tools)
// run as plain execs, and every rule the body carried — what is a shell
// script, which severity gates, what "absent" means, what a fault of the
// substrate looks like — is in internal/checks/opsphases.go, the one place,
// which keeps the pre-push hook and the gate grading with the same eyes.
//
// THE SURFACE IS THE OPS SHAPE, NOT THE FACET. ops.sh gated the repos that are
// not stars, and it gated their shell scripts and YAML *because* they were
// those repos. Running ops:shell over every star's scripts would be a new
// gate the fleet never had, turned on at once across 80 repos at profile
// "error" — so the atoms first ask whether the tree is an ops tree at all
// (flux/, ansible/, a chezmoi source, a compose spec, a rego policy, or one
// of infra's own tools) and stand down ABSENT on a star. Inside an ops tree
// each phase decides its facet from the tracked files, as detect did.
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

// opsContainer is the phase's container: the fleet lane image with the TRACKED
// tree mounted and a fresh index over it, so `git ls-files` answers the
// tracked set on a clone and a linked worktree alike.
//
// THE TREE IS THE GITIGNORE-FILTERED ONE, not the directory as mounted.
// MEASURED 2026-09-13 on the first live run over infra from a developer's
// checkout: ops:dup reported every fleet fact "duplicated in code, 13
// occurrences" — thirteen being the number of linked worktrees under
// .claude/worktrees/, each carrying its own modules/fleet, all inside the
// mounted directory and none of them tracked. The repo's own tools walk the
// filesystem they are given, so the filter has to be on the mount; and the
// index is built here rather than by gitReady, which re-mounts /src from the
// unfiltered source for a linked worktree.
func (r *run) opsContainer(image string) *dagger.Container {
	tracked := r.src.Filter(dagger.DirectoryFilterOpts{Gitignore: true, Exclude: []string{".git"}})
	return r.lane(image).
		WithMountedDirectory("/src", tracked).
		WithExec([]string{"git", "config", "--global", "--add", "safe.directory", "*"}).
		WithExec([]string{"git", "init", "-q", "."}).
		WithExec([]string{"git", "config", "--local", "ca.snapshot", "ops-tracked"}).
		WithExec([]string{"git", "add", "-A"}).
		// The index uv resolves ansible-core, ansible-lint and pyyaml from.
		WithEnvVariable("UV_INDEX_URL", checks.OpsUVIndex)
}

// opsResult is what one phase found: its state and its report, or the facet
// it found absent.
type opsResult struct {
	state  int
	out    string
	absent string
}

// opsPhase runs one phase over the tracked files in its container.
type opsPhase func(ctx context.Context, ctr *dagger.Container, files []checks.OpsFile) (opsResult, error)

// opsAtom is the shape all eight share: the surface, the stand-down, the tool
// on PATH, the tracked files, the phase.
func opsAtom(ctx context.Context, r *run, id string, phase opsPhase, prep func(*dagger.Container) (*dagger.Container, error)) checks.Verdict {
	a := checks.AtomByID(id)
	_, ops, stop := opsSurface(ctx, r, a)
	if stop != nil {
		return *stop
	}
	if !ops {
		return opsAbsent(a)
	}
	ctr := r.opsContainer(a.Image)
	if prep != nil {
		var err error
		if ctr, err = prep(ctr); err != nil {
			return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the phase's tool could not be provisioned: %v", a.ID, err))
		}
	}
	ls, code, err := output(ctx, ctr.WithExec([]string{"git", "ls-files", "-s", "-z"}, anyExit))
	if err != nil {
		return neverRan(a, err)
	}
	if code != 0 {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the tracked tree could not be listed: "+ls)
	}
	res, err := phase(ctx, ctr, checks.OpsTracked(ls))
	if err != nil {
		return neverRan(a, err)
	}
	if res.absent != "" {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - "+res.absent)
	}
	return checks.VerdictOf(a, res.state, res.out)
}

// opsRun runs one exec in ctr and answers its combined output and exit, and
// the container it left.
func opsRun(ctx context.Context, ctr *dagger.Container, args []string, opts ...dagger.ContainerWithExecOpts) (*dagger.Container, string, int, error) {
	o := dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny}
	if len(opts) > 0 {
		o = opts[0]
		o.Expect = dagger.ReturnTypeAny
	}
	next := ctr.WithExec(args, o)
	out, code, err := outputBoth(ctx, next)
	return next, out, code, err
}

// opsSettled is a phase's result from its tools' exit and report.
func opsSettled(phase string, rc int, out string) opsResult {
	state, line := checks.OpsSettle(phase, rc, out)
	if line != "" {
		out += "\n" + line
	}
	return opsResult{state: state, out: out}
}

// uvPython runs one of the repo's own python tools the way the body did:
// isolated, no project, pyyaml alongside.
func uvPython(tool string, args ...string) []string {
	return append([]string{"uv", "run", "--isolated", "--no-project", "--with", "pyyaml", "python", "tools/" + tool}, args...)
}

// ops:shell — shellcheck over every tracked script, at the gating severity,
// with the report severity counted and never gating.
//
// THE GATE IS `error`, AND THAT IS A DELIBERATE FLOOR. Measured 2026-09-07
// against personal/dotfiles' 20 shell files: `error` is 0 and `warning` is 10.
// What `error` still catches was checked rather than assumed — SC1046 an
// unclosed `if`, an unbraced array expansion, `local` outside a function —
// scripts that are BROKEN. The report severity counts the rest.
//
// shellcheck comes from the shellcheck-py wheel through uvx, the one
// distribution of the binary the fleet's index carries; the --version probe
// under the default Expect is the provisioning step.
func opsShell(ctx context.Context, r *run) checks.Verdict {
	return opsAtom(ctx, r, "ops:shell", opsShellPhase, func(ctr *dagger.Container) (*dagger.Container, error) {
		return ctr.WithExec(append(shellcheck, "--version")), nil
	})
}

var shellcheck = []string{"uvx", "--from", "shellcheck-py", "shellcheck"}

func opsShellPhase(ctx context.Context, ctr *dagger.Container, files []checks.OpsFile) (opsResult, error) {
	_, grep, code, err := opsRun(ctx, ctr, []string{"git", "grep", "-I", "-n", "-z", "-E", "^#!"})
	if err != nil {
		return opsResult{}, err
	}
	// git grep answers 1 when nothing matched; anything above is a grep that
	// did not read the tree.
	if code > 1 {
		return opsResult{state: 2, out: "shell: could not read the scripts' first lines: " + grep}, nil
	}
	shebang, fragments := checks.OpsShellFiles(files, checks.OpsFirstLines(grep))
	if len(shebang) == 0 && len(fragments) == 0 {
		return opsResult{absent: "no shell script in this tree"}, nil
	}
	// A shebang tells shellcheck its dialect; a SOURCED FRAGMENT does not, so
	// it is told bash — 3 false SC2148s became 0 on dotfiles, measured.
	check := func(severity string) (string, int, error) {
		var out strings.Builder
		rc := 0
		for _, group := range []struct {
			files []string
			flags []string
		}{{shebang, nil}, {fragments, []string{"-s", "bash"}}} {
			for _, batch := range checks.OpsBatches(group.files) {
				args := append(append(append(append([]string{}, shellcheck...), "-S", severity), group.flags...), "-f", "gcc")
				_, o, c, err := opsRun(ctx, ctr, append(args, batch...))
				if err != nil {
					return "", 0, err
				}
				out.WriteString(o)
				rc = max(rc, c)
			}
		}
		return out.String(), rc, nil
	}
	gate, rc, err := check("error")
	if err != nil {
		return opsResult{}, err
	}
	report, _, err := check("warning")
	if err != nil {
		return opsResult{}, err
	}
	res := opsSettled("shell", rc, fmt.Sprintf("shell: %d script(s), gating at severity error\n%s", len(shebang)+len(fragments), gate))
	res.out += fmt.Sprintf("\nshellcheck -S warning: %d finding(s) — reported, not gating", checks.OpsDebt(report))
	return res, nil
}

// ops:chezmoi — every tracked *.tmpl of a chezmoi source tree renders, the
// check a dotfiles tree has no other way to make: a template that will not
// execute breaks `chezmoi apply` on every host at once.
//
// `--source .` IS LOAD-BEARING. chezmoi resolves `include` against its SOURCE
// directory, which defaults to ~/.local/share/chezmoi; without the flag four
// of dotfiles' six templates failed on a tree where every one was fine
// (ops--personal.dotfiles-mft58, 2026-09-08). Rendering has no host config: a
// template that reads custom data from a .chezmoi.toml.tmpl will fail here
// while working on the host that has it. None does today.
func opsChezmoi(ctx context.Context, r *run) checks.Verdict {
	return opsAtom(ctx, r, "ops:chezmoi", opsChezmoiPhase, func(ctr *dagger.Container) (*dagger.Container, error) {
		f, err := fetchTool(ctx, checks.ChezmoiMirror, checks.ChezmoiURL)
		if err != nil {
			return nil, err
		}
		return ctr.
			WithFile("/usr/local/bin/chezmoi", f, dagger.ContainerWithFileOpts{Permissions: 0o755}).
			WithExec([]string{"chezmoi", "--version"}), nil
	})
}

func opsChezmoiPhase(ctx context.Context, ctr *dagger.Container, files []checks.OpsFile) (opsResult, error) {
	templates := checks.OpsChezmoiTemplates(files)
	if len(templates) == 0 {
		return opsResult{absent: "no chezmoi template in this tree"}, nil
	}
	var out strings.Builder
	rc := 0
	for _, t := range templates {
		src, err := ctr.File("/src/" + t).Contents(ctx)
		if err != nil {
			return opsResult{}, err
		}
		_, o, c, err := opsRun(ctx, ctr, []string{"chezmoi", "--source", ".", "execute-template"}, dagger.ContainerWithExecOpts{Stdin: src})
		if err != nil {
			return opsResult{}, err
		}
		if c != 0 {
			rc = 1
			fmt.Fprintf(&out, "FAIL %s\n    %s\n", t, strings.ReplaceAll(strings.TrimRight(o, "\n"), "\n", "\n    "))
		}
	}
	fmt.Fprintf(&out, "chezmoi: %d template(s) checked", len(templates))
	return opsSettled("chezmoi", rc, out.String()), nil
}

// ops:yaml — no tracked YAML carries a duplicate key, the defect every loader
// the fleet runs resolves last-wins and never reports. A repo that carries its
// own tools/yaml-strict (infra) knows which trees matter, and is run instead.
func opsYAML(ctx context.Context, r *run) checks.Verdict {
	return opsAtom(ctx, r, "ops:yaml", opsYAMLPhase, nil)
}

func opsYAMLPhase(ctx context.Context, ctr *dagger.Container, files []checks.OpsFile) (opsResult, error) {
	yamls := checks.OpsYAMLFiles(files)
	if len(yamls) == 0 {
		return opsResult{absent: "no yaml in this tree"}, nil
	}
	if checks.OpsHasTool(files, "yaml-strict", false) {
		_, out, rc, err := opsRun(ctx, ctr, uvPython("yaml-strict", "flux"))
		if err != nil {
			return opsResult{}, err
		}
		return opsSettled("yaml", rc, out), nil
	}
	errs := map[string]error{}
	for _, f := range yamls {
		src, err := ctr.File("/src/" + f).Contents(ctx)
		if err != nil {
			return opsResult{}, err
		}
		errs[f] = checks.OpsStrictYAML(src)
	}
	report, bad := checks.OpsYAMLReport(yamls, errs)
	rc := 0
	if bad {
		rc = 1
	}
	return opsSettled("yaml", rc, report), nil
}

// ops:dup, ops:declaration, ops:specs — infra's own checkers, run when the tree
// carries them and ABSENT otherwise.
func opsDup(ctx context.Context, r *run) checks.Verdict {
	return opsAtom(ctx, r, "ops:dup", func(ctx context.Context, ctr *dagger.Container, files []checks.OpsFile) (opsResult, error) {
		if !checks.OpsHasTool(files, "dup-check", true) {
			return opsResult{absent: "no tools/dup-check in this tree"}, nil
		}
		_, out, rc, err := opsRun(ctx, ctr, []string{"python3", "tools/dup-check", "--blocking"})
		return opsSettled("dup", rc, out), err
	}, nil)
}

func opsDeclaration(ctx context.Context, r *run) checks.Verdict {
	return opsAtom(ctx, r, "ops:declaration", func(ctx context.Context, ctr *dagger.Container, files []checks.OpsFile) (opsResult, error) {
		if !checks.OpsHasTool(files, "declaration-integrity", false) {
			return opsResult{absent: "no tools/declaration-integrity in this tree"}, nil
		}
		_, out, rc, err := opsRun(ctx, ctr, uvPython("declaration-integrity"))
		return opsSettled("declaration", rc, out), err
	}, nil)
}

// ops:specs — the console read-model specs follow nas01-stacks' emits. The
// tool grades itself: 2 is "could not read the source", could-not-run
// whatever its words were.
func opsSpecs(ctx context.Context, r *run) checks.Verdict {
	return opsAtom(ctx, r, "ops:specs", func(ctx context.Context, ctr *dagger.Container, files []checks.OpsFile) (opsResult, error) {
		if !checks.OpsHasTool(files, "console-specs", false) {
			return opsResult{absent: "no tools/console-specs in this tree"}, nil
		}
		_, out, rc, err := opsRun(ctx, ctr, uvPython("console-specs", "--check"))
		if rc == 2 {
			return opsResult{state: 2, out: out + "\nspecs: could not read the source — did not look"}, err
		}
		return opsSettled("specs", rc, out), err
	}, nil)
}

// ops:ansible — every playbook syntax-checked, then ansible-lint at the gating
// profile (min) and a count at the report profile (basic).
//
// A TREE THAT DECLARES COLLECTIONS CANNOT BE SYNTAX-CHECKED WITHOUT THEM:
// ansible-core resolves a module against the collections installed, and
// rob/infra went red on main for exactly that the moment its first collection
// landed (2026-09-09). A failed install is could-not-run — not looking is not
// a clean bill, and not the tree's fault. Every step runs from ansible/, so
// the tree's own ansible.cfg decides where the collections land.
func opsAnsible(ctx context.Context, r *run) checks.Verdict {
	return opsAtom(ctx, r, "ops:ansible", func(ctx context.Context, ctr *dagger.Container, files []checks.OpsFile) (opsResult, error) {
		return opsAnsiblePhase(ctx, r, ctr, files)
	}, nil)
}

// ansibleCollectionsPath is where the gate installs a tree's collections, and
// where every ansible step finds them: outside the tree, so the tree's own
// collections_path cannot place them where lint then reads them as source.
const ansibleCollectionsPath = "/opt/ansible-collections"

// ansibleCollections installs the tree's declared collections from a step that
// sees only ansible/requirements.yml and ansible/ansible.cfg (whose [galaxy]
// block names the proxy).
//
// THE INSTALL IS CACHED ON WHAT IT READS, AND A FAILED ONE IS NEVER CACHED.
// MEASURED over every gate receipt 2026-09-09 -> 17: ops:ansible found
// something on 2 trees and could not run on 32 — 31 of them 2026-09-14, the
// nexus galaxy proxy answering 404 on a cold miss (infra f81bcd08), and one
// 2026-09-16 07:10Z when the proxy answered vyos.vyos without `results`.
// Every one was the collections resolving live on a run that changed nothing
// about them. A filtered mount keys the step on the declaration alone, so the
// engine answers it from cache until requirements.yml or ansible.cfg changes.
// The exec expects success rather than any exit: an exec that errors is not
// cached, so a proxy that failed once is asked again next run instead of
// being remembered failing. A failed install is retried once live with
// --clear-response-cache, the flag galaxy's own error names for a bad cached
// answer.
func (r *run) ansibleCollections(ctx context.Context) (*dagger.Directory, string, error) {
	declaration := r.src.Filter(dagger.DirectoryFilterOpts{Include: []string{"ansible/requirements.yml", "ansible/ansible.cfg"}})
	base := r.laneBase(checks.ImageFleet).
		WithMountedDirectory("/src", declaration).
		WithWorkdir("/src/ansible").
		WithEnvVariable("UV_INDEX_URL", checks.OpsUVIndex)
	var log strings.Builder
	for _, flags := range [][]string{nil, {"--clear-response-cache"}} {
		args := append([]string{"uv", "run", "--isolated", "--no-project", "--with", "ansible-core", "ansible-galaxy", "collection", "install"}, flags...)
		args = append(args, "-r", "requirements.yml", "-p", ansibleCollectionsPath)
		installed := base.WithExec(args)
		out, err := installed.Stdout(ctx)
		log.WriteString(strings.Join(args[6:], " ") + "\n" + out)
		if err == nil {
			return installed.Directory(ansibleCollectionsPath), log.String(), nil
		}
		log.WriteString(err.Error() + "\n")
		if len(flags) > 0 {
			return nil, log.String(), err
		}
	}
	return nil, log.String(), nil
}

func opsAnsiblePhase(ctx context.Context, r *run, ctr *dagger.Container, files []checks.OpsFile) (opsResult, error) {
	playbooks := checks.OpsPlaybooks(files)
	if len(playbooks) == 0 {
		return opsResult{absent: "no ansible/playbooks in this tree"}, nil
	}
	tracked := func(p string) bool {
		for _, f := range files {
			if f.Path == p {
				return true
			}
		}
		return false
	}
	ansible := []string{"uv", "run", "--isolated", "--no-project", "--with", "ansible-core"}
	lint := []string{"uv", "run", "--isolated", "--no-project", "--with", "ansible-core", "--with", "ansible-lint", "ansible-lint", "--offline", "-q", "--profile"}
	ctr = ctr.WithWorkdir("/src/ansible")
	var out strings.Builder
	if tracked("ansible/requirements.yml") {
		collections, o, err := r.ansibleCollections(ctx)
		out.WriteString(o)
		if err != nil {
			return opsResult{state: 2, out: out.String() + "\nansible: declares collections that would not install — did not look"}, nil
		}
		ctr = ctr.
			WithMountedDirectory(ansibleCollectionsPath, collections).
			WithEnvVariable("ANSIBLE_COLLECTIONS_PATH", ansibleCollectionsPath)
	}
	var inventory []string
	if tracked("ansible/inventory/hosts.yml") {
		inventory = []string{"-i", "inventory/hosts.yml"}
	}
	for _, pb := range playbooks {
		_, o, rc, err := opsRun(ctx, ctr, append(append(append(append([]string{}, ansible...), "ansible-playbook", "--syntax-check"), inventory...), pb))
		if err != nil {
			return opsResult{}, err
		}
		out.WriteString("syntax-check " + pb + "\n" + o)
		if rc != 0 {
			return opsSettled("ansible", rc, out.String()), nil
		}
	}
	_, o, rc, err := opsRun(ctx, ctr, append(append([]string{}, lint...), "min", "."))
	if err != nil {
		return opsResult{}, err
	}
	out.WriteString("ansible-lint --profile min (gates)\n" + o)
	debt, err := ctr.WithExec(append(append([]string{}, lint...), "basic", "."), dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny}).Stdout(ctx)
	if err != nil {
		return opsResult{}, err
	}
	res := opsSettled("ansible", rc, out.String())
	res.out += fmt.Sprintf("\nansible-lint --profile basic: %d finding(s) — reported, not gating", checks.OpsAnsibleDebt(debt))
	return res, nil
}

// ops:flux — every tree a Flux Kustomization CR under flux/clusters/ applies,
// built with kubectl kustomize the way kustomize-controller will: a duplicate
// resource id, a missing base, a bad patch stop here, before Flux stops
// applying (measured 2026-09-06: a second IngressRoute named git parked the
// infrastructure Kustomization for eight minutes). No CR: every flux/<dir>
// with a kustomization.yaml.
//
// THEN THE BUILT STREAM IS SCHEMA-CHECKED, what kustomize-controller will
// apply rather than the sources. ops.sh ran kubeconform over it only when
// kubeconform was on PATH, and in this lane it never was — infra's runs said
// "kubeconform is not on PATH" every time (measured 2026-09-15, 831 objects
// from 4 trees), so the check was written and never ran. It runs now: the one
// static binary is copied out of the pinned kubeconform image sweep:kubeconform
// already runs, the way uv and node reach their lanes, with ops.sh's flags
// (strict, missing schemas ignored, CRDs skipped, the default location).
//
// kubectl is fetched pinned from the release host; there is no Nexus mirror of
// dl.k8s.io today, so fetchTool's second try is the retry.
func opsFlux(ctx context.Context, r *run) checks.Verdict {
	return opsAtom(ctx, r, "ops:flux", opsFluxPhase, func(ctr *dagger.Container) (*dagger.Container, error) {
		f, err := fetchTool(ctx, checks.KubectlMirror, checks.KubectlURL)
		if err != nil {
			return nil, err
		}
		return ctr.
			WithFile("/usr/local/bin/kubectl", f, dagger.ContainerWithFileOpts{Permissions: 0o755}).
			WithFile("/usr/local/bin/kubeconform", dag.Container().From(checks.ImageKubeconform).File("/kubeconform"), dagger.ContainerWithFileOpts{Permissions: 0o755}).
			WithExec([]string{"kubectl", "version", "--client=true"}).
			WithExec([]string{"kubeconform", "-v"}), nil
	})
}

// opsFluxBuilt is where the built stream is written for kubeconform to read.
const opsFluxBuilt = "/tmp/ops/flux.built.yaml"

func opsFluxPhase(ctx context.Context, ctr *dagger.Container, files []checks.OpsFile) (opsResult, error) {
	if !slices.ContainsFunc(files, func(f checks.OpsFile) bool { return strings.HasPrefix(f.Path, "flux/") }) {
		return opsResult{absent: "no flux/ in this tree"}, nil
	}
	manifests := map[string]string{}
	for _, m := range checks.OpsFluxClusterManifests(files) {
		src, err := ctr.File("/src/" + m).Contents(ctx)
		if err != nil {
			return opsResult{}, err
		}
		manifests[m] = src
	}
	paths, problems := checks.OpsFluxPaths(manifests)
	if len(paths) == 0 {
		paths = checks.OpsFluxFallback(files)
	}
	if len(paths) == 0 {
		return opsResult{absent: "flux/ carries no Kustomization CR and no kustomization.yaml"}, nil
	}
	var out, built strings.Builder
	for _, p := range problems {
		out.WriteString(p + "\n")
	}
	rc := 0
	for i, p := range paths {
		out.WriteString("kustomize build " + p + "\n")
		// THE BUILT STREAM GOES TO A FILE KUBECTL WRITES, NOT TO ANYTHING
		// DAGGER CAN SEE. Dagger echoes an exec's stdout into the lane's
		// progress log line by line, and a kustomize build is the whole
		// rendered tree — measured 2026-09-18 on infra: a gate pod's log was
		// 65,883 lines and 13 MB, 986 of them `apiVersion:`, rotated past
		// what `kubectl logs` shows, shipped to Loki on every run, where the
		// comment lines that mention level=error were counted as errors by
		// Loki's level detection. RedirectStdout was tried first (b1c078fe)
		// and measured useless: dagger tees a redirected stream into the log
		// all the same (65,888 lines on the next run). `kubectl kustomize -o`
		// writes the file itself — nothing on stdout for dagger to tee, no
		// shell (rule 7), stderr and the exit status as before — and the
		// stream is read back from the file.
		rendered := "/tmp/kustomize." + strconv.Itoa(i) + ".yaml"
		next := ctr.WithExec([]string{"kubectl", "kustomize", p, "-o", rendered}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})
		code, err := next.ExitCode(ctx)
		if err != nil {
			return opsResult{}, err
		}
		if code != 0 {
			stderr, _ := next.Stderr(ctx)
			out.WriteString(stderr + "  build FAILED: " + p + "\n")
			rc = 1
			continue
		}
		stream, err := next.File(rendered).Contents(ctx)
		if err != nil {
			return opsResult{}, err
		}
		// A separator between trees: kustomize ends its stream without one,
		// and two trees back to back fuse at the boundary (ops-infra-gfrpk,
		// 2026-09-06).
		built.WriteString(stream + "\n---\n")
	}
	if rc != 0 {
		return opsSettled("flux", rc, out.String()), nil
	}
	fmt.Fprintf(&out, "%d object(s) built from %d tree(s)\n", checks.OpsKinds(built.String()), len(paths))
	_, validated, rc, err := opsRun(ctx, ctr.WithNewFile(opsFluxBuilt, built.String()), []string{
		"kubeconform", "-strict", "-summary", "-ignore-missing-schemas", "-skip", "CustomResourceDefinition",
		"-schema-location", "default", opsFluxBuilt,
	})
	if err != nil {
		return opsResult{}, err
	}
	out.WriteString(validated)
	return opsSettled("flux", rc, out.String()), nil
}
