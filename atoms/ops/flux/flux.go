// Package opsflux is the atom ops:flux: every Flux Kustomization builds with
// kubectl kustomize and its objects pass kubeconform. One file holds the atom
// and both runners execute it (lld.md FN-085).
package opsflux

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. kubectl and kubeconform are the pinned binaries the tools
// container carries.
var Def = atomkit.Def{
	ID: "ops:flux", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every Flux Kustomization under flux/ builds with kubectl kustomize — a duplicate resource id, a missing base or a bad patch is a finding before flux meets it, and so is a namespaced object that names no namespace under a CR that sets no targetNamespace.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"tool:kubectl", "tool:kubeconform"},
	Check:     check,
}

const id = "ops:flux"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// kubeconformArgs are ops.sh's flags: strict, missing schemas ignored, CRDs
// skipped, the default schema location. The built files follow. This pass
// gates; crdPass appends the CRD catalogue to the same flags and only warns.
var kubeconformArgs = []string{"-strict", "-summary", "-ignore-missing-schemas", "-skip", "CustomResourceDefinition", "-schema-location", "default"}

// crdSkipped is the count kubeconform's -summary line ends on.
var crdSkipped = regexp.MustCompile(`Skipped: (\d+)`)

// crdProbeDoc is one HelmRelease the catalogue has a schema for
// (checks.CRDSchemaProbe names the same file). Validated WITHOUT
// -ignore-missing-schemas, so a schema that cannot be fetched fails the probe
// where the scan would have counted it skipped.
const crdProbeDoc = `apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: probe
spec:
  interval: 1h
`

// crdPass validates the built trees against the default store AND the CRDs
// catalogue, so HelmRelease, Kustomization, CiliumNetworkPolicy and the rest
// are read instead of skipped. It is WARN-ONLY, faults included: its invalid
// findings are printed and never change the exit (replayed over flux's last 20
// merges, the catalogue's ObjectStore schema refuses all eight barman
// ObjectStores on every one of them), and a catalogue the lane cannot fetch is
// printed as UNCHECKED rather than turning the atom into a 2, so a flaky
// catalogue cannot block a flux landing the gating pass cleared. What it never
// is: silent. The probe runs WITHOUT -ignore-missing-schemas, so an
// unreachable catalogue is named instead of rendered as "skipped". When this
// pass graduates to gating, its faults become 2s.
func crdPass(ctx context.Context, in atomkit.In, rendered []string, out *strings.Builder) {
	unchecked := func(why string) {
		fmt.Fprintf(out, "CRD catalogue pass (warn-only): custom resources went UNCHECKED this run, not clean: %s\n", why)
	}
	// kubeconform reads stdin when it is named no file.
	o, rc := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "kubeconform", Args: []string{"-strict", "-schema-location", checks.CRDSchemaLocation}, Stdin: crdProbeDoc, Both: true})
	if rc != 0 {
		unchecked(fmt.Sprintf("the catalogue could not be read (%s, rc=%d): %s", checks.CRDSchemaProbe, rc, strings.TrimSpace(o)))
		return
	}
	args := append(append([]string{}, kubeconformArgs...), "-schema-location", checks.CRDSchemaLocation)
	o, rc = in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "kubeconform", Args: append(args, rendered...), Both: true})
	if rc < 0 {
		unchecked(strings.TrimSpace(o))
		return
	}
	if rc != 0 {
		if state, text := atomlib.OpsSettled("flux CRD catalogue pass", rc, o); state == 2 {
			unchecked(strings.TrimSpace(text))
			return
		}
	}
	out.WriteString("CRD catalogue pass (warn-only; findings here do not fail the atom):\n" + strings.TrimRight(o, "\n") + "\n")
	if m := crdSkipped.FindStringSubmatch(o); m != nil {
		fmt.Fprintf(out, "%s resource(s) skipped for a missing schema (the catalogue lacks the kind)\n", m[1])
	}
}

// check: every tree a Flux Kustomization CR under flux/clusters/ applies,
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
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if stop := atomlib.OpsSurface(row, in); stop != nil {
		return *stop
	}
	files := checks.OpsFiles(in.Committable)
	tracked := in.Committable
	root, ok := checks.FluxRoot(tracked)
	if !ok {
		return atomlib.PhaseAbsent(row, "no Flux tree here: neither flux/ nor clusters/<name>/kustomization.yaml at the root")
	}
	t := in.Tree()
	manifests := map[string]string{}
	for _, m := range checks.OpsFluxClusterManifests(files, root) {
		src, err := t.Read(m)
		if err != nil {
			return atomlib.NeverRan(row, err.Error())
		}
		manifests[m] = src
	}
	paths, problems := checks.OpsFluxPaths(manifests)
	if len(paths) == 0 {
		paths = checks.OpsFluxFallback(files, root)
	}
	if len(paths) == 0 {
		return atomlib.PhaseAbsent(row, "the Flux tree carries no Kustomization CR and no kustomization.yaml")
	}
	if stop := atomlib.ProbeTool(ctx, row, in, "kubectl", "version", "--client=true"); stop != nil {
		return *stop
	}
	if stop := atomlib.ProbeTool(ctx, row, in, "kubeconform", "-v"); stop != nil {
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
		file := atoms.TempPath("kustomize", ".yaml")
		scratch = append(scratch, file)
		o, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "kubectl", Args: []string{"kustomize", p, "-o", file}, Both: true})
		if code < 0 {
			return atomlib.NeverRan(row, o)
		}
		if code != 0 {
			out.WriteString(o + "  build FAILED: " + p + "\n")
			rc = 1
			continue
		}
		rendered = append(rendered, file)
		stream, err := os.ReadFile(file)
		if err != nil {
			return atomlib.NeverRan(row, err.Error())
		}
		// The stream is read back only to COUNT what was built. A separator
		// between trees: kustomize ends its stream without one, and two trees
		// back to back fuse at the boundary (ops-infra-gfrpk, 2026-09-06).
		built.WriteString(string(stream) + "\n---\n")
		streams[p] = string(stream)
	}
	if rc != 0 {
		state, report := atomlib.OpsSettled("flux", rc, out.String())
		return checks.VerdictOf(row, state, report)
	}
	fmt.Fprintf(&out, "%d object(s) built from %d tree(s)\n", checks.OpsKinds(built.String()), len(paths))
	validated, rc := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "kubeconform", Args: append(append([]string{}, kubeconformArgs...), rendered...), Both: true})
	if rc < 0 {
		return atomlib.NeverRan(row, validated)
	}
	out.WriteString(validated)
	if rc == 0 {
		crdPass(ctx, in, rendered, &out)
	}
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
	state, text := atomlib.OpsSettled("flux", rc, out.String())
	return checks.VerdictOf(row, state, text)
}
