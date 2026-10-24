package l4erasure

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomtest"
)

// hadesDeploy is the Deployment whose template labels the face's pod carries.
const hadesDeploy = `apiVersion: apps/v1
kind: Deployment
metadata: {name: hades, namespace: prime}
spec:
  template:
    metadata:
      labels: {app: hades}
    spec:
      serviceAccountName: hades-sa
`

// ccnp is a clusterwide policy on every prime pod but the excluded SAs, with one ingress rule.
func ccnp(name, excluded, rule string) string {
	return `apiVersion: cilium.io/v2
kind: CiliumClusterwideNetworkPolicy
metadata:
  name: ` + name + `
spec:
  endpointSelector:
    matchLabels:
      io.kubernetes.pod.namespace: prime
    matchExpressions:
      - {key: io.cilium.k8s.policy.serviceaccount, operator: NotIn, values: [` + excluded + `]}
  ingress:
` + rule
}

// cnpOnHades is a namespaced policy on hades-sa with the given body under spec.
func cnpOnHades(ns, name, body string) string {
	return `apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: ` + name + `
  namespace: ` + ns + `
spec:
  endpointSelector:
    matchLabels: {"k8s:io.cilium.k8s.policy.serviceaccount": hades-sa}
` + body
}

const fromNyx = `    - fromEndpoints:
        - matchLabels: {io.kubernetes.pod.namespace: prime, io.cilium.k8s.policy.serviceaccount: nyx-sa}
`

func ports(spec string) string {
	return fromNyx + "      toPorts:\n        - ports: [" + spec + "]\n"
}

const l7 = fromNyx + `      toPorts:
        - ports: [{port: "8101", protocol: TCP}]
          rules:
            http:
              - {method: GET, path: "^/v1/served$"}
`

const denyHost = `  ingressDeny:
    - fromEntities: [host, world]
      toPorts:
        - ports: [{port: "8101", protocol: TCP}]
`

const denyAll = `  ingressDeny:
    - fromEntities: [all]
      toPorts:
        - ports: [{port: "8101", protocol: TCP}]
`

const netpol = `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: np, namespace: prime}
spec:
  podSelector:
    matchLabels: {app: %s}
  ingress:
    - ports: [{port: 8101}]
`

func erasureTree(extra map[string]string) map[string]string {
	f := map[string]string{"prime/star-hades.yaml": hadesDeploy, "README.md": "not yaml"}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

// erased runs Erased over a tree of the given files.
func erased(t *testing.T, files map[string]string) ([]string, error) {
	t.Helper()
	in := atomtest.TreeIn(t, files)
	return Erased(in.Root, append(in.Committable, "prime/gone.yaml"))
}

// UT-l7-5: an L4 allow reaching hades's :8101 is a finding naming the policy;
// http rules, another port or protocol, a carve-out, or a covering deny are not.
func TestAnL4AdmissionToHadesFaceIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []string // empty: pass
	}{
		{"the nyx-prober floor, all ports, hades not carved out (ch67)",
			map[string]string{"network/floor.yaml": ccnp("forge-baseline-nyx-prober", "kairos-sa", ports(`{port: "0", protocol: TCP}`))},
			[]string{"forge-baseline-nyx-prober", "network/floor.yaml", "nyx-sa", "every port"}},
		{"the same floor with hades-sa carved out",
			map[string]string{"network/floor.yaml": ccnp("forge-baseline-nyx-prober", "kairos-sa, hades-sa", ports(`{port: "0", protocol: TCP}`))}, nil},
		{"no toPorts at all",
			map[string]string{"network/floor.yaml": ccnp("floor", "kairos-sa", fromNyx)}, []string{"floor", "every port"}},
		{"a toPorts entry with no ports",
			map[string]string{"network/floor.yaml": ccnp("floor", "kairos-sa", fromNyx+"      toPorts:\n        - {}\n")}, []string{"floor", "every port"}},
		{"8101 by number",
			map[string]string{"prime/p.yaml": cnpOnHades("prime", "bynum", "  ingress:\n"+ports(`{port: "8101", protocol: TCP}`))}, []string{"bynum", "no http rules"}},
		{"8101 by the port's name",
			map[string]string{"prime/p.yaml": cnpOnHades("prime", "byname", "  ingress:\n"+ports(`{port: openapi-http}`))}, []string{"byname"}},
		{"8101 inside a range",
			map[string]string{"prime/p.yaml": cnpOnHades("prime", "byrange", "  ingress:\n"+ports(`{port: "8100", endPort: 8102, protocol: ANY}`))}, []string{"byrange"}},
		{"8101 with http rules", map[string]string{"prime/p.yaml": cnpOnHades("prime", "grants", "  ingress:\n"+l7)}, nil},
		{"8102 only", map[string]string{"prime/p.yaml": cnpOnHades("prime", "mtls", "  ingress:\n"+ports(`{port: "8102", protocol: TCP}`))}, nil},
		{"8101 over UDP", map[string]string{"prime/p.yaml": cnpOnHades("prime", "udp", "  ingress:\n"+ports(`{port: "8101", protocol: UDP}`))}, nil},
		{"ICMP only", map[string]string{"prime/p.yaml": cnpOnHades("prime", "icmp", "  ingress:\n    - fromEntities: [health]\n      icmps: [{fields: [{type: 8}]}]\n")}, nil},
		{"a policy in another namespace", map[string]string{"apps/p.yaml": cnpOnHades("apps", "elsewhere", "  ingress:\n"+fromNyx)}, nil},
		{"the host on every port, uncovered",
			map[string]string{"prime/p.yaml": cnpOnHades("prime", "kubelet", "  ingress:\n    - fromEntities: [host]\n")}, []string{"kubelet", "fromEntities: - host"}},
		{"the host on every port, the fence denies host on 8101",
			map[string]string{"prime/p.yaml": cnpOnHades("prime", "kubelet", "  ingress:\n    - fromEntities: [host]\n"), "prime/fence.yaml": cnpOnHades("prime", "fence", denyHost)}, nil},
		{"an endpoint on every port: an entity deny does not cover it",
			map[string]string{"network/floor.yaml": ccnp("floor", "kairos-sa", fromNyx), "prime/fence.yaml": cnpOnHades("prime", "fence", denyHost)}, []string{"floor"}},
		{"an endpoint on every port while the door is closed to all",
			map[string]string{"network/floor.yaml": ccnp("floor", "kairos-sa", fromNyx), "prime/fence.yaml": cnpOnHades("prime", "closed", denyAll)}, nil},
		{"a NetworkPolicy on hades's template label",
			map[string]string{"prime/np.yaml": strings.Replace(netpol, "%s", "hades", 1)}, []string{"NetworkPolicy prime/np", "has no L7"}},
		{"a NetworkPolicy with no ports",
			map[string]string{"prime/np.yaml": strings.Replace(strings.Replace(netpol, "%s", "hades", 1), "    - ports: [{port: 8101}]\n", "    - {}\n", 1)}, []string{"np", "every port", "every peer"}},
		{"an egress-only NetworkPolicy",
			map[string]string{"prime/np.yaml": strings.Replace(strings.Replace(netpol, "%s", "hades", 1), "  ingress:", "  policyTypes: [Egress]\n  ingress:", 1)}, nil},
		{"a CCNP's specs list, from a CIDR",
			map[string]string{"network/s.yaml": "apiVersion: cilium.io/v2\nkind: CiliumClusterwideNetworkPolicy\nmetadata: {name: multi}\nspecs:\n  - endpointSelector: {matchLabels: {app: hades}}\n    ingress:\n      - fromCIDR: [10.0.0.0/8]\n"}, []string{"multi", "fromCIDR"}},
		{"a CNP with no endpointSelector", map[string]string{"prime/p.yaml": "apiVersion: cilium.io/v2\nkind: CiliumNetworkPolicy\nmetadata: {name: nosel, namespace: prime}\nspec:\n  ingress:\n    - {}\n"}, nil},
		{"the host, a deny on every port",
			map[string]string{"prime/p.yaml": cnpOnHades("prime", "kubelet", "  ingress:\n    - fromEntities: [host]\n"), "prime/fence.yaml": cnpOnHades("prime", "fence", "  ingressDeny:\n    - fromEntities: [host]\n")}, nil},
		{"the host, a deny on another port only",
			map[string]string{"prime/p.yaml": cnpOnHades("prime", "kubelet", "  ingress:\n    - fromEntities: [host]\n"), "prime/fence.yaml": cnpOnHades("prime", "fence", "  ingressDeny:\n    - fromEntities: [all]\n      toPorts:\n        - ports: [{port: \"6060\"}]\n")}, []string{"kubelet"}},
		{"another port by name", map[string]string{"prime/p.yaml": cnpOnHades("prime", "named", "  ingress:\n"+ports(`{port: metrics}`))}, nil},
		{"a NetworkPolicy in another namespace",
			map[string]string{"apps/np.yaml": strings.Replace(strings.Replace(netpol, "%s", "hades", 1), "namespace: prime", "namespace: apps", 1)}, nil},
		{"Deployments elsewhere lend hades no labels",
			map[string]string{
				"apps/cm.yaml":  "kind: ConfigMap\nmetadata: {name: x}\n",
				"apps/d.yaml":   strings.Replace(strings.Replace(hadesDeploy, "namespace: prime", "namespace: apps", 1), "app: hades", "app: other", 1),
				"prime/k.yaml":  strings.Replace(strings.Replace(hadesDeploy, "hades-sa", "kairos-sa", 1), "app: hades", "app: other", 1),
				"prime/np.yaml": strings.Replace(netpol, "%s", "other", 1),
			}, nil},
		{"a NetworkPolicy on another app", map[string]string{"prime/np.yaml": strings.Replace(netpol, "%s", "kairos", 1)}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := erased(t, erasureTree(tc.files))
			if err != nil {
				t.Fatal(err)
			}
			if len(tc.want) == 0 {
				if len(got) != 0 {
					t.Fatalf("want no finding, got:\n%s", strings.Join(got, "\n"))
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want one finding, got %d:\n%s", len(got), strings.Join(got, "\n"))
			}
			for _, w := range append([]string{"voids the L7 stamp", "prime/hades-sa :8101"}, tc.want...) {
				if !strings.Contains(got[0], w) {
					t.Errorf("finding lacks %q:\n%s", w, got[0])
				}
			}
		})
	}
}

// A policy file that does not parse could hide an admission: an error, never
// a clean answer. A Deployment file that does not parse holds no policy.
func TestAnUnparseablePolicyIsAnError(t *testing.T) {
	_, err := erased(t, erasureTree(map[string]string{"prime/bad.yaml": "kind: CiliumNetworkPolicy\nspec: [\n"}))
	if err == nil || !strings.Contains(err.Error(), "prime/bad.yaml") || !strings.Contains(err.Error(), "does not parse") {
		t.Fatalf("want a parse error naming the file, got %v", err)
	}
	got, err := erased(t, erasureTree(map[string]string{"prime/d.yml": "kind: Deployment\nspec: [\n"}))
	if err != nil || len(got) != 0 {
		t.Fatalf("an unparseable Deployment: %v %v", got, err)
	}
}

// A file that cannot be read (a directory named like YAML) is an error.
func TestAnUnreadableFileIsAnError(t *testing.T) {
	in := atomtest.TreeIn(t, map[string]string{"dir.yaml/x": "x"})
	if _, err := Erased(in.Root, []string{"dir.yaml"}); err == nil {
		t.Fatal("want an error")
	}
}

// Every selector operator, the reserved: prefix and an unknown operator.
func TestSelectorOperators(t *testing.T) {
	labels := map[string]string{"app": "hades", "tier": "edge"}
	for _, tc := range []struct {
		name string
		sel  map[string]any
		want bool
	}{
		{"In", map[string]any{"matchExpressions": []any{map[string]any{"key": "app", "operator": "In", "values": []any{"hades"}}}}, true},
		{"In, absent", map[string]any{"matchExpressions": []any{map[string]any{"key": "zone", "operator": "In", "values": []any{"a"}}}}, false},
		{"NotIn, listed", map[string]any{"matchExpressions": []any{map[string]any{"key": "app", "operator": "NotIn", "values": []any{"hades"}}}}, false},
		{"Exists", map[string]any{"matchExpressions": []any{map[string]any{"key": "any:tier", "operator": "Exists"}}}, true},
		{"Exists, absent", map[string]any{"matchExpressions": []any{map[string]any{"key": "zone", "operator": "Exists"}}}, false},
		{"DoesNotExist", map[string]any{"matchExpressions": []any{map[string]any{"key": "zone", "operator": "DoesNotExist"}}}, true},
		{"DoesNotExist, present", map[string]any{"matchExpressions": []any{map[string]any{"key": "app", "operator": "DoesNotExist"}}}, false},
		{"unknown operator", map[string]any{"matchExpressions": []any{map[string]any{"key": "app", "operator": "Gt"}}}, false},
		{"reserved label", map[string]any{"matchLabels": map[string]any{"reserved:host": ""}}, false},
		{"reserved expression", map[string]any{"matchExpressions": []any{map[string]any{"key": "reserved:host", "operator": "Exists"}}}, false},
		{"empty selects all", map[string]any{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := matches(tc.sel, labels); got != tc.want {
				t.Errorf("matches = %v, want %v", got, tc.want)
			}
		})
	}
}
