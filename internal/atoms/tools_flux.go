package atoms

import (
	"context"
	"fmt"
	"os"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// kubeconformArgs are ops.sh's flags: strict, missing schemas ignored, CRDs
// skipped, the default schema location. The built files follow.
var kubeconformArgs = []string{"-strict", "-summary", "-ignore-missing-schemas", "-skip", "CustomResourceDefinition", "-schema-location", "default"}

// opsFlux: every tree a Flux Kustomization CR under flux/clusters/ applies,
// built with `kubectl kustomize` the way kustomize-controller will, and then
// the built objects schema-checked with kubeconform. No CR: every flux/<dir>
// with a kustomization.yaml.
//
// ABSENCE COMES FIRST, all three kinds of it (no ops shape, no Flux tree, no
// Kustomization and no kustomization.yaml), before kubectl or kubeconform is
// asked anything: on infra the chain paid 7.1s to provision them and then
// settled ABSENT.
//
// THE BUILT STREAM GOES TO A FILE kubectl writes (`-o`), never to a pipe this
// process would hold: a kustomize build is the whole rendered tree, and
// kubeconform reads the per-tree files in place, one argument each.
func opsFlux(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if stop := opsSurface(a, in); stop != nil {
		return *stop
	}
	files := checks.OpsFiles(in.Committable)
	tracked := in.Committable
	root, ok := checks.FluxRoot(tracked)
	if !ok {
		return opsPhaseAbsent(a, "no Flux tree here: neither flux/ nor clusters/<name>/kustomization.yaml at the root")
	}
	t := in.tree()
	manifests := map[string]string{}
	for _, m := range checks.OpsFluxClusterManifests(files, root) {
		src, err := t.read(m)
		if err != nil {
			return neverRan(a, err.Error())
		}
		manifests[m] = src
	}
	paths, problems := checks.OpsFluxPaths(manifests)
	if len(paths) == 0 {
		paths = checks.OpsFluxFallback(files, root)
	}
	if len(paths) == 0 {
		return opsPhaseAbsent(a, "the Flux tree carries no Kustomization CR and no kustomization.yaml")
	}
	if stop := probeTool(ctx, a, in, "kubectl", "version", "--client=true"); stop != nil {
		return *stop
	}
	if stop := probeTool(ctx, a, in, "kubeconform", "-v"); stop != nil {
		return *stop
	}

	var out, built strings.Builder
	var rendered, scratch []string
	defer func() {
		for _, f := range scratch {
			_ = os.Remove(f)
		}
	}()
	streams := map[string]string{}
	for _, p := range problems {
		out.WriteString(p + "\n")
	}
	rc := 0
	for _, p := range paths {
		out.WriteString("kustomize build " + p + "\n")
		file := tempPath("kustomize", ".yaml")
		scratch = append(scratch, file)
		o, code := in.run(ctx, Cmd{Dir: in.Root, Name: "kubectl", Args: []string{"kustomize", p, "-o", file}, Both: true})
		if code < 0 {
			return neverRan(a, o)
		}
		if code != 0 {
			out.WriteString(o + "  build FAILED: " + p + "\n")
			rc = 1
			continue
		}
		rendered = append(rendered, file)
		stream, err := os.ReadFile(file)
		if err != nil {
			return neverRan(a, err.Error())
		}
		// The stream is read back only to COUNT what was built. A separator
		// between trees: kustomize ends its stream without one, and two trees
		// back to back fuse at the boundary (ops-infra-gfrpk, 2026-09-06).
		built.WriteString(string(stream) + "\n---\n")
		streams[p] = string(stream)
	}
	if rc != 0 {
		state, report := opsSettled("flux", rc, out.String())
		return checks.VerdictOf(a, state, report)
	}
	fmt.Fprintf(&out, "%d object(s) built from %d tree(s)\n", checks.OpsKinds(built.String()), len(paths))
	validated, rc := in.run(ctx, Cmd{Dir: in.Root, Name: "kubeconform", Args: append(append([]string{}, kubeconformArgs...), rendered...), Both: true})
	if rc < 0 {
		return neverRan(a, validated)
	}
	out.WriteString(validated)
	// A tree its CR applies with no targetNamespace must place every namespaced
	// object itself (checks.OpsNamespacelessReport; flux #264).
	report, bad, err := checks.OpsNamespacelessReport(paths, streams, checks.OpsFluxUnscoped(manifests))
	if err != nil {
		out.WriteString("a built tree did not parse: " + err.Error() + "\n")
		bad = true
	}
	out.WriteString(report)
	if bad && rc == 0 {
		rc = 1
	}
	state, text := opsSettled("flux", rc, out.String())
	return checks.VerdictOf(a, state, text)
}
