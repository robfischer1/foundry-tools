package atoms

import (
	"os"
	"strings"
	"testing"
)

const fluxCR = `apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: apps
spec:
  path: ./flux/apps
  targetNamespace: apps
`

const builtStream = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n  namespace: apps\n"

func fluxTree() map[string]string {
	return map[string]string{"flux/clusters/prod/apps.yaml": fluxCR, "flux/apps/kustomization.yaml": "resources: []\n"}
}

// fluxTools answers kubectl and kubeconform: kustomize writes the stream to the
// file it is told to, and the two probes and kubeconform answer from the fields.
type fluxTools struct {
	build     func(tree string) (string, int)
	conform   string
	conformRc int
	probeRc   map[string]int
	rendered  []string
}

func (s *fluxTools) answer(t *testing.T) func(Cmd) (string, int) {
	return func(c Cmd) (string, int) {
		switch {
		case c.Name == "kubectl" && c.Args[0] == "version":
			return "Client Version: v1.37.1", s.probeRc["kubectl"]
		case c.Name == "kubeconform" && c.Args[0] == "-v":
			return "v0.7.0", s.probeRc["kubeconform"]
		case c.Name == "kubectl":
			out, code := builtStream, 0
			if s.build != nil {
				out, code = s.build(c.Args[1])
			}
			if code != 0 || out == "" {
				return out, code
			}
			if err := os.WriteFile(c.Args[3], []byte(out), 0o644); err != nil {
				t.Errorf("the rendered file could not be written: %v", err)
			}
			s.rendered = append(s.rendered, c.Args[3])
			return "", 0
		}
		return s.conform, s.conformRc
	}
}

func TestOpsFlux(t *testing.T) {
	const id = "ops:flux"
	for _, tc := range []struct {
		name    string
		files   map[string]string
		tools   fluxTools
		state   int
		result  string
		needles []string
	}{
		{"every tree builds and validates", fluxTree(), fluxTools{conform: "Summary: 1 resource found"}, 0, pass,
			[]string{"kustomize build flux/apps", "1 object(s) built from 1 tree(s)", "Summary: 1 resource found", "0 tree(s) applied with no targetNamespace checked"}},
		{"with no CR, every flux/<dir> with a kustomization.yaml is built", map[string]string{"flux/apps/kustomization.yaml": "x", "flux/infra/kustomization.yaml": "x"},
			fluxTools{}, 0, pass, []string{"kustomize build flux/apps", "kustomize build flux/infra", "2 object(s) built from 2 tree(s)"}},
		{"a tree that will not build is a finding, named", fluxTree(), fluxTools{build: func(string) (string, int) { return "accumulating resources", 1 }}, 1, findings,
			[]string{"accumulating resources  build FAILED: flux/apps", "flux failed (rc=1) — findings"}},
		{"kubeconform's finding is the exit", fluxTree(), fluxTools{conform: "Summary: 1 invalid", conformRc: 1}, 1, findings, []string{"Summary: 1 invalid"}},
		{"a schema host that did not answer is a 2", fluxTree(), fluxTools{conform: "dial tcp: i/o timeout", conformRc: 1}, 2, cannot, []string{"failed on a fault of the substrate"}},
		{"a CR file that does not parse is named", map[string]string{"flux/clusters/prod/apps.yaml": fluxCR, "flux/clusters/prod/bad.yaml": "a: [", "flux/apps/kustomization.yaml": "x"},
			fluxTools{}, 0, pass, []string{"flux/clusters/prod/bad.yaml:"}},
		{"an object that names no namespace under a CR with no targetNamespace", map[string]string{
			"flux/clusters/prod/apps.yaml": strings.Replace(fluxCR, "  targetNamespace: apps\n", "", 1), "flux/apps/kustomization.yaml": "x"},
			fluxTools{build: func(string) (string, int) { return "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n", 0 }}, 1, findings,
			[]string{"names no namespace"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := toolTree(t, tc.files, nil)
			f.answer = tc.tools.answer(t)
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
			if f.calls[0].Name != "kubectl" || f.calls[0].Args[0] != "version" || flagged(f.calls[0]) != "version --client=true" || flagged(f.calls[1]) != "-v" {
				t.Errorf("the tools were not probed first: %v", f.ran())
			}
			for _, c := range f.calls[2:] {
				if c.Dir != in.Root || !c.Both {
					t.Errorf("%s ran in %q (both streams %v)", c.Name, c.Dir, c.Both)
				}
			}
			for _, file := range tc.tools.rendered {
				if _, err := os.Stat(file); err == nil {
					t.Errorf("the rendered tree %s was left behind", file)
				}
			}
		})
	}
	t.Run("kubeconform reads the built files in place, one per tree", func(t *testing.T) {
		in, f := toolTree(t, map[string]string{"flux/apps/kustomization.yaml": "x", "flux/infra/kustomization.yaml": "x"}, nil)
		tools := &fluxTools{}
		f.answer = tools.answer(t)
		runAtom(t, id, in)
		last := f.calls[len(f.calls)-1]
		want := "-strict -summary -ignore-missing-schemas -skip CustomResourceDefinition -schema-location default " + strings.Join(tools.rendered, " ")
		if last.Name != "kubeconform" || flagged(last) != want || len(tools.rendered) != 2 {
			t.Errorf("kubeconform ran as %q, want %q", flagged(last), want)
		}
		if got := flagged(f.calls[2]); got != "kustomize flux/apps -o "+tools.rendered[0] {
			t.Errorf("kubectl ran as %q", got)
		}
	})
	for name, tc := range map[string]struct {
		files map[string]string
		why   string
	}{
		"not an ops tree":                   {map[string]string{"main.go": "x"}, "no ops shape"},
		"an ops tree with no Flux tree":     {map[string]string{"ansible/playbooks/a.yml": "x"}, "no Flux tree here"},
		"a Flux tree with nothing to build": {map[string]string{"flux/readme.yaml": "x"}, "carries no Kustomization CR and no kustomization.yaml"},
	} {
		t.Run(name+" is absent before kubectl is asked", func(t *testing.T) {
			in, f := toolTree(t, tc.files, nil)
			expect(t, runAtom(t, id, in), stateOf(0), absent, tc.why)
			if len(f.calls) != 0 {
				t.Errorf("an absent atom touched %v", f.ran())
			}
		})
	}
	for name, tc := range map[string]struct {
		tools   fluxTools
		needles string
	}{
		"a kubectl that fails its probe":     {fluxTools{probeRc: map[string]int{"kubectl": 127}}, "kubectl version --client=true exited 127"},
		"a kubeconform that fails its probe": {fluxTools{probeRc: map[string]int{"kubeconform": 127}}, "kubeconform -v exited 127"},
	} {
		t.Run(name, func(t *testing.T) {
			in, f := toolTree(t, fluxTree(), nil)
			f.answer = tc.tools.answer(t)
			expect(t, runAtom(t, id, in), stateOf(2), cannot, "the phase's tool could not be provisioned", tc.needles)
			for _, c := range f.calls {
				if c.Args[0] == "kustomize" {
					t.Errorf("a tree was built with an unprovisioned tool")
				}
			}
		})
	}
	t.Run("a kubectl that would not start never ran", func(t *testing.T) {
		in, f := toolTree(t, fluxTree(), nil)
		tools := &fluxTools{build: func(string) (string, int) { return "kubectl: gone", -1 }}
		f.answer = tools.answer(t)
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the atom never ran: kubectl: gone")
	})
	t.Run("a kubeconform that would not start never ran", func(t *testing.T) {
		in, f := toolTree(t, fluxTree(), nil)
		tools := &fluxTools{conform: "kubeconform: gone", conformRc: -1}
		f.answer = tools.answer(t)
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the atom never ran: kubeconform: gone")
	})
	t.Run("a build that wrote nothing it can be read from never ran", func(t *testing.T) {
		in, _ := toolTree(t, fluxTree(), nil)
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the atom never ran")
	})
	t.Run("a cluster manifest that will not read never ran, and says which", func(t *testing.T) {
		in, f := toolTree(t, fluxTree(), nil)
		in.Committable = append(in.Committable, "flux/clusters/prod/ghost.yaml")
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the atom never ran", "ghost.yaml")
		if len(f.calls) != 0 {
			t.Errorf("a tool ran over a tree it could not read: %v", f.ran())
		}
	})
	t.Run("a built tree that does not parse is a finding, named", func(t *testing.T) {
		in, f := toolTree(t, fluxTree(), nil)
		tools := &fluxTools{build: func(string) (string, int) { return "kind: [\n", 0 }}
		f.answer = tools.answer(t)
		expect(t, runAtom(t, id, in), stateOf(1), findings, "a built tree did not parse: flux/apps")
	})
	t.Run("kubeconform's own exit code is kept when the namespaces are wrong too", func(t *testing.T) {
		files := map[string]string{
			"flux/clusters/prod/apps.yaml": strings.Replace(fluxCR, "  targetNamespace: apps\n", "", 1), "flux/apps/kustomization.yaml": "x"}
		in, f := toolTree(t, files, nil)
		tools := &fluxTools{conformRc: 2, conform: "Summary: bad", build: func(string) (string, int) { return "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n", 0 }}
		f.answer = tools.answer(t)
		expect(t, runAtom(t, id, in), stateOf(1), findings, "names no namespace", "flux failed (rc=2)")
	})
	t.Run("a population that failed", func(t *testing.T) {
		in, _ := toolTree(t, fluxTree(), nil)
		in.FilesErr = errBoom
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the tree would not enumerate")
	})
}
