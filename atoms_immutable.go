package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

func init() {
	register("ops:immutable", opsImmutable)
}

// ops:immutable — no pull edits a field the API server refuses to change on a
// live object. Every Kustomization path the tree's CRs apply is rendered at
// the pull's merge base and at its head, and checks.ImmutableEdits compares
// the two (a Job's pod template, a workload selector, a StatefulSet's claim
// templates, a binding's roleRef, ...).
//
// WHY IT EXISTS: flux #206 (2026-10-02) dropped a CPU limit inside
// Job/devpi-provision-3's template. ops:flux built it and kubeconform passed
// it, because both judge the HEAD alone; the API server refused it, Flux's
// dry-run failed, and the foundry Kustomization applied nothing until #209
// reverted it. Only a comparison against what is already live can see that,
// and the merge base is what is live once the previous pull landed.
//
// STATIC, NO CLUSTER CREDENTIAL (Rob, 2026-10-02). A live server dry-run would
// also catch CRD webhooks but needs a gate identity that can create and patch
// every kind; gate pods hold none, by design.
//
// NEEDS THE BASE, SO IT IS A PUSH-STAGE ATOM. With no base (a tip run, a local
// pre-push with no --base) there is nothing to compare and it stands down
// saying so. A base path that does not build is noted and skipped: the base is
// what landed, and a pull that fixes a broken base is not this atom's finding.
func opsImmutable(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("ops:immutable")
	settle := func(state int, reason string) checks.Verdict {
		return checks.VerdictOf(a, state, a.ID+": "+reason)
	}
	files, err := r.population(ctx)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	root, ok := checks.FluxRoot(files)
	if !ok {
		return settle(0, "ABSENT - no Flux tree here: neither flux/ nor clusters/<name>/kustomization.yaml at the root.")
	}
	if r.base == "" {
		return settle(0, "ABSENT - no base to compare against (a tip or local run), so nothing is live to differ from.")
	}

	manifests := map[string]string{}
	for _, m := range checks.OpsFluxClusterManifests(checks.OpsFiles(files), root) {
		src, err := r.src.File(m).Contents(ctx)
		if err != nil {
			return settle(2, "CANNOT RUN - "+m+" could not be read: "+err.Error())
		}
		manifests[m] = src
	}
	paths, _ := checks.OpsFluxPaths(manifests)
	if len(paths) == 0 {
		paths = checks.OpsFluxFallback(checks.OpsFiles(files), root)
	}
	if len(paths) == 0 {
		return settle(0, "ABSENT - the Flux tree carries no Kustomization CR and no kustomization.yaml")
	}

	kubectl, err := fetchTool(ctx, checks.KubectlURL)
	if err != nil {
		return settle(2, "CANNOT RUN - kubectl could not be fetched: "+err.Error())
	}
	ctr := r.gitReady(ctx, r.withBase(r.lane(checks.ImageFleet))).
		WithFile("/usr/local/bin/kubectl", kubectl, dagger.ContainerWithFileOpts{Permissions: 0o755})
	since, err := r.changeBase(ctx, ctr)
	if err != nil {
		return settle(2, "CANNOT RUN - "+err.Error())
	}
	if since == "" {
		return settle(2, "CANNOT RUN - the base "+r.base+" is not in this history")
	}
	diff, rc, err := output(ctx, ctr.WithExec([]string{"git", "diff", "--name-only", since, "HEAD"}, anyExit))
	if err != nil || rc != 0 {
		return settle(2, fmt.Sprintf("CANNOT RUN - the changed files against %s would not list (exit %d): %v %.200s", since, rc, err, diff))
	}
	changed := strings.Fields(diff)
	tracked := map[string]bool{}
	for _, f := range files {
		tracked[f] = true
	}
	var readErr error
	read := func(dir string) string {
		k := dir + "/kustomization.yaml"
		if !tracked[k] {
			return ""
		}
		src, err := r.src.File(k).Contents(ctx)
		if err != nil {
			readErr = errors.New(k + " could not be read: " + err.Error())
		}
		return src
	}
	var touched []string
	for _, p := range paths {
		if checks.ImmutableTouched(checks.ImmutableInputs(p, read), changed) {
			touched = append(touched, p)
		}
	}
	if readErr != nil {
		return settle(2, "CANNOT RUN - "+readErr.Error())
	}
	if len(touched) == 0 {
		return settle(0, fmt.Sprintf("PASS - no Flux path's inputs changed against %s; %d path(s), nothing to compare", since, len(paths)))
	}
	ctr = ctr.WithExec([]string{"git", "worktree", "add", "--detach", "/tmp/immutable-base", since})

	var edits, notes []string
	for i, p := range touched {
		head, err := renderAt(ctx, ctr, p, "/tmp/immutable-head."+strconv.Itoa(i)+".yaml")
		if errors.Is(err, errNotBuilt) {
			// ops:flux reports a head that does not build; this atom has
			// nothing to compare.
			notes = append(notes, p+": head does not build (ops:flux reports it)")
			continue
		}
		if err != nil {
			return settle(2, "CANNOT RUN - "+err.Error())
		}
		base, err := renderAt(ctx, ctr, "/tmp/immutable-base/"+p, "/tmp/immutable-base."+strconv.Itoa(i)+".yaml")
		if errors.Is(err, errNotBuilt) {
			notes = append(notes, p+": no buildable base (new or broken there), skipped")
			continue
		}
		if err != nil {
			return settle(2, "CANNOT RUN - "+err.Error())
		}
		found, err := checks.ImmutableEdits(base, head)
		if err != nil {
			return settle(2, "CANNOT RUN - "+p+": "+err.Error())
		}
		for _, f := range found {
			edits = append(edits, p+": "+f)
		}
	}
	var out strings.Builder
	for _, n := range notes {
		out.WriteString(n + "\n")
	}
	if len(edits) > 0 {
		out.WriteString("FINDINGS - these edits change a field the API server refuses on a live object; Flux's dry-run will fail and the Kustomization will stop applying. Rename the object, or annotate it kustomize.toolkit.fluxcd.io/force: enabled to have Flux recreate it:\n")
		for _, e := range edits {
			out.WriteString("  " + e + "\n")
		}
		return settle(1, out.String())
	}
	out.WriteString("PASS - " + strconv.Itoa(len(touched)) + " path(s) compared against " + since + ", the " + strconv.Itoa(len(touched)) + " of " + strconv.Itoa(len(paths)) + " whose inputs changed")
	return settle(0, out.String())
}

// errNotBuilt is renderAt's answer for a directory kubectl kustomize refused:
// not an engine fault, and the caller notes and skips it.
var errNotBuilt = errors.New("kubectl kustomize refused it")

// renderAt builds one Kustomization directory with kubectl kustomize into
// dest and reads it back.
func renderAt(ctx context.Context, ctr *dagger.Container, dir, dest string) (string, error) {
	next := ctr.WithExec([]string{"kubectl", "kustomize", dir, "-o", dest}, anyExit)
	code, err := next.ExitCode(ctx)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", errNotBuilt
	}
	return next.File(dest).Contents(ctx)
}
