package atoms

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// OPS:IMMUTABLE IN THE BINARY. No pull edits a field the API server refuses to
// change on a live object (a Job's pod template, a workload selector, a
// StatefulSet's claim templates, a binding's roleRef). Every Kustomization path
// whose inputs changed is rendered at the pull's merge base and at its head, and
// checks.ImmutableEdits compares the two. The chain is atoms_immutable.go, whose
// header records why it exists (flux #206); the states, the sentences and the
// kubectl argv are its.
//
// IT READS THE BINARY'S OWN CHECKOUT AND GIT, and fetches nothing: the base is
// in the history the module fetched once for the whole run (Input.Base). The
// merge base is the same resolve the change set uses (mergeBase), so the sha the
// chain's changeBase answered is the sha this answers.
//
// THE BASE TREE IS A LINKED WORKTREE of the checkout, added detached at the
// merge base in a scratch directory and removed when the atom ends. It is the
// one write this atom makes to the repository's metadata (.git/worktrees); it
// writes nothing to the tree, and a leftover entry is pruned.
func opsImmutable(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	settle := func(state int, reason string) checks.Verdict {
		return checks.VerdictOf(a, state, a.ID+": "+reason)
	}
	if in.FilesErr != nil {
		return cannotEnumerate(a, in.FilesErr)
	}
	files := in.Files
	root, ok := checks.FluxRoot(files)
	if !ok {
		return settle(0, "ABSENT - no Flux tree here: neither flux/ nor clusters/<name>/kustomization.yaml at the root.")
	}
	if in.Base == "" {
		return settle(0, "ABSENT - no base to compare against (a tip or local run), so nothing is live to differ from.")
	}

	t := in.tree()
	manifests := map[string]string{}
	for _, m := range checks.OpsFluxClusterManifests(checks.OpsFiles(files), root) {
		src, err := t.read(m)
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

	if out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "kubectl", Args: []string{"version", "--client=true"}, Both: true}); code != 0 {
		return settle(2, fmt.Sprintf("CANNOT RUN - kubectl could not be provisioned: exited %d: %s", code, strings.TrimSpace(out)))
	}
	since, err := mergeBase(ctx, in.Root, in.Base)
	if err != nil {
		return settle(2, "CANNOT RUN - "+err.Error())
	}
	diff, rc := git(ctx, in.Root, "diff", "-z", "--name-only", since, "HEAD")
	if rc != 0 {
		return settle(2, fmt.Sprintf("CANNOT RUN - the changed files against %s would not list (exit %d): %.200s", since, rc, diff))
	}
	changed := checks.SplitNul(diff)
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
		src, err := t.read(k)
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

	baseTree := tempPath("immutable-base", "")
	if out, rc := git(ctx, in.Root, "worktree", "add", "--detach", baseTree, since); rc != 0 {
		return settle(2, fmt.Sprintf("CANNOT RUN - the base %s could not be checked out (exit %d): %.200s", since, rc, strings.TrimSpace(out)))
	}
	defer func() {
		// Not the run's context: a deadline that ended the atom must not leave the
		// worktree registered. A failed removal is pruned with the directory.
		_, _ = git(context.WithoutCancel(ctx), in.Root, "worktree", "remove", "--force", baseTree)
		_ = os.RemoveAll(baseTree)
		_, _ = git(context.WithoutCancel(ctx), in.Root, "worktree", "prune")
	}()

	var edits, notes []string
	for i, p := range touched {
		head, err := renderAt(ctx, in, p, tempPath("immutable-head-"+strconv.Itoa(i), ".yaml"))
		if errors.Is(err, errNotBuilt) {
			// ops:flux reports a head that does not build; this atom has
			// nothing to compare.
			notes = append(notes, p+": head does not build (ops:flux reports it)")
			continue
		}
		if err != nil {
			return settle(2, "CANNOT RUN - "+err.Error())
		}
		base, err := renderAt(ctx, in, baseTree+"/"+p, tempPath("immutable-base-"+strconv.Itoa(i), ".yaml"))
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

// errNotBuilt is renderAt's answer for a directory kubectl kustomize refused: not
// a fault of the atom, and the caller notes and skips it.
var errNotBuilt = errors.New("kubectl kustomize refused it")

// renderAt builds one Kustomization directory with kubectl kustomize into dest,
// reads it back and removes it. A program that would not start is an error; one
// that refused the directory is errNotBuilt.
func renderAt(ctx context.Context, in Input, dir, dest string) (string, error) {
	defer func() { _ = os.Remove(dest) }()
	out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "kubectl", Args: []string{"kustomize", dir, "-o", dest}, Both: true})
	if code < 0 {
		return "", errors.New("the atom never ran: " + out)
	}
	if code != 0 {
		return "", errNotBuilt
	}
	b, err := os.ReadFile(dest)
	return string(b), err
}
