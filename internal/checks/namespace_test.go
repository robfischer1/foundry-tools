package checks

import (
	"strings"
	"testing"
)

const nsCNP = "apiVersion: cilium.io/v2\nkind: CiliumNetworkPolicy\nmetadata:\n  name: ingress-fence\n"

func TestOpsFluxUnscoped(t *testing.T) {
	got := OpsFluxUnscoped(map[string]string{
		"clusters/p/a.yaml": "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nmetadata: {name: prime}\nspec: {path: ./prime}\n" +
			"---\napiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nmetadata: {name: foundry}\nspec: {path: ./foundry, targetNamespace: foundry}\n",
		// One tree, two CRs: the one with no targetNamespace is the one whose apply fails.
		"clusters/p/b.yaml": "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nmetadata: {name: alloy}\nspec: {path: ./alloy, targetNamespace: prime}\n" +
			"---\napiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nmetadata: {name: alloy-z}\nspec: {path: ./alloy}\n" +
			"---\napiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nmetadata: {name: alloy-b}\nspec: {path: ./alloy}\n",
		// A CR on another repository's source applies none of this tree.
		"clusters/p/c.yaml": "apiVersion: source.toolkit.fluxcd.io/v1\nkind: GitRepository\nmetadata: {name: other}\n" +
			"---\napiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nmetadata: {name: far}\nspec: {path: ./far, sourceRef: {name: other}}\n",
	})
	if len(got) != 2 || strings.Join(got["prime"], ",") != "prime" || strings.Join(got["alloy"], ",") != "alloy-b,alloy-z" {
		t.Errorf("unscoped %v", got)
	}
}

func TestKindScopesComeFromTheCatalogAndTheTreesCRDs(t *testing.T) {
	scopes := kindScopeCatalog()
	err := addCRDScopes(scopes, "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata: {name: a}\nspec: {group: x.io, scope: Namespaced, names: {kind: Thing}}\n"+
		"---\napiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata: {name: b}\nspec: {group: x.io, scope: Cluster, names: {kind: ClusterThing}}\n"+
		// The tree's own CRD overrides the catalog.
		"---\napiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata: {name: c}\nspec: {group: cilium.io, scope: Cluster, names: {kind: CiliumNetworkPolicy}}\n"+
		"---\napiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata: {name: d}\nspec: {group: x.io, scope: Cluster}\n"+
		// Only a CRD declares a scope, whatever another kind's spec says.
		"---\napiVersion: z.io/v1\nkind: Lookalike\nmetadata: {name: e}\nspec: {group: z.io, scope: Cluster, names: {kind: Fake}}\n")
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]bool{
		"/Service": true, "/Namespace": false, "rbac.authorization.k8s.io/ClusterRole": false,
		"cilium.io/CiliumBGPPeerConfig": false, "piraeus.io/LinstorCluster": false,
		"x.io/Thing": true, "x.io/ClusterThing": false, "cilium.io/CiliumNetworkPolicy": false,
	} {
		if got, ok := scopes[key]; !ok || got != want {
			t.Errorf("%s: namespaced=%v known=%v, want %v", key, got, ok, want)
		}
	}
	if _, ok := scopes["z.io/Fake"]; ok {
		t.Error("a kind that is not a CRD declares no scope")
	}
	if _, ok := scopes["x.io/"]; ok {
		t.Error("a CRD naming no kind adds nothing")
	}
	if _, ok := scopes["#"]; ok {
		t.Error("the catalog's header is not a kind")
	}
	if err := addCRDScopes(scopes, ": [bad\n"); err == nil {
		t.Error("a stream that does not parse is an error")
	}
}

// Comments and any line that is not two fields are skipped, a comment of two
// fields included.
func TestParseKindScopes(t *testing.T) {
	got := parseKindScopes("#x n\n# a header line\n/A n\n/B c\n/C n extra\n\n")
	if len(got) != 2 || got["/A"] != true || got["/B"] != false {
		t.Errorf("scopes %v", got)
	}
}

func TestNamespaceless(t *testing.T) {
	scopes := map[string]bool{"/Service": true, "rbac.authorization.k8s.io/ClusterRole": false, "cilium.io/CiliumNetworkPolicy": true}
	missing, unknown, err := Namespaceless(nsCNP+
		"---\napiVersion: v1\nkind: Service\nmetadata: {name: placed, namespace: prime}\n"+
		"---\napiVersion: v1\nkind: Service\nmetadata: {name: loose}\n"+
		"---\napiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata: {name: r}\n"+
		"---\napiVersion: y.io/v1\nkind: Mystery\nmetadata: {name: m1}\n"+
		"---\napiVersion: y.io/v1\nkind: Mystery\nmetadata: {name: m2}\n"+
		"---\napiVersion: y.io/v1\nkind: Mystery\nmetadata: {name: m3, namespace: prime}\n"+
		// An unknown kind does not end the scan: what follows is still read.
		"---\napiVersion: v1\nkind: Service\nmetadata: {name: after}\n"+
		"---\n", scopes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(missing, "|") != "CiliumNetworkPolicy/ingress-fence|Service/loose|Service/after" {
		t.Errorf("missing %q", missing)
	}
	if strings.Join(unknown, "|") != "y.io/Mystery" {
		t.Errorf("unknown %q", unknown)
	}
	if _, _, err := Namespaceless(": [bad\n", scopes); err == nil {
		t.Error("a stream that does not parse is an error")
	}
}

func TestGroupOf(t *testing.T) {
	for in, want := range map[string]string{"v1": "", "apps/v1": "apps", "cilium.io/v2": "cilium.io", "": ""} {
		if got := groupOf(in); got != want {
			t.Errorf("groupOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// flux #264, replayed: prime's CR sets no targetNamespace and its tree builds
// a CiliumNetworkPolicy that names none. foundry's CR sets one, so the same
// object there is placed by Flux and is not a finding.
func TestOpsNamespacelessReport(t *testing.T) {
	paths := []string{"apps", "foundry", "prime"}
	streams := map[string]string{"foundry": nsCNP, "prime": nsCNP + "---\napiVersion: y.io/v1\nkind: Mystery\nmetadata: {name: m}\n"}
	unscoped := map[string][]string{"prime": {"prime"}, "apps": {"apps"}}
	out, bad, err := OpsNamespacelessReport(paths, streams, unscoped)
	if err != nil || !bad {
		t.Fatalf("bad=%v err=%v", bad, err)
	}
	for _, want := range []string{
		"prime: CiliumNetworkPolicy/ingress-fence names no namespace, and Kustomization prime sets no targetNamespace",
		"prime: y.io/Mystery has no known scope",
		"\n1 tree(s) applied with no targetNamespace checked",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "foundry:") || strings.Contains(out, "apps:") {
		t.Errorf("a scoped tree and an unbuilt tree are not checked:\n%s", out)
	}

	out, bad, err = OpsNamespacelessReport(paths, map[string]string{"prime": "apiVersion: v1\nkind: Service\nmetadata: {name: s, namespace: prime}\n"}, unscoped)
	if err != nil || bad || !strings.HasPrefix(out, "1 tree(s) applied") {
		t.Errorf("a placed object passes: bad=%v err=%v\n%s", bad, err, out)
	}

	if _, bad, err := OpsNamespacelessReport(paths, map[string]string{"prime": "apiVersion: v1\nkind: Service\n---\n: [bad\n"}, unscoped); bad || err == nil || !strings.HasPrefix(err.Error(), "prime: ") {
		t.Errorf("a tree that does not parse is an error naming it: %v", err)
	}
	// A CRD in one tree scopes a kind another tree uses.
	out, bad, err = OpsNamespacelessReport([]string{"operators", "prime"}, map[string]string{
		"operators": "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata: {name: m}\nspec: {group: y.io, scope: Namespaced, names: {kind: Mystery}}\n",
		"prime":     "apiVersion: y.io/v1\nkind: Mystery\nmetadata: {name: m}\n",
	}, unscoped)
	if err != nil || !bad || !strings.Contains(out, "prime: Mystery/m names no namespace") {
		t.Errorf("a CRD from another tree scopes the kind: bad=%v err=%v\n%s", bad, err, out)
	}
	if _, _, err := OpsNamespacelessReport([]string{"foundry"}, map[string]string{"foundry": ": [bad\n"}, unscoped); err == nil || !strings.HasPrefix(err.Error(), "foundry: ") {
		t.Errorf("a scoped tree that does not parse is still named: %v", err)
	}
}
