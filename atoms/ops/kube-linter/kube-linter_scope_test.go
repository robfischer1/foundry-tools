package opskubelinter

import (
	"os"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
)

const (
	cr   = "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nspec:\n  path: ./%s\n  sourceRef: {name: flux-system}\n"
	pins = "apiVersion: kustomize.config.k8s.io/v1alpha1\nkind: Component\nimages:\n  - name: a\n    newTag: %s\n"
)

// scopedTree is a root Flux tree whose prime and aiws both take the images
// Component, and whose spire takes nothing from it.
func scopedTree() map[string]string {
	return map[string]string{
		"clusters/pantheon/kustomization.yaml": "resources: [prime.yaml, aiws.yaml, spire.yaml]\n",
		"clusters/pantheon/prime.yaml":         strings.Replace(cr, "%s", "prime", 1),
		"clusters/pantheon/aiws.yaml":          strings.Replace(cr, "%s", "aiws", 1),
		"clusters/pantheon/spire.yaml":         strings.Replace(cr, "%s", "spire", 1),
		"prime/kustomization.yaml":             "resources: [s.yaml]\ncomponents: [images]\n",
		"prime/s.yaml":                         "x",
		"prime/images/kustomization.yaml":      strings.Replace(pins, "%s", "2", 1),
		"aiws/kustomization.yaml":              "resources: [o.yaml]\ncomponents: [../prime/images]\n",
		"aiws/o.yaml":                          "x",
		"spire/kustomization.yaml":             "resources: [v.yaml]\n",
		"spire/v.yaml":                         "x",
		"docs/notes.md":                        "x",
	}
}

// gitScript answers the questions the scope asks git: the base resolves, the
// merge base is "mb", the name-status is diff, and `show mb:<path>` is base[path].
func gitScript(diff string, base map[string]string) func(atoms.Cmd) (string, int) {
	return func(c atoms.Cmd) (string, int) {
		if c.Name != "git" {
			return "", 0
		}
		a := strings.Join(c.Args, " ")
		switch {
		case strings.HasPrefix(a, "rev-parse") && strings.HasSuffix(a, "HEAD^2"):
			return "", 1 // the tree under test is no merge: a tip gate has no first parent to measure from
		case strings.HasPrefix(a, "rev-parse"):
			return "ok\n", 0
		case strings.HasPrefix(a, "merge-base"):
			return "mb\n", 0
		case strings.HasPrefix(a, "diff"):
			return diff, 0
		case strings.HasPrefix(a, "show mb:"):
			if s, ok := base[strings.TrimPrefix(a, "show mb:")]; ok {
				return s, 0
			}
			return "fatal: no such path", 128
		}
		return "", 0
	}
}

func scoped(t *testing.T, diff string, base map[string]string, baseSha string) (atoms.Input, *atomtest.ToolFake) {
	t.Helper()
	in, f := kubeLinter(t, scopedTree(), nil)
	in.Base = baseSha
	f.Answer = func(c atoms.Cmd) (string, int) {
		if c.Name == "git" {
			return gitScript(diff, base)(c)
		}
		return "", 0
	}
	return in, f
}

func lintArgs(f *atomtest.ToolFake) string { return atomtest.Flagged(f.Call("kube-linter")) }

func TestUT_KL10_APinBumpLintsOnlyItsConsumersWithTheCrossObjectChecksOff(t *testing.T) {
	in, f := scoped(t, "M\x00prime/images/kustomization.yaml\x00",
		map[string]string{"prime/images/kustomization.yaml": strings.Replace(pins, "%s", "1", 1)}, "basesha")
	atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass, "clean")
	args := lintArgs(f)
	if !strings.HasPrefix(args, "lint --config ") || !strings.HasSuffix(args, " --fail-if-no-objects-found aiws prime") {
		t.Errorf("want the pin's consumers only, behind a derived --config: %s", args)
	}
}

func TestUT_KL11_NoYamlChangedIsNothingToLint(t *testing.T) {
	in, f := scoped(t, "M\x00docs/notes.md\x00", nil, "basesha")
	atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass, "nothing to lint")
	if len(f.Calls) != 0 && len(f.Ran()) != 0 {
		for _, c := range f.Calls {
			if c.Name == "kube-linter" {
				t.Errorf("kube-linter must not run: %v", f.Ran())
			}
		}
	}
}

func TestUT_KL12_TheFullTreeIsKeptWhenTheScopeCannotBeTrusted(t *testing.T) {
	const full = "lint --fail-if-no-objects-found clusters/ aiws prime spire"
	pin := map[string]string{"prime/images/kustomization.yaml": strings.Replace(pins, "%s", "1", 1)}
	for _, tc := range []struct {
		name string
		diff string
		base string
	}{
		{"no base: a main landing or a local hook", "M\x00prime/images/kustomization.yaml\x00", ""},
		{"a manifest changed", "M\x00prime/s.yaml\x00", "b"},
		{"a manifest was deleted", "D\x00spire/v.yaml\x00", "b"},
		{"the config changed", "M\x00.kube-linter.yaml\x00", "b"},
		{"the justifications changed", "M\x00.kube-justifications.json\x00", "b"},
		{"a torn diff", "M\x00", "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := scoped(t, tc.diff, pin, tc.base)
			atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass)
			if got := lintArgs(f); got != full {
				t.Errorf("want the full tree, got %s", got)
			}
		})
	}

	// A git that will not answer is the change set not computing.
	in, f := scoped(t, "", pin, "b")
	f.Answer = func(c atoms.Cmd) (string, int) {
		if c.Name == "git" {
			return "boom", 128
		}
		return "", 0
	}
	atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass)
	if got := lintArgs(f); got != full {
		t.Errorf("an uncomputable change set fails toward full: %s", got)
	}
}

func TestUT_KL13_ANarrowRunsFindingsAreTheLintersOwnAndTheTempConfigIsGone(t *testing.T) {
	in, f := scoped(t, "M\x00prime/images/kustomization.yaml\x00",
		map[string]string{"prime/images/kustomization.yaml": strings.Replace(pins, "%s", "1", 1)}, "basesha")
	var cfg string
	f.Answer = func(c atoms.Cmd) (string, int) {
		if c.Name == "kube-linter" {
			cfg = c.Args[2]
			b, err := os.ReadFile(cfg)
			if err != nil || !strings.Contains(string(b), "non-existent-service-account") {
				t.Errorf("the derived config must exist while the linter runs: %v %s", err, b)
			}
			return "prime/s.yaml: image is latest\nError: found 1 lint errors\n", 1
		}
		return gitScript("M\x00prime/images/kustomization.yaml\x00", map[string]string{"prime/images/kustomization.yaml": strings.Replace(pins, "%s", "1", 1)})(c)
	}
	atomtest.Expect(t, run(in), atomtest.StateOf(1), atomtest.Findings, "Error: found 1 lint errors", "image is latest")
	if _, err := os.Stat(cfg); err == nil {
		t.Errorf("the derived config %s must be removed", cfg)
	}
}
