package opsflux

import (
	"context"
	"os"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// run is the atom's check over an input, as the runner calls it.
func run(in atoms.Input) checks.Verdict { return check(context.Background(), in) }

func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	atomtest.DeclaresItsRow(t, Def, "precommit", "none")
	if Def.ID != id {
		t.Errorf("%+v", Def)
	}
}

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
	// crd is the catalogue pass's answer, crdProbe the probe's exit.
	crd      string
	crdRc    int
	crdProbe int
	rendered []string
}

func (s *fluxTools) answer(t *testing.T) func(atoms.Cmd) (string, int) {
	return func(c atoms.Cmd) (string, int) {
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
		if strings.Contains(atomtest.Flagged(c), checks.CRDSchemaLocation) {
			if !strings.Contains(atomtest.Flagged(c), "-ignore-missing-schemas") {
				return "probe", s.crdProbe
			}
			if s.crd == "" {
				return `{"resources":[]}`, s.crdRc
			}
			return s.crd, s.crdRc
		}
		return s.conform, s.conformRc
	}
}

// crdStream is a built tree that carries one CRD.
const crdStream = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.w.io
spec:
  group: w.io
  names:
    kind: Widget
  versions:
  - name: v1
    schema:
      openAPIV3Schema:
        type: object
`

// crdJSON is kubeconform's -output json report over the given resources.
func crdJSON(resources ...string) string {
	return `{"resources":[` + strings.Join(resources, ",") + `]}`
}

func valid(n int) string {
	r := make([]string, n)
	for i := range r {
		r[i] = `{"kind":"Widget","name":"w","version":"w.io/v1","status":"statusValid"}`
	}
	return strings.Join(r, ",")
}

func skipped(kind, version string) string {
	return `{"kind":"` + kind + `","name":"x","version":"` + version + `","status":"statusSkipped"}`
}

func TestOpsFlux(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   map[string]string
		tools   fluxTools
		state   int
		result  string
		needles []string
	}{
		{"every tree builds and validates", fluxTree(), fluxTools{conform: "Summary: 1 resource found"}, 0, atomtest.Pass,
			[]string{"kustomize build flux/apps", "1 object(s) built from 1 tree(s)", "Summary: 1 resource found", "0 tree(s) applied with no targetNamespace checked"}},
		{"with no CR, every flux/<dir> with a kustomization.yaml is built", map[string]string{"flux/apps/kustomization.yaml": "x", "flux/infra/kustomization.yaml": "x"},
			fluxTools{}, 0, atomtest.Pass, []string{"kustomize build flux/apps", "kustomize build flux/infra", "2 object(s) built from 2 tree(s)"}},
		{"a tree that will not build is a finding, named", fluxTree(), fluxTools{build: func(string) (string, int) { return "accumulating resources", 1 }}, 1, atomtest.Findings,
			[]string{"accumulating resources  build FAILED: flux/apps", "flux failed (rc=1) — findings"}},
		{"kubeconform's finding is the exit", fluxTree(), fluxTools{conform: "Summary: 1 invalid", conformRc: 1}, 1, atomtest.Findings, []string{"Summary: 1 invalid"}},
		{"a schema host that did not answer is a 2", fluxTree(), fluxTools{conform: "dial tcp: i/o timeout", conformRc: 1}, 2, atomtest.Cannot, []string{"failed on a fault of the substrate"}},
		{"a CR file that does not parse is named", map[string]string{"flux/clusters/prod/apps.yaml": fluxCR, "flux/clusters/prod/bad.yaml": "a: [", "flux/apps/kustomization.yaml": "x"},
			fluxTools{}, 0, atomtest.Pass, []string{"flux/clusters/prod/bad.yaml:"}},
		{"an object that names no namespace under a CR with no targetNamespace", map[string]string{
			"flux/clusters/prod/apps.yaml": strings.Replace(fluxCR, "  targetNamespace: apps\n", "", 1), "flux/apps/kustomization.yaml": "x"},
			fluxTools{build: func(string) (string, int) { return "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n", 0 }}, 1, atomtest.Findings,
			[]string{"names no namespace"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := atomtest.ToolTree(t, tc.files, nil)
			f.Answer = tc.tools.answer(t)
			atomtest.Expect(t, run(in), atomtest.StateOf(tc.state), tc.result, tc.needles...)
			if f.Calls[0].Name != "kubectl" || f.Calls[0].Args[0] != "version" || atomtest.Flagged(f.Calls[0]) != "version --client=true" || atomtest.Flagged(f.Calls[1]) != "-v" {
				t.Errorf("the tools were not probed first: %v", f.Ran())
			}
			for _, c := range f.Calls[2:] {
				// The CRD pass reads stdout alone: stderr would corrupt its JSON.
				json := strings.Contains(atomtest.Flagged(c), "-output json")
				if c.Dir != in.Root || c.Both == json {
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
		in, f := atomtest.ToolTree(t, map[string]string{"flux/apps/kustomization.yaml": "x", "flux/infra/kustomization.yaml": "x"}, nil)
		tools := &fluxTools{}
		f.Answer = tools.answer(t)
		run(in)
		last := f.Calls[len(f.Calls)-3]
		want := "-strict -summary -ignore-missing-schemas -skip CustomResourceDefinition -schema-location default " + strings.Join(tools.rendered, " ")
		if last.Name != "kubeconform" || atomtest.Flagged(last) != want || len(tools.rendered) != 2 {
			t.Errorf("kubeconform ran as %q, want %q", atomtest.Flagged(last), want)
		}
		if got := atomtest.Flagged(f.Calls[2]); got != "kustomize flux/apps -o "+tools.rendered[0] {
			t.Errorf("kubectl ran as %q", got)
		}
	})
	t.Run("the CRD pass reads the built trees' own CRDs ahead of the catalogue, and a clean read passes", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, fluxTree(), nil)
		tools := &fluxTools{crd: crdJSON(valid(3)), build: func(string) (string, int) { return crdStream, 0 }}
		f.Answer = tools.answer(t)
		atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass, "CRD pass (1 schema(s) generated", "3 valid, 0 invalid")
		n := len(f.Calls)
		probe, pass := atomtest.Flagged(f.Calls[n-2]), atomtest.Flagged(f.Calls[n-1])
		if !strings.Contains(probe, "-schema-location "+checks.CRDSchemaLocation) || strings.Contains(probe, "-ignore-missing-schemas") {
			t.Errorf("the probe must fail on a missing schema, ran %q", probe)
		}
		own := strings.Index(pass, checks.CRDSchemaFile)
		if own < 0 || own > strings.Index(pass, checks.CRDSchemaLocation) || !strings.Contains(pass, "-output json -verbose") || !strings.Contains(pass, "-ignore-missing-schemas") {
			t.Errorf("the CRD pass ran %q", pass)
		}
	})
	t.Run("a resource invalid against its schema fails the atom", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, fluxTree(), nil)
		bad := `{"resources":[{"filename":"a.yaml","kind":"ObjectStore","name":"x","version":"barmancloud.cnpg.io/v1","status":"statusInvalid","msg":"compression: value must be one of"}]}`
		tools := &fluxTools{crd: bad, crdRc: 1}
		f.Answer = tools.answer(t)
		atomtest.Expect(t, run(in), atomtest.StateOf(1), atomtest.Findings, "INVALID a.yaml ObjectStore x", "compression")
	})
	t.Run("a kind with no schema anywhere fails the atom unless it is named", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, fluxTree(), nil)
		tools := &fluxTools{crd: crdJSON(skipped("Mystery", "x.io/v1"))}
		f.Answer = tools.answer(t)
		atomtest.Expect(t, run(in), atomtest.StateOf(1), atomtest.Findings, "NO SCHEMA x.io/v1/Mystery x1")
	})
	t.Run("a catalogue the lane cannot reach is a 2, never a warning", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, fluxTree(), nil)
		tools := &fluxTools{crdProbe: 1}
		f.Answer = tools.answer(t)
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "CRD pass UNANALYZABLE", "could not be read")
		for _, c := range f.Calls {
			if strings.Contains(atomtest.Flagged(c), "-ignore-missing-schemas") && strings.Contains(atomtest.Flagged(c), checks.CRDSchemaLocation) {
				t.Errorf("the catalogue was scanned after its probe failed")
			}
		}
	})
	t.Run("a schema kubeconform could not read is a 2", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, fluxTree(), nil)
		tools := &fluxTools{crd: `{"resources":[{"kind":"X","status":"statusError","msg":"dial tcp: i/o timeout"}]}`, crdRc: 1}
		f.Answer = tools.answer(t)
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "UNREADABLE", "UNANALYZABLE")
	})
	t.Run("schemas that cannot be laid out are a 2", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, fluxTree(), nil)
		// w.io/widget_v1.json is a file for one CRD and a directory for the other.
		clash := strings.Replace(crdStream, "group: w.io", "group: w.io/widget_v1.json", 1)
		tools := &fluxTools{build: func(string) (string, int) { return crdStream + "---\n" + clash, 0 }}
		f.Answer = tools.answer(t)
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "CRD pass UNANALYZABLE")
	})
	t.Run("a CRD pass that never ran is a 2", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, fluxTree(), nil)
		tools := &fluxTools{crd: "kubeconform: signal: killed", crdRc: -1}
		f.Answer = tools.answer(t)
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "CRD pass UNANALYZABLE", "signal: killed")
	})
	t.Run("a built tree whose CRDs do not parse is a 2", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, fluxTree(), nil)
		tools := &fluxTools{build: func(string) (string, int) { return "kind: [\n", 0 }}
		f.Answer = tools.answer(t)
		atomtest.Expect(t, run(in), atomtest.StateOf(1), atomtest.Findings, "a built tree did not parse")
	})
	t.Run("a CRD finding beside a namespace finding keeps both", func(t *testing.T) {
		files := map[string]string{
			"flux/clusters/prod/apps.yaml": strings.Replace(fluxCR, "  targetNamespace: apps\n", "", 1), "flux/apps/kustomization.yaml": "x"}
		in, f := atomtest.ToolTree(t, files, nil)
		tools := &fluxTools{crd: crdJSON(skipped("Mystery", "x.io/v1")), build: func(string) (string, int) { return "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n", 0 }}
		f.Answer = tools.answer(t)
		atomtest.Expect(t, run(in), atomtest.StateOf(1), atomtest.Findings, "NO SCHEMA", "names no namespace")
	})
	t.Run("the probe hands kubeconform the HelmRelease on stdin", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, fluxTree(), nil)
		tools := &fluxTools{}
		f.Answer = tools.answer(t)
		run(in)
		probe := f.Calls[len(f.Calls)-2]
		if !strings.Contains(probe.Stdin, "kind: HelmRelease") {
			t.Errorf("the probe read %q on stdin", probe.Stdin)
		}
	})
	t.Run("the catalogue pass is not asked when the default pass already found something", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, fluxTree(), nil)
		tools := &fluxTools{conform: "Summary: 1 invalid", conformRc: 1}
		f.Answer = tools.answer(t)
		run(in)
		for _, c := range f.Calls {
			if strings.Contains(atomtest.Flagged(c), checks.CRDSchemaLocation) {
				t.Errorf("catalogue consulted after a finding: %v", f.Ran())
			}
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
			in, f := atomtest.ToolTree(t, tc.files, nil)
			atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Absent, tc.why)
			if len(f.Calls) != 0 {
				t.Errorf("an absent atom touched %v", f.Ran())
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
			in, f := atomtest.ToolTree(t, fluxTree(), nil)
			f.Answer = tc.tools.answer(t)
			atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the phase's tool could not be provisioned", tc.needles)
			for _, c := range f.Calls {
				if c.Args[0] == "kustomize" {
					t.Errorf("a tree was built with an unprovisioned tool")
				}
			}
		})
	}
	t.Run("a kubectl that would not start never ran", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, fluxTree(), nil)
		tools := &fluxTools{build: func(string) (string, int) { return "kubectl: gone", -1 }}
		f.Answer = tools.answer(t)
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the atom never ran: kubectl: gone")
	})
	t.Run("a kubeconform that would not start never ran", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, fluxTree(), nil)
		tools := &fluxTools{conform: "kubeconform: gone", conformRc: -1}
		f.Answer = tools.answer(t)
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the atom never ran: kubeconform: gone")
	})
	t.Run("a build that wrote nothing it can be read from never ran", func(t *testing.T) {
		in, _ := atomtest.ToolTree(t, fluxTree(), nil)
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the atom never ran")
	})
	t.Run("a cluster manifest that will not read never ran, and says which", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, fluxTree(), nil)
		in.Committable = append(in.Committable, "flux/clusters/prod/ghost.yaml")
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the atom never ran", "ghost.yaml")
		if len(f.Calls) != 0 {
			t.Errorf("a tool ran over a tree it could not read: %v", f.Ran())
		}
	})
	t.Run("a built tree that does not parse is a finding, named", func(t *testing.T) {
		in, f := atomtest.ToolTree(t, fluxTree(), nil)
		tools := &fluxTools{build: func(string) (string, int) { return "kind: [\n", 0 }}
		f.Answer = tools.answer(t)
		atomtest.Expect(t, run(in), atomtest.StateOf(1), atomtest.Findings, "a built tree did not parse: flux/apps")
	})
	t.Run("kubeconform's own exit code is kept when the namespaces are wrong too", func(t *testing.T) {
		files := map[string]string{
			"flux/clusters/prod/apps.yaml": strings.Replace(fluxCR, "  targetNamespace: apps\n", "", 1), "flux/apps/kustomization.yaml": "x"}
		in, f := atomtest.ToolTree(t, files, nil)
		tools := &fluxTools{conformRc: 2, conform: "Summary: bad", build: func(string) (string, int) { return "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n", 0 }}
		f.Answer = tools.answer(t)
		atomtest.Expect(t, run(in), atomtest.StateOf(1), atomtest.Findings, "names no namespace", "flux failed (rc=2)")
	})
	t.Run("a population that failed", func(t *testing.T) {
		in, _ := atomtest.ToolTree(t, fluxTree(), nil)
		in.FilesErr = atomtest.ErrBoom
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the tree would not enumerate")
	})
}
