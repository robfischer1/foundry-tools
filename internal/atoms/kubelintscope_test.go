package atoms

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	klPins   = "apiVersion: kustomize.config.k8s.io/v1alpha1\nkind: Component\nimages:\n  - name: a\n    newTag: %s\n"
	klTarget = "x"
)

// klTree writes a root Flux-shaped tree and answers its Input: prime and aiws
// take the images Component, spire takes nothing from it.
func klTree(t *testing.T, extra map[string]string) Input {
	t.Helper()
	files := map[string]string{
		"prime/kustomization.yaml":        "resources: [s.yaml]\ncomponents: [images]\n",
		"prime/s.yaml":                    klTarget,
		"prime/images/kustomization.yaml": strings.Replace(klPins, "%s", "2", 1),
		"aiws/kustomization.yaml":         "resources: [o.yaml]\ncomponents: [../prime/images]\n",
		"aiws/o.yaml":                     klTarget,
		"spire/kustomization.yaml":        "resources: [v.yaml]\n",
		"spire/v.yaml":                    klTarget,
		"docs/notes.md":                   klTarget,
	}
	for k, v := range extra {
		files[k] = v
	}
	root := t.TempDir()
	var names []string
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return Input{Root: root, Files: names, Base: "basesha"}
}

// klGit scripts the git questions the plan asks. A zero field answers the
// healthy reply.
type klGit struct {
	mergeBase, diff, show     string
	revCode, mbCode, diffCode int
	showCode                  int
}

func (g klGit) exec(_ context.Context, c Cmd) (string, int) {
	a := strings.Join(c.Args, " ")
	switch {
	case strings.HasPrefix(a, "rev-parse"):
		return "ok\n", g.revCode
	case strings.HasPrefix(a, "merge-base"):
		if g.mergeBase == "" {
			return "mb\n", g.mbCode
		}
		return g.mergeBase, g.mbCode
	case strings.HasPrefix(a, "diff"):
		return g.diff, g.diffCode
	case strings.HasPrefix(a, "show"):
		return g.show, g.showCode
	}
	return "", 0
}

var klTargets = []string{"aiws", "prime", "spire"}

const klPinDiff = "M\x00prime/images/kustomization.yaml\x00"

func klPlan(t *testing.T, in Input, g klGit) KubeLintPlan {
	t.Helper()
	in.Exec = g.exec
	p := PlanKubeLinter(context.Background(), in, klTargets)
	if p.Config != "" {
		t.Cleanup(func() { _ = os.Remove(p.Config) })
	}
	return p
}

func klFull(t *testing.T, p KubeLintPlan, why string) {
	t.Helper()
	if p.Skip || p.Config != "" || !slices.Equal(p.Targets, klTargets) || !strings.Contains(p.Note, why) {
		t.Errorf("want the full tree noting %q, got %+v", why, p)
	}
}

func TestUT_KL20_ThePlanNarrowsAPinBumpAndWritesTheDerivedConfig(t *testing.T) {
	in := klTree(t, map[string]string{".kube-linter.yaml": "checks:\n  exclude:\n    - sorted-keys\n"})
	p := klPlan(t, in, klGit{diff: klPinDiff, show: strings.Replace(klPins, "%s", "1", 1)})
	if p.Skip || !slices.Equal(p.Targets, []string{"aiws", "prime"}) || p.Config == "" {
		t.Fatalf("want aiws and prime behind a derived config: %+v", p)
	}
	b, err := os.ReadFile(p.Config)
	if err != nil || !strings.Contains(string(b), "sorted-keys") || !strings.Contains(string(b), "non-existent-service-account") {
		t.Errorf("the derived config keeps the tree's excludes and adds the cross-object checks: %v\n%s", err, b)
	}
}

func TestUT_KL21_NoYamlReadByATargetIsNothingToLint(t *testing.T) {
	p := klPlan(t, klTree(t, nil), klGit{diff: "M\x00docs/notes.md\x00"})
	if !p.Skip || len(p.Targets) != 0 || !strings.Contains(p.Note, "nothing to lint") {
		t.Errorf("%+v", p)
	}
}

func TestUT_KL22_TheFullTreeIsKeptWheneverTheScopeIsNotEstablished(t *testing.T) {
	pin := strings.Replace(klPins, "%s", "1", 1)
	noBase := klTree(t, nil)
	noBase.Base = ""
	klFull(t, klPlan(t, noBase, klGit{}), "no base")
	klFull(t, klPlan(t, klTree(t, nil), klGit{revCode: 1}), "merge base")
	klFull(t, klPlan(t, klTree(t, nil), klGit{mbCode: 1}), "merge base")
	klFull(t, klPlan(t, klTree(t, nil), klGit{diffCode: 128, diff: "fatal"}), "did not compute")
	klFull(t, klPlan(t, klTree(t, nil), klGit{diff: "M\x00"}), "did not parse")
	klFull(t, klPlan(t, klTree(t, nil), klGit{diff: "M\x00prime/s.yaml\x00"}), "prime/s.yaml")
	// The base of the pin cannot be read: not provably inert.
	klFull(t, klPlan(t, klTree(t, nil), klGit{diff: klPinDiff, showCode: 128, show: "fatal"}), "prime/images/kustomization.yaml")
	// A kube-linter config that does not parse is not replaced by a guess.
	bad := klTree(t, map[string]string{".kube-linter.yaml": "checks: [\n"})
	klFull(t, klPlan(t, bad, klGit{diff: klPinDiff, show: pin}), "does not parse")
}

func TestUT_KL23_ATreeWithOnlyTheYmlConfigOrNoneStillDerivesOne(t *testing.T) {
	pin := strings.Replace(klPins, "%s", "1", 1)
	yml := klTree(t, map[string]string{".kube-linter.yml": "checks:\n  exclude:\n    - sorted-keys\n"})
	p := klPlan(t, yml, klGit{diff: klPinDiff, show: pin})
	if b, err := os.ReadFile(p.Config); err != nil || !strings.Contains(string(b), "sorted-keys") {
		t.Errorf(".kube-linter.yml is read when .yaml is absent: %v %s", err, b)
	}
	none := klPlan(t, klTree(t, nil), klGit{diff: klPinDiff, show: pin})
	if b, err := os.ReadFile(none.Config); err != nil || !strings.Contains(string(b), "invalid-target-ports") {
		t.Errorf("no config at all derives one from the defaults: %v %s", err, b)
	}
}

func TestUT_KL24_ADerivedConfigThatCannotBeWrittenKeepsTheFullTree(t *testing.T) {
	t.Setenv("ATOM_WORK", filepath.Join(t.TempDir(), "absent", "dir"))
	p := klPlan(t, klTree(t, nil), klGit{diff: klPinDiff, show: strings.Replace(klPins, "%s", "1", 1)})
	klFull(t, p, "could not be written")
}
