package main

import (
	"context"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE SWEEP, AS TYPED CHAINS. These five describe a REPOSITORY rather than a
// change, so their answer cannot differ between two pulls against the same
// repo — and running them per pull leaves every repository nobody opened a PR
// against unevaluated indefinitely. That is the whole of CA F9: the digest rots
// while the tree sits still, so the probe has to be a clock, not a diff. None
// of them may appear in a pull's path (checks.PullPathAtoms is the
// enforcement, TestNoSweepAtomOnThePullPath the assertion); ca-sweep's CronJob
// is the one caller.
//
// A SWEEP ATOM RUNS UNATTENDED, which is where a silent pass does the most
// damage: nobody is watching, so a body that exits 0 because its tool never
// arrived reads as a clean fleet for as long as it takes somebody to look.
// Every refusal below is therefore explicit, and every absence says so.
//
// Read runtime.go's eight rules and atoms_go.go first; this file follows both.

func init() {
	register("sweep:digest-pins", sweepDigestPins)
	register("sweep:portfolio-sbom", sweepPortfolioSbom)
	register("sweep:template-render-matrix", sweepTemplateRenderMatrix)
	register("sweep:kubeconform", sweepKubeconform)
	register("sweep:kube-linter", sweepKubeLinter)
}

// Every image digest this repo's workflows pin still resolves in the registry.
//
// THE CANONICAL SCRIPT, READ AT ITS ONE HOME. It already carries the three
// states this module requires — 0 every pin resolves, 1 a pin is BROKEN, 2 no
// pins found at all ("the scan is broken, not the tree clean") — which is why
// this atom runs it instead of reimplementing it. Twice in five days a
// collected digest took out the same five stars, and both times a human found
// it by noticing a red landing.
//
// THE SURFACE PROBE IS THE ATOM'S OWN, and it is what the script cannot do for
// itself. The script was written for foundry-stocks, where cast.yml carries
// several pins, so calling zero pins a broken scan is right THERE. Dispatched
// over all 86 repos in custody it is wrong on most of them: a star calls the
// reusable workflow and the pin lives in the callee's tree. Measured on
// ca-sweep-manual-1788973171 (2026-09-09), that turned 57 of 86 repos into
// cannot-run and buried the run's one real finding. So the atom asks first
// whether there is a pin surface at all (checks.HasPinSurface over
// checks.PinSurfacePattern): no surface is ABSENT, and a surface the extractor
// could not read stays a CANNOT RUN that now says which of the two it is,
// because a check that could not run must also say why.
//
// THE PROBE STILL RUNS BEFORE THE FETCH. In the shell it was an ordering the
// tests policed; here it is Go before any container exists, so the 57 repos
// with nothing to check pay no network round trip to learn it.
func sweepDigestPins(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("sweep:digest-pins")

	bodies, present, err := r.forgejoWorkflows(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the workflow tree could not be read, so its pin population is unknown rather than empty: "+err.Error())
	}
	if !present {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - no .forgejo/workflows in this tree, so nothing here pins a digest.")
	}
	if !checks.HasPinSurface(bodies) {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - .forgejo/workflows carries no digest reference at all, so this repo's pin population is EMPTY rather than unscanned. It calls the reusable workflows and the image pin lives in the callee's tree.")
	}
	if _, err := r.stocks.File("ci/lib/digest-pins.sh").Sync(ctx); err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the canonical script is not reachable through the door.")
	}

	// oras is not in ANY of the four CI images (images.go lists what each
	// carries), so this is a provision rather than a fallback — and both
	// sources failing is a 2, never a fallthrough. Resolving zero pins and
	// calling them all healthy is the outage this check exists to catch,
	// running backwards.
	oras, err := fetchTool(ctx, checks.OrasMirror, checks.OrasURL)
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - could not fetch oras from the mirror or from upstream. Resolving zero pins and calling them all healthy is the outage this check exists to catch, running backwards.\n"+err.Error())
	}

	ctr := r.withStocks(r.lane(checks.ImageFleet)).
		WithFile("/tmp/oras.tgz", oras).
		// Provisioning, under the DEFAULT Expect: an archive that will not
		// unpack and a client that will not run are both state 2 with the
		// engine's own error text, not a guard and an `exit 2`.
		WithExec([]string{"tar", "-xzf", "/tmp/oras.tgz", "-C", "/usr/local/bin", "oras"}).
		WithExec([]string{"oras", "version"}).
		WithEnvVariable("PINS_DIR", ".forgejo/workflows").
		WithExec([]string{"bash", "/stocks/ci/lib/digest-pins.sh"}, anyExit)

	out, code, err := output(ctx, ctr)
	if err != nil {
		return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
	}
	if code == 2 {
		// The script's own 2 means it extracted no pin. The surface probe
		// above already proved there is one, so this is the broken scan the
		// script's message names — and naming WHICH is the point of asking
		// the two questions separately.
		return checks.VerdictOf(a, 2, out+"\n"+a.ID+": CANNOT RUN - this tree DOES carry a digest reference (it matches "+checks.PinSurfacePattern+") and the canonical extractor still returned none, so the SCAN is broken rather than the tree unpinned. Compare digest_pins() in foundry-stocks/ci/lib/digest-pins.sh against the pin forms under .forgejo/workflows.")
	}
	return checks.VerdictOf(a, code, out)
}

// A repository that builds an image builds it through the workflow that
// attests its SBOM.
//
// NO CONTAINER RUNS. Every term of this question is a fact about the tree — is
// there a Dockerfile, is there a workflow tree, does any workflow call the
// attesting build — so the whole atom is Go over the mounted Directory and the
// engine is never asked to start anything.
//
// THIS DOES NOT RE-RUN THE PORTFOLIO SCAN, deliberately. The scan is
// fleet-wide, already scheduled, and stays where it is: CronJob
// portfolio-weekly (infra, ci-foundry, Mondays 07:00 UTC) drives
// ci-portfolio-pipeline, which re-scores the SBOM attestations the registry
// already holds. A per-repo copy would be a second surface free to disagree
// with the first — the drift this module exists to delete.
//
// What it closes is the hole that scan structurally cannot see. The re-score
// reads ATTESTATIONS; a repo whose image is never attested contributes nothing
// to read, so it scores clean by being invisible, forever, and no pull will
// ever say so. That is CA F9's own value statement — "repos nobody has opened
// a PR against stop being invisible" — asked at the one place where the answer
// is a fact about the tree rather than a fact about the database.
func sweepPortfolioSbom(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("sweep:portfolio-sbom")

	entries, err := r.src.Entries(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the repository root could not be read: "+err.Error())
	}
	if !checks.HasEntry(entries, "Dockerfile") {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - no Dockerfile at the repository root. The portfolio re-scores image SBOMs, and this repo builds no image.")
	}

	bodies, present, err := r.forgejoWorkflows(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the workflow tree could not be read, so whether the image is attested is unknown rather than answered: "+err.Error())
	}
	if !present {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - a Dockerfile and no workflow tree. Nothing here says whether the image is ever built, so nothing here can say whether it is attested.")
	}
	if checks.BuiltThroughAttestingWorkflow(bodies) {
		return checks.VerdictOf(a, 0, a.ID+": the image is built through the attesting workflow, so the weekly re-score can see this repo.")
	}
	return checks.VerdictOf(a, 1, a.ID+": this repo has a Dockerfile but no workflow calling foundry-stocks build.yml (or frontend-build.yml / bake-blade.yml).\n"+
		"The weekly portfolio re-score reads cosign SBOM attestations out of the registry. An image nobody attests contributes no SBOM, so it is not scored badly - it is not scored at all, and the digest reports clean because it never looked.")
}

// Every case in this template's ci-matrix.toml still renders.
//
// A template bug does not break the template. It propagates into every repo
// stamped afterward and surfaces later, in someone else's repo, where the
// cause is expensive to trace — which is exactly why this is a cadence check
// and not a pull check: the stamped population keeps growing while the
// template tree sits still.
//
// THE TREE MUST BE A REAL REPOSITORY, AND THE CHECK FOR IT IS TWO QUESTIONS.
// The matrix renders the template AT ITS GIT HEAD (--vcs-ref=HEAD is
// load-bearing, foundry#130); without a repository copier resolves some other
// tree, and a green from that would be a green about something else. `test -d
// .git` answered both questions at once in the shell; in Go a directory is an
// entry that is NOT readable as a file, so both are asked — and a linked
// worktree, whose `.git` is a file pointing at a host path that does not exist
// inside the container, still fails this ON PURPOSE. gitReady rebuilds an
// index for the atoms that only enumerate files; it cannot rebuild the history
// copier resolves a ref against.
//
// gitReady is still called, for its other half: git refuses a repository it
// does not own ("dubious ownership", exit 128) and the process here is root
// over a mounted tree.
func sweepTemplateRenderMatrix(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("sweep:template-render-matrix")

	entries, err := r.src.Entries(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the repository root could not be read: "+err.Error())
	}
	if !checks.HasEntry(entries, "ci-matrix.toml") {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - no ci-matrix.toml at the repository root, so this repo declares no render matrix.")
	}
	if _, err := r.stocks.File("ci/lib/template_render_matrix.py").Sync(ctx); err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the canonical gate is not reachable through the door.")
	}

	const noRepository = ": CANNOT RUN - no .git in the tree under check. The matrix renders the template AT ITS GIT HEAD (--vcs-ref=HEAD is load-bearing, foundry#130); without a repository copier resolves some other tree, and a green from that would be a green about something else."
	if !checks.HasEntry(entries, ".git") {
		return checks.VerdictOf(a, 2, a.ID+noRepository)
	}
	if _, err := r.src.File(".git").Contents(ctx); err == nil {
		return checks.VerdictOf(a, 2, a.ID+noRepository+" Here .git is a FILE, not a directory: this tree came from a linked worktree, and the gitdir it names is a host path that does not exist inside the container. That is a refusal on purpose — an index can be rebuilt, a history cannot.")
	}

	ctr := r.gitReady(ctx, r.withStocks(r.lane(checks.ImageFleet))).
		// The provisioning probe: python-ci carries uvx, and the canonical
		// gate shells out to it. A missing one is state 2 with the engine's
		// error, never a green.
		WithExec([]string{"uvx", "--version"}).
		WithExec([]string{"python3", "/stocks/ci/lib/template_render_matrix.py", "--template", "."}, anyExit)
	return verdict(ctx, a, ctr)
}

// Every manifest under flux/ validates against its Kubernetes schema.
//
// THE ZERO-SCAN REFUSAL IN THIS ATOM'S DIALECT. kubeconform reports `skipped`
// both for a CRD genuinely absent from the catalogue and for a catalogue it
// could not reach, and the second of those is a CANNOT RUN — so the catalogue
// is PROBED before the scan, and a scan that validated nothing at all is a 2
// rather than a green.
//
// THE PROBE NEEDS NO CONTAINER. It was `wget` inside the image because the
// image was where the atom lived; the question is only whether one URL
// answers, and dag.HTTP asks it from the engine — which also means the answer
// is cached and the image is never pulled for a catalogue outage.
//
// THE CATALOGUE IS NOT AN ENHANCEMENT HERE. Measured against infra's own flux/
// tree on 2026-09-08: the default store alone validates 517 resources and
// SKIPS 395 — every HelmRelease, Kustomization, GitRepository and
// CiliumNetworkPolicy in the tree, which is most of what that tree IS. With
// the catalogue: 809 validated, 101 skipped.
func sweepKubeconform(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("sweep:kubeconform")

	entries, err := r.src.Entries(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the repository root could not be read: "+err.Error())
	}
	if !checks.HasEntry(entries, "flux") {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - no flux/ tree at the repository root.")
	}
	if _, err := dag.HTTP(checks.CRDSchemaProbe).Sync(ctx); err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the CRD schema catalogue is unreachable. Every custom resource would then report as skipped, which is indistinguishable from a clean validation and is not one.\n"+err.Error())
	}

	ctr := r.lane(checks.ImageKubeconformSweep).WithExec([]string{
		"/kubeconform",
		"-ignore-missing-schemas",
		"-ignore-filename-pattern", `\.json$`,
		"-schema-location", "default",
		"-schema-location", checks.CRDSchemaLocation,
		"-summary",
		"-n", "8",
		"flux/",
	}, anyExit)

	// BOTH STREAMS, ALWAYS. Which one the summary lands on is not a promise
	// kubeconform makes, and the summary is the whole of the measurement —
	// output() folds stderr in only on a non-zero code, and a clean run is
	// exactly where the count still has to be read.
	out, code, err := outputBoth(ctx, ctr)
	if err != nil {
		return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
	}
	valid, summary, ok := checks.KubeconformSummary(out)
	if !ok {
		return checks.VerdictOf(a, 2, out+"\n"+a.ID+": CANNOT RUN - kubeconform printed no summary, so there is no count to read and nothing was measured.")
	}
	if valid == 0 {
		return checks.VerdictOf(a, 2, summary+"\n"+a.ID+": REFUSING a zero-resource validation. Nothing under flux/ was checked against a schema, so 0 invalid means NOTHING WAS EXAMINED - not that the tree is correct.")
	}
	if code != 0 {
		return checks.VerdictOf(a, 1, summary+"\n"+checks.KubeconformFindings(out, 80))
	}
	return checks.VerdictOf(a, 0, summary+"\n"+a.ID+": clean")
}

// Every workload under flux/ passes kube-linter's default checks.
//
// --fail-if-no-objects-found is kube-linter's own zero-population refusal, and
// it exits 1 for it — the same code it uses for findings. Reading that as
// findings would be wrong in the direction that still looks like the check
// worked, so the message is matched and remapped to 2 — in
// checks.KubeLinterState, where a table test holds it, rather than in a case
// statement nobody can run.
func sweepKubeLinter(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("sweep:kube-linter")

	entries, err := r.src.Entries(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the repository root could not be read: "+err.Error())
	}
	if !checks.HasEntry(entries, "flux") {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - no flux/ tree at the repository root.")
	}

	// The refusal arrives on stderr and the findings on stdout, and the code
	// is the same 1 for both — so both streams are read before anything is
	// decided.
	out, code, err := outputBoth(ctx, r.lane(checks.ImageKubeLinterSweep).
		WithExec([]string{"/kube-linter", "lint", "--fail-if-no-objects-found", "flux/"}, anyExit))
	if err != nil {
		return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
	}
	state, reason := checks.KubeLinterState(code, out)
	return checks.VerdictOf(a, state, reason)
}

// forgejoWorkflows answers whether this tree carries .forgejo/workflows and,
// when it does, the body of every file under it.
//
// TWO ATOMS ASK THE SAME QUESTION AND MEAN DIFFERENT THINGS BY THE ANSWER —
// digest-pins calls a missing workflow tree an ABSENCE, portfolio-sbom calls
// it a CANNOT RUN, because it has already found a Dockerfile — so this returns
// the fact and lets each atom file its own verdict.
//
// THE ABSENCE IS TWO ENTRIES DEEP. `.forgejo` present with no `workflows`
// under it is the same nothing as no `.forgejo` at all, and the shell's
// `test -d .forgejo/workflows` asked both at once.
//
// NO GITIGNORE FILTER, deliberately: this is not a population the fleet grades
// but a question about what the repository's CI declares, and the recursive
// match it replaces read the tree as it stood.
func (r *run) forgejoWorkflows(ctx context.Context) (bodies []string, present bool, err error) {
	entries, err := r.src.Entries(ctx)
	if err != nil {
		return nil, false, err
	}
	if !checks.HasEntry(entries, ".forgejo") {
		return nil, false, nil
	}
	sub, err := r.src.Directory(".forgejo").Entries(ctx)
	if err != nil {
		return nil, false, err
	}
	if !checks.HasEntry(sub, "workflows") {
		return nil, false, nil
	}
	paths, err := r.src.Glob(ctx, ".forgejo/workflows/**")
	if err != nil {
		return nil, true, err
	}
	for _, p := range paths {
		// Glob names a directory with a trailing separator, and a directory
		// has no contents to read.
		if strings.HasSuffix(p, "/") {
			continue
		}
		body, err := r.src.File(p).Contents(ctx)
		if err != nil {
			return nil, true, err
		}
		bodies = append(bodies, body)
	}
	return bodies, true, nil
}

// outputBoth evaluates a chain whose last exec carries anyExit and answers
// EVERYTHING it printed — stdout then stderr — with the tool's own exit code.
//
// It is output()'s sibling, and the difference is the whole reason it exists:
// output() folds stderr in only when the code is non-zero, which is right when
// stderr is an error report and wrong when it is half the measurement.
// kubeconform prints its summary to either stream and the summary is the count
// this atom refuses a zero of; kube-linter prints its zero-population refusal
// to stderr with the same exit code it uses for findings. Both have to read
// both on a clean exit.
//
// THE TWO FAILURES STAY DISTINCT, as in output(): err is the engine's (state 2
// for the caller) and code is the tool's (the caller's to interpret).
//
// FOR TESLA19: this belongs beside output() in runtime.go if a second lane
// wants it.
func outputBoth(ctx context.Context, ctr *dagger.Container) (out string, code int, err error) {
	code, err = ctr.ExitCode(ctx)
	if err != nil {
		return "", 0, err
	}
	stdout, err := ctr.Stdout(ctx)
	if err != nil {
		return "", 0, err
	}
	stderr, err := ctr.Stderr(ctx)
	if err != nil {
		return "", 0, err
	}
	return stdout + stderr, code, nil
}
