package main

import (
	"context"
	"sort"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE SWEEP, AS TYPED CHAINS. These four describe a REPOSITORY rather than a
// change, so their answer cannot differ between two pulls against the same
// repo — and running them per pull leaves every repository nobody opened a PR
// against unevaluated indefinitely. That is the whole of CA F9. None
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
	register("sweep:template-render-matrix", sweepTemplateRenderMatrix)
	register("sweep:kubeconform", sweepKubeconform)
	register("sweep:kube-linter", sweepKubeLinter)
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
	const noRepository = ": CANNOT RUN - no .git in the tree under check. The matrix renders the template AT ITS GIT HEAD (--vcs-ref=HEAD is load-bearing, foundry#130); without a repository copier resolves some other tree, and a green from that would be a green about something else."
	if !checks.HasEntry(entries, ".git") {
		return checks.VerdictOf(a, 2, a.ID+noRepository)
	}
	if _, err := r.src.File(".git").Contents(ctx); err == nil {
		return checks.VerdictOf(a, 2, a.ID+noRepository+" Here .git is a FILE, not a directory: this tree came from a linked worktree, and the gitdir it names is a host path that does not exist inside the container. That is a refusal on purpose — an index can be rebuilt, a history cannot.")
	}

	text, err := r.src.File("ci-matrix.toml").Contents(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - ci-matrix.toml could not be read: "+err.Error())
	}
	matrix, err := checks.ParseMatrix(text)
	if err != nil {
		return checks.VerdictOf(a, 2, "::error::"+err.Error())
	}

	// The provisioning probe: the fleet image carries uvx and copier is
	// fetched through it. A missing uvx is state 2 with the engine's error,
	// never a green.
	base := r.gitReady(ctx, r.lane(checks.ImageFleet)).WithExec([]string{"uvx", "--version"})

	names := make([]string, 0, len(matrix.Cases))
	problems := make(map[string][]string, len(matrix.Cases))
	for _, c := range matrix.Cases {
		names = append(names, c.Name)
		found, err := renderCase(ctx, base, c, matrix.Parse)
		if err != nil {
			return neverRan(a, err)
		}
		problems[c.Name] = found
	}
	state, out := checks.MatrixReport(names, problems)
	return checks.VerdictOf(a, state, out)
}

// renderCase renders one case and grades what came out. The error return is
// the ENGINE's — a mount that would not evaluate, an image that would not
// pull; everything the template did wrong is a problem in the slice.
func renderCase(ctx context.Context, base *dagger.Container, c checks.MatrixCase, parse []string) ([]string, error) {
	dest := "/out/" + c.Name
	argv, err := checks.CopierArgv("/src", dest, c.Answers)
	if err != nil {
		return []string{err.Error()}, nil
	}
	ctr := base.WithExec(argv, anyExit)
	out, code, err := outputBoth(ctx, ctr)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return []string{"copier render failed:\n    " + strings.Join(tailLines(out, 12), "\n    ")}, nil
	}

	rendered := ctr.Directory(dest)
	paths, err := rendered.Glob(ctx, "**")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, p := range paths {
		if !strings.HasSuffix(p, "/") {
			files = append(files, p)
		}
	}
	problems := checks.RenderedPathProblems(files)

	// THE EXPECTATIONS MATCH DIRECTORIES TOO, and that is the point of the
	// whole-directory guards a matrix carries: `absent = [".forgejo"]` is how a
	// workflow tree nobody listed comes back noticed.
	for _, pattern := range c.Present {
		hits, err := rendered.Glob(ctx, pattern)
		if err != nil {
			return nil, err
		}
		if len(hits) == 0 {
			problems = append(problems, checks.PresentProblem(pattern))
		}
	}
	for _, pattern := range c.Absent {
		hits, err := rendered.Glob(ctx, pattern)
		if err != nil {
			return nil, err
		}
		if len(hits) > 0 {
			problems = append(problems, checks.AbsentProblem(pattern, trimDirs(hits)))
		}
	}

	parsed, err := parseProblems(ctx, rendered, parse)
	if err != nil {
		return nil, err
	}
	problems = append(problems, parsed...)

	scanned, err := suppressionProblems(ctx, rendered, files)
	if err != nil {
		return nil, err
	}
	return append(problems, scanned...), nil
}

// trimDirs renders a glob's hits the way the script printed them: the paths
// themselves, a directory without its trailing separator.
func trimDirs(hits []string) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, strings.TrimSuffix(h, "/"))
	}
	return out
}

// parseProblems reads every file a `parse` glob matched and answers what would
// not parse. An unknown suffix is an error rather than a silent skip.
func parseProblems(ctx context.Context, rendered *dagger.Directory, parse []string) ([]string, error) {
	var problems []string
	for _, pattern := range parse {
		hits, err := rendered.Glob(ctx, pattern)
		if err != nil {
			return nil, err
		}
		sort.Strings(hits)
		for _, rel := range hits {
			if strings.HasSuffix(rel, "/") {
				continue
			}
			body, err := rendered.File(rel).Contents(ctx)
			if err != nil {
				return nil, err
			}
			if problem := checks.ParseRendered(rel, body); problem != "" {
				problems = append(problems, problem)
			}
		}
	}
	return problems, nil
}

// suppressionProblems scans the rendered tree with the fleet's own suppression
// scan — one definition, not a second copy of the rule.
func suppressionProblems(ctx context.Context, rendered *dagger.Directory, files []string) ([]string, error) {
	var wanted []string
	for _, rel := range files {
		if checks.LanguageOf(rel) != "" {
			wanted = append(wanted, rel)
		}
	}
	bodies := readFiles(ctx, rendered, wanted)
	tree := make(map[string]string, len(bodies))
	for rel, read := range bodies {
		if read.err != nil {
			return nil, read.err
		}
		tree[rel] = read.body
	}
	found, err := checks.ScanTree(tree)
	if err != nil {
		return nil, err
	}
	problems := make([]string, 0, len(found))
	for _, f := range found {
		problems = append(problems, checks.SuppressionProblem(f))
	}
	return problems, nil
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

	ctr := r.lane(checks.ImageKubeconform).WithExec([]string{
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
	out, code, err := outputBoth(ctx, r.lane(checks.ImageKubeLinter).
		WithExec([]string{"/kube-linter", "lint", "--fail-if-no-objects-found", "flux/"}, anyExit))
	if err != nil {
		return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
	}
	state, reason := checks.KubeLinterState(code, out)
	return checks.VerdictOf(a, state, reason)
}

// tailLines is the script's own cut of a failed render: the last n lines of
// what copier said, which is where the reason is.
//
// A CLAMP, NOT A BRANCH, for the reason internal/checks/sweeplane.go already
// records: `if len(lines) > n { lines = lines[len(lines)-n:] }` and the same
// line with `>=` answer identically for every input — at n == len(lines) the
// cut IS the whole slice — so the boundary mutant is EQUIVALENT and no test
// can ever clear it. This file reintroduced the branch form that PR #31 had
// already retired, and the mutation gate caught it again (atoms_sweep.go
// 418:16 LIVED, 419:27 NOT COVERED ×2, measured on PR #77). The clamp has no
// comparison to mutate, and a wrong sign on the arithmetic panics instead of
// being absorbed.
func tailLines(out string, n int) []string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return lines[len(lines)-min(n, len(lines)):]
}
