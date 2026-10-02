package checks

import (
	"errors"
	"strings"
	"testing"
)

// Each rule fires on an edit to its field, holds on an edit elsewhere in the
// same object, and is named kind/namespace/name: field.
func TestImmutableEdits(t *testing.T) {
	job := func(cpu, other string) string {
		return "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: provision-3\n  namespace: foundry\n  labels: {x: " + other + "}\n" +
			"spec:\n  template:\n    spec:\n      containers:\n      - name: c\n        resources: {limits: {cpu: " + cpu + "}}\n"
	}
	cases := map[string]struct {
		base, head string
		want       []string
	}{
		"job template edited (flux #206)": {job("200m", "a"), job("1", "a"), []string{"Job/foundry/provision-3: spec.template"}},
		"job metadata only":               {job("200m", "a"), job("200m", "b"), nil},
		"job force-recreated":             {job("200m", "a"), strings.Replace(job("1", "a"), "  labels:", "  annotations: {kustomize.toolkit.fluxcd.io/force: enabled}\n  labels:", 1), nil},
		"job force annotation not enabled": {job("200m", "a"), strings.Replace(job("1", "a"), "  labels:", "  annotations: {kustomize.toolkit.fluxcd.io/force: disabled}\n  labels:", 1),
			[]string{"Job/foundry/provision-3: spec.template"}},
		"job added":   {"", job("1", "a"), nil},
		"job removed": {job("1", "a"), "", nil},
		"deployment selector": {
			"kind: Deployment\nmetadata: {name: d}\nspec: {selector: {matchLabels: {app: a}}, replicas: 1}\n",
			"kind: Deployment\nmetadata: {name: d}\nspec: {selector: {matchLabels: {app: b}}, replicas: 1}\n",
			[]string{"Deployment//d: spec.selector"}},
		"deployment template is mutable": {
			"kind: Deployment\nmetadata: {name: d}\nspec: {selector: {matchLabels: {app: a}}, template: {x: 1}}\n",
			"kind: Deployment\nmetadata: {name: d}\nspec: {selector: {matchLabels: {app: a}}, template: {x: 2}}\n", nil},
		"daemonset selector":  {"kind: DaemonSet\nmetadata: {name: d}\nspec: {selector: 1}\n", "kind: DaemonSet\nmetadata: {name: d}\nspec: {selector: 2}\n", []string{"DaemonSet//d: spec.selector"}},
		"replicaset selector": {"kind: ReplicaSet\nmetadata: {name: d}\nspec: {selector: 1}\n", "kind: ReplicaSet\nmetadata: {name: d}\nspec: {selector: 2}\n", []string{"ReplicaSet//d: spec.selector"}},
		"statefulset four fields": {
			"kind: StatefulSet\nmetadata: {name: s}\nspec: {selector: 1, volumeClaimTemplates: [a], serviceName: x, podManagementPolicy: OrderedReady, replicas: 1}\n",
			"kind: StatefulSet\nmetadata: {name: s}\nspec: {selector: 2, volumeClaimTemplates: [b], serviceName: y, podManagementPolicy: Parallel, replicas: 2}\n",
			[]string{"StatefulSet//s: spec.selector", "StatefulSet//s: spec.volumeClaimTemplates", "StatefulSet//s: spec.serviceName", "StatefulSet//s: spec.podManagementPolicy"}},
		"rolebinding roleRef": {"kind: RoleBinding\nmetadata: {name: r, namespace: n}\nroleRef: {name: a}\nsubjects: [x]\n", "kind: RoleBinding\nmetadata: {name: r, namespace: n}\nroleRef: {name: b}\nsubjects: [y]\n",
			[]string{"RoleBinding/n/r: roleRef"}},
		"clusterrolebinding roleRef": {"kind: ClusterRoleBinding\nmetadata: {name: r}\nroleRef: {name: a}\n", "kind: ClusterRoleBinding\nmetadata: {name: r}\nroleRef: {name: b}\n", []string{"ClusterRoleBinding//r: roleRef"}},
		"storageclass": {"kind: StorageClass\nmetadata: {name: g}\nprovisioner: a\nparameters: {x: 1}\nreclaimPolicy: Retain\nvolumeBindingMode: WaitForFirstConsumer\nallowVolumeExpansion: false\n",
			"kind: StorageClass\nmetadata: {name: g}\nprovisioner: b\nparameters: {x: 2}\nreclaimPolicy: Delete\nvolumeBindingMode: Immediate\nallowVolumeExpansion: true\n",
			[]string{"StorageClass//g: provisioner", "StorageClass//g: parameters", "StorageClass//g: reclaimPolicy", "StorageClass//g: volumeBindingMode"}},
		"pvc growth is allowed": {"kind: PersistentVolumeClaim\nmetadata: {name: p}\nspec: {resources: {requests: {storage: 1Gi}}, storageClassName: gaia}\n",
			"kind: PersistentVolumeClaim\nmetadata: {name: p}\nspec: {resources: {requests: {storage: 2Gi}}, storageClassName: gaia}\n", nil},
		"pvc class is not": {"kind: PersistentVolumeClaim\nmetadata: {name: p}\nspec: {storageClassName: gaia, accessModes: [RWO], selector: 1, volumeMode: Filesystem, volumeName: v, dataSource: 1, dataSourceRef: 1}\n",
			"kind: PersistentVolumeClaim\nmetadata: {name: p}\nspec: {storageClassName: local, accessModes: [RWX], selector: 2, volumeMode: Block, volumeName: w, dataSource: 2, dataSourceRef: 2}\n",
			[]string{"PersistentVolumeClaim//p: spec.accessModes", "PersistentVolumeClaim//p: spec.selector", "PersistentVolumeClaim//p: spec.storageClassName",
				"PersistentVolumeClaim//p: spec.volumeMode", "PersistentVolumeClaim//p: spec.volumeName", "PersistentVolumeClaim//p: spec.dataSource", "PersistentVolumeClaim//p: spec.dataSourceRef"}},
		"service pinned clusterIP":   {"kind: Service\nmetadata: {name: s}\nspec: {clusterIP: 10.43.0.5}\n", "kind: Service\nmetadata: {name: s}\nspec: {clusterIP: 10.43.0.6}\n", []string{"Service//s: spec.clusterIP"}},
		"service unpinned":           {"kind: Service\nmetadata: {name: s}\nspec: {ports: [1]}\n", "kind: Service\nmetadata: {name: s}\nspec: {clusterIP: 10.43.0.6}\n", nil},
		"service headless":           {"kind: Service\nmetadata: {name: s}\nspec: {clusterIP: None}\n", "kind: Service\nmetadata: {name: s}\nspec: {clusterIP: 10.43.0.6}\n", nil},
		"service ports are mutable":  {"kind: Service\nmetadata: {name: s}\nspec: {clusterIP: 10.43.0.5, ports: [1]}\n", "kind: Service\nmetadata: {name: s}\nspec: {clusterIP: 10.43.0.5, ports: [2]}\n", nil},
		"immutable configmap":        {"kind: ConfigMap\nmetadata: {name: c}\nimmutable: true\ndata: {a: '1'}\nbinaryData: {b: x}\n", "kind: ConfigMap\nmetadata: {name: c}\nimmutable: true\ndata: {a: '2'}\nbinaryData: {b: y}\n", []string{"ConfigMap//c: data", "ConfigMap//c: binaryData"}},
		"immutable secret":           {"kind: Secret\nmetadata: {name: c}\nimmutable: true\nstringData: {a: '1'}\n", "kind: Secret\nmetadata: {name: c}\nimmutable: true\nstringData: {a: '2'}\n", []string{"Secret//c: stringData"}},
		"mutable configmap":          {"kind: ConfigMap\nmetadata: {name: c}\ndata: {a: '1'}\n", "kind: ConfigMap\nmetadata: {name: c}\ndata: {a: '2'}\n", nil},
		"same name, other namespace": {"kind: RoleBinding\nmetadata: {name: r, namespace: a}\nroleRef: {name: a}\n", "kind: RoleBinding\nmetadata: {name: r, namespace: b}\nroleRef: {name: b}\n", nil},
		"unnamed and kindless docs are skipped": {"kind: Job\nmetadata: {}\nspec: {template: 1}\n---\nmetadata: {name: j}\nspec: {template: 1}\n",
			"kind: Job\nmetadata: {}\nspec: {template: 2}\n---\nmetadata: {name: j}\nspec: {template: 2}\n", nil},
	}
	for name, c := range cases {
		got, err := ImmutableEdits(c.base, c.head)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s:\n got  %q\n want %q", name, got, c.want)
		}
	}
}

// A stream that does not parse is an error naming which side, never an empty
// (clean) answer.
func TestImmutableEditsRefusesAnUnparseableStream(t *testing.T) {
	if _, err := ImmutableEdits(": [bad\n", "kind: Job\n"); err == nil || !strings.HasPrefix(err.Error(), "base: ") {
		t.Errorf("base parse error = %v", err)
	}
	if _, err := ImmutableEdits("kind: Job\n", ": [bad\n"); err == nil || !strings.HasPrefix(err.Error(), "head: ") {
		t.Errorf("head parse error = %v", err)
	}
}

// Objects are compared in key order, so one skipped object (added, or
// force-recreated) never hides an edit after it.
func TestImmutableEditsSeesPastASkippedObject(t *testing.T) {
	base := "kind: Job\nmetadata: {name: z}\nspec: {template: 1}\n"
	head := "kind: Job\nmetadata: {name: a}\nspec: {template: 1}\n---\n" +
		"kind: Job\nmetadata: {name: b, annotations: {kustomize.toolkit.fluxcd.io/force: enabled}}\nspec: {template: 2}\n---\n" +
		"kind: Job\nmetadata: {name: z}\nspec: {template: 2}\n"
	base = "kind: Job\nmetadata: {name: b}\nspec: {template: 1}\n---\n" + base
	got, err := ImmutableEdits(base, head)
	if err != nil || strings.Join(got, "|") != "Job//z: spec.template" {
		t.Errorf("got %q, %v", got, err)
	}
	// A kindless document with a name keys under "" and matches no rule.
	got, _ = ImmutableEdits("metadata: {name: k}\nspec: {template: 1}\n", "metadata: {name: k}\nspec: {template: 2}\n")
	if len(got) != 0 {
		t.Errorf("kindless: %q", got)
	}
}

// The parse error wraps yaml's own, so a caller can inspect it.
func TestImmutableEditsWrapsTheParseError(t *testing.T) {
	for _, c := range [][2]string{{": [bad\n", "kind: Job\n"}, {"kind: Job\n", ": [bad\n"}} {
		_, err := ImmutableEdits(c[0], c[1])
		if err == nil || errors.Unwrap(err) == nil {
			t.Errorf("%q / %q: %v does not wrap", c[0], c[1], err)
		}
	}
}
