package checks

import (
	"reflect"
	"strings"
	"testing"
)

func deployment(name, annotations string) string {
	return "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: " + name + "\n" + annotations + "spec: {}\n"
}

func grantsPolicy(n string) string {
	return "apiVersion: cilium.io/v2\nkind: CiliumNetworkPolicy\nmetadata:\n  name: grants-" + n + "\n"
}

func TestStarFiles(t *testing.T) {
	files, names := StarFiles([]string{"prime/star-b.yaml", "prime/other.yaml", "prime/star-a.yml", "prime/sub/star-c.yaml", "star-d.yaml", "prime/star-daedalus-green.yaml"})
	if !reflect.DeepEqual(files, []string{"prime/star-a.yml", "prime/star-b.yaml", "prime/star-daedalus-green.yaml"}) || !reflect.DeepEqual(names, []string{"a", "b", "daedalus-green"}) {
		t.Errorf("%q %q", files, names)
	}
}

func TestStarGrants(t *testing.T) {
	optOut := "  annotations:\n    " + StarNoGrantsAnnotation + ": \"internal batch star, no callers\"\n"
	emptyOptOut := "  annotations:\n    " + StarNoGrantsAnnotation + ": \"\"\n"
	for _, tc := range []struct {
		name    string
		files   []string
		prime   map[string]string
		bad     bool
		needles []string
	}{
		{"a Deployment with no grants-<n> is a finding that names the policy", []string{"prime/star-hecate.yaml"},
			map[string]string{"prime/star-hecate.yaml": deployment("hecate", "")}, true,
			[]string{"STAR prime/star-hecate.yaml", "no CiliumNetworkPolicy grants-hecate", StarNoGrantsAnnotation}},
		{"grants-<n> in another prime/ file satisfies it", []string{"prime/star-hecate.yaml"},
			map[string]string{"prime/star-hecate.yaml": deployment("hecate", ""), "prime/first-l7-enforcement.yaml": grantsPolicy("other") + "---\n" + grantsPolicy("hecate")}, false,
			[]string{"grants-hecate is in prime/"}},
		{"the opt-out annotation with a reason satisfies it", []string{"prime/star-x.yaml"},
			map[string]string{"prime/star-x.yaml": deployment("x", optOut)}, false, []string{"opted out of grants-x", "internal batch star"}},
		{"an opt-out with no reason does not", []string{"prime/star-x.yaml"},
			map[string]string{"prime/star-x.yaml": deployment("x", emptyOptOut)}, true, []string{"STAR prime/star-x.yaml"}},
		{"a grants policy for another star does not", []string{"prime/star-x.yaml"},
			map[string]string{"prime/star-x.yaml": deployment("x", ""), "prime/g.yaml": grantsPolicy("y")}, true, []string{"grants-x"}},
		{"a star manifest with no Deployment is not graded", []string{"prime/star-x.yaml"},
			map[string]string{"prime/star-x.yaml": "kind: ConfigMap\nmetadata:\n  name: x\n"}, false, []string{"adds no Deployment; not graded", "0 with a Deployment graded"}},
		{"the first ungranted star is named though a later one is granted", []string{"prime/star-a.yaml", "prime/star-b.yaml"},
			map[string]string{"prime/star-a.yaml": deployment("a", ""), "prime/star-b.yaml": deployment("b", ""), "prime/g.yaml": grantsPolicy("b")}, true,
			[]string{"STAR prime/star-a.yaml", "grants-b is in prime/"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files, names := StarFiles(tc.files)
			report, bad, err := StarGrants(files, names, tc.prime)
			if err != nil || bad != tc.bad {
				t.Fatalf("bad=%v err=%v, want bad=%v:\n%s", bad, err, tc.bad, report)
			}
			for _, n := range tc.needles {
				if !strings.Contains(report, n) {
					t.Errorf("report lacks %q:\n%s", n, report)
				}
			}
		})
	}
	t.Run("a manifest that does not parse is an error", func(t *testing.T) {
		if _, _, err := StarGrants([]string{"prime/star-x.yaml"}, []string{"x"}, map[string]string{"prime/star-x.yaml": "kind: [\n"}); err == nil || !strings.Contains(err.Error(), "prime/star-x.yaml did not parse") {
			t.Errorf("err = %v", err)
		}
	})
}
