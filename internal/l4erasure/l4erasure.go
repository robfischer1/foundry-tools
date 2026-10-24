// Package l4erasure finds policies that admit a peer to an attributed face at
// L4. The atom fleet:l7-render (ERASURE) runs it over flux's tree.
//
// ERASURE. An attributed face names its caller by the X-Forge-Peer header a
// Cilium L7 rule REPLACE-stamps. Policies are a union of allows: one L3/L4
// allow for a peer on that port, from any CNP, CCNP or NetworkPolicy, admits
// the peer without the Envoy redirect, so its own X-Forge-Peer reaches the
// listener unstamped and it can name itself anything (ch67: the nyx-prober
// floor's all-ports allow on charon). So no policy may admit a peer to a face
// port at L4: every allow selecting the face's pod must carry http rules on it,
// unless an ingressDeny on the same pod and port covers that peer (deny wins).
//
// Deny coverage is read narrowly, on purpose: an entity allow is covered by a
// deny naming that entity or `all`; an endpoint, CIDR or node allow only by a
// deny from `all` (the closed door). Anything else is a finding, so a fence
// rewrite cannot quietly make an L4 floor live.
package l4erasure

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// face is an attributed REST door: the pod (namespace + ServiceAccount) and the
// port, by number and by the Service/container port name policies may use.
type face struct {
	namespace, sa string
	port          int
	portName      string
}

// faces are the doors guarded. hades's :8101 (hades edge plan S3): its fence
// and grants-hades admit callers by ServiceAccount, and only the stamp names them.
var faces = []face{{namespace: "prime", sa: "hades-sa", port: 8101, portName: "openapi-http"}}

// policy is one decoded policy document and where it came from.
type policy struct {
	file, kind, name, namespace string
	specs                       []map[string]any
}

// Erased lists every L4 admission to a guarded face in the tree's tracked
// YAML, one finding each, sorted.
func Erased(root string, tracked []string) ([]string, error) {
	pols, deploys, err := readPolicies(root, tracked)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, f := range faces {
		labels := podLabels(f, deploys)
		var denies []map[string]any
		var allows []struct {
			p    policy
			rule map[string]any
		}
		for _, p := range pols {
			for _, s := range p.specs {
				if !p.selects(f, s, labels) {
					continue
				}
				denies = append(denies, maps(s["ingressDeny"])...)
				ingress := maps(s["ingress"])
				if p.kind == "NetworkPolicy" {
					ingress = netpolIngress(s)
				}
				for _, r := range ingress {
					allows = append(allows, struct {
						p    policy
						rule map[string]any
					}{p, r})
				}
			}
		}
		for _, a := range allows {
			if why := a.p.admitsL4(f, a.rule, denies); why != "" {
				out = append(out, fmt.Sprintf("%s %s (%s) admits %s to %s/%s :%d at L4 (%s), which voids the L7 stamp there: give it http rules on :%d or drop the port",
					a.p.kind, strings.TrimPrefix(a.p.namespace+"/"+a.p.name, "/"), a.p.file, peer(a.rule), f.namespace, f.sa, f.port, why, f.port))
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// readPolicies decodes every Cilium and Kubernetes network policy, and every
// Deployment, in the tracked YAML. A file that names a network policy and does
// not parse is an error: the gate could not see what it admits.
func readPolicies(root string, tracked []string) ([]policy, []map[string]any, error) {
	var pols []policy
	var deploys []map[string]any
	for _, rel := range tracked {
		if ext := filepath.Ext(rel); ext != ".yaml" && ext != ".yml" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, nil, fmt.Errorf("%s would not read: %w", rel, err)
		}
		if !bytes.Contains(raw, []byte("NetworkPolicy")) && !bytes.Contains(raw, []byte("kind: Deployment")) {
			continue
		}
		dec := yaml.NewDecoder(bytes.NewReader(raw))
		for {
			var doc map[string]any
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				if bytes.Contains(raw, []byte("NetworkPolicy")) {
					return nil, nil, fmt.Errorf("%s names a network policy and does not parse: %w", rel, err)
				}
				break
			}
			kind, _ := doc["kind"].(string)
			md, _ := doc["metadata"].(map[string]any)
			name, _ := md["name"].(string)
			ns, _ := md["namespace"].(string)
			switch kind {
			case "Deployment":
				deploys = append(deploys, doc)
			case "CiliumNetworkPolicy", "CiliumClusterwideNetworkPolicy", "NetworkPolicy":
				p := policy{file: rel, kind: kind, name: name, namespace: ns}
				if s, ok := doc["spec"].(map[string]any); ok {
					p.specs = append(p.specs, s)
				}
				p.specs = append(p.specs, maps(doc["specs"])...)
				pols = append(pols, p)
			}
		}
	}
	return pols, deploys, nil
}

// podLabels is the label set Cilium sees on the face's pod: the namespace and
// ServiceAccount identity labels, and the template labels of the Deployments
// that run as the ServiceAccount there.
func podLabels(f face, deploys []map[string]any) map[string]string {
	l := map[string]string{
		"io.kubernetes.pod.namespace":                                f.namespace,
		"io.cilium.k8s.policy.serviceaccount":                        f.sa,
		"io.cilium.k8s.namespace.labels.kubernetes.io/metadata.name": f.namespace,
	}
	for _, d := range deploys {
		md, _ := d["metadata"].(map[string]any)
		if ns, _ := md["namespace"].(string); ns != f.namespace {
			continue
		}
		tpl := dig(d, "spec", "template")
		if sa, _ := dig(tpl, "spec")["serviceAccountName"].(string); sa != f.sa {
			continue
		}
		for k, v := range dig(tpl, "metadata", "labels") {
			l[k] = fmt.Sprint(v)
		}
	}
	return l
}

// selects is whether a spec of p applies to the face's pod. A CNP and a
// NetworkPolicy select only in their own namespace.
func (p policy) selects(f face, s map[string]any, labels map[string]string) bool {
	switch p.kind {
	case "CiliumNetworkPolicy":
		if p.namespace != f.namespace {
			return false
		}
		sel, ok := s["endpointSelector"].(map[string]any)
		return ok && matches(sel, labels)
	case "NetworkPolicy":
		if p.namespace != f.namespace {
			return false
		}
		sel, ok := s["podSelector"].(map[string]any)
		return ok && matches(sel, labels)
	default:
		sel, ok := s["endpointSelector"].(map[string]any)
		return ok && matches(sel, labels)
	}
}

// matches evaluates a label selector against a label set. Cilium's source
// prefixes (k8s:, any:) are dropped; a reserved: key never names a pod.
func matches(sel map[string]any, labels map[string]string) bool {
	key := func(k string) (string, bool) {
		if strings.HasPrefix(k, "reserved:") {
			return "", false
		}
		k = strings.TrimPrefix(k, "k8s:")
		return strings.TrimPrefix(k, "any:"), true
	}
	ml, _ := sel["matchLabels"].(map[string]any)
	for k, v := range ml {
		k, ok := key(k)
		if !ok || labels[k] != fmt.Sprint(v) {
			return false
		}
	}
	for _, e := range maps(sel["matchExpressions"]) {
		ks, _ := e["key"].(string)
		k, ok := key(ks)
		if !ok {
			return false
		}
		op, _ := e["operator"].(string)
		var vals []string
		for _, v := range list(e["values"]) {
			vals = append(vals, fmt.Sprint(v))
		}
		v, has := labels[k]
		switch op {
		case "In":
			if !has || !slices.Contains(vals, v) {
				return false
			}
		case "NotIn":
			if has && slices.Contains(vals, v) {
				return false
			}
		case "Exists":
			if !has {
				return false
			}
		case "DoesNotExist":
			if has {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// admitsL4 says why an ingress allow reaches the face port without http rules,
// or "" when it does not (another port, http rules on it, a covering deny).
func (p policy) admitsL4(f face, r map[string]any, denies []map[string]any) string {
	why := ""
	if p.kind == "NetworkPolicy" {
		if ports := maps(r["ports"]); len(ports) == 0 {
			why = "every port"
		} else if f.hit(ports) {
			why = "the port, and a NetworkPolicy has no L7"
		}
	} else {
		tps := maps(r["toPorts"])
		if len(tps) == 0 {
			if _, icmp := r["icmps"]; icmp {
				return "" // ICMP only: no TCP is admitted
			}
			why = "every port"
		}
		for _, tp := range tps {
			if len(list(dig(tp, "rules")["http"])) > 0 {
				continue
			}
			if ports := maps(tp["ports"]); len(ports) == 0 || anyPort(ports) {
				why = "every port"
			} else if f.hit(ports) {
				why = "the port with no http rules"
			}
		}
	}
	if why == "" || covered(f, r, denies) {
		return ""
	}
	return why
}

// anyPort is whether a port list holds port 0 (or none named): every port.
func anyPort(ports []map[string]any) bool {
	return slices.ContainsFunc(ports, func(pt map[string]any) bool {
		pv := fmt.Sprint(orDefault(pt["port"], "0"))
		return pv == "" || pv == "0"
	})
}

// hit is whether a port list reaches the face port over TCP: by number, by a
// range, by name, or as port 0 (every port).
func (f face) hit(ports []map[string]any) bool {
	for _, pt := range ports {
		proto := strings.ToUpper(fmt.Sprint(orDefault(pt["protocol"], "ANY")))
		if proto != "TCP" && proto != "ANY" {
			continue
		}
		pv := fmt.Sprint(orDefault(pt["port"], "0"))
		if pv == "" || pv == "0" || pv == f.portName {
			return true
		}
		lo, err := strconv.Atoi(pv)
		if err != nil {
			continue
		}
		hi := lo
		if e, err := strconv.Atoi(fmt.Sprint(orDefault(pt["endPort"], ""))); err == nil && e > lo {
			hi = e
		}
		if lo <= f.port && f.port <= hi {
			return true
		}
	}
	return false
}

// covered is whether an ingressDeny on the face's pod covers this allow's
// peers on the face port. Narrow by design: see ERASURE.
func covered(f face, r map[string]any, denies []map[string]any) bool {
	allowed := strs(r["fromEntities"])
	onlyEntities := len(allowed) > 0
	for _, k := range []string{"fromEndpoints", "fromCIDR", "fromCIDRSet", "fromNodes", "fromRequires", "fromGroups", "from"} {
		if _, ok := r[k]; ok {
			onlyEntities = false
		}
	}
	denied := map[string]bool{}
	for _, d := range denies {
		tps := maps(d["toPorts"])
		reaches := len(tps) == 0
		for _, tp := range tps {
			if ports := maps(tp["ports"]); len(ports) == 0 || f.hit(ports) {
				reaches = true
			}
		}
		if !reaches {
			continue
		}
		for _, e := range strs(d["fromEntities"]) {
			denied[e] = true
		}
	}
	if denied["all"] {
		return true
	}
	if !onlyEntities {
		return false
	}
	for _, e := range allowed {
		if !denied[e] {
			return false
		}
	}
	return true
}

// peer names an allow's peers for a finding.
func peer(r map[string]any) string {
	for _, k := range []string{"fromEntities", "fromEndpoints", "fromCIDR", "fromCIDRSet", "fromNodes", "from"} {
		if v, ok := r[k]; ok {
			b, _ := yaml.Marshal(map[string]any{k: v})
			return strings.Join(strings.Fields(string(b)), " ")
		}
	}
	return "every peer"
}

// netpolIngress is a NetworkPolicy's ingress rules, when it governs ingress.
func netpolIngress(s map[string]any) []map[string]any {
	types := strs(s["policyTypes"])
	if len(types) > 0 && !slices.Contains(types, "Ingress") {
		return nil
	}
	return maps(s["ingress"])
}

func dig(m map[string]any, path ...string) map[string]any {
	for _, k := range path {
		m, _ = m[k].(map[string]any)
	}
	return m
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func maps(v any) []map[string]any {
	var out []map[string]any
	for _, e := range list(v) {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func strs(v any) []string {
	var out []string
	for _, e := range list(v) {
		out = append(out, fmt.Sprint(e))
	}
	return out
}

func orDefault(v any, d string) any {
	if v == nil {
		return d
	}
	return v
}
