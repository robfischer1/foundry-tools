package checks

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const imagesComponent = "apiVersion: kustomize.config.k8s.io/v1alpha1\nkind: Component\nimages:\n  - name: a\n    newTag: %s\n"

func comp(tag string) string { return strings.Replace(imagesComponent, "%s", tag, 1) }

// reader answers files from a map, and an error for the rest.
func reader(m map[string]string) func(string) (string, error) {
	return func(p string) (string, error) {
		if s, ok := m[p]; ok {
			return s, nil
		}
		return "", errors.New("no " + p)
	}
}

// fluxFiles is the root Flux shape: prime takes the images Component, and so do
// aiws and blades (the consumers sit outside the changed file's own path).
var fluxFiles = []string{
	"clusters/pantheon/kustomization.yaml", "clusters/pantheon/prime.yaml",
	"prime/kustomization.yaml", "prime/star-a.yaml", "prime/images/kustomization.yaml",
	"aiws/kustomization.yaml", "aiws/op.yaml",
	"blades/kustomization.yaml", "blades/ctl.yaml",
	"spire/kustomization.yaml", "spire/server.yaml",
	"docs/README.md", "docs/notes.yaml",
}

var fluxHead = map[string]string{
	"prime/kustomization.yaml":        "resources:\n  - star-a.yaml\ncomponents:\n  - images\n",
	"aiws/kustomization.yaml":         "resources:\n  - op.yaml\ncomponents:\n  - ../prime/images\n",
	"blades/kustomization.yaml":       "resources:\n  - ctl.yaml\ncomponents:\n  - ../prime/images\n",
	"spire/kustomization.yaml":        "resources:\n  - server.yaml\n",
	"prime/images/kustomization.yaml": comp("2"),
}

var fluxTargets = []string{"clusters/", "prime", "aiws", "blades", "spire"}

func scope(changes []KubeChange, head, base map[string]string) KubeScope {
	return ScopeKubeLinter(KubeScopeIn{
		Targets: fluxTargets, Files: fluxFiles, Changes: changes,
		Head: reader(head), Base: reader(base),
	})
}

func TestUT_KL1_APinBumpLintsOnlyTheTargetsThatTakeTheComponent(t *testing.T) {
	got := scope([]KubeChange{{"M", "prime/images/kustomization.yaml"}}, fluxHead,
		map[string]string{"prime/images/kustomization.yaml": comp("1")})
	if got.Full || !slices.Equal(got.Targets, []string{"prime", "aiws", "blades"}) {
		t.Errorf("want prime aiws blades (every consumer, not the path owner alone), got %+v", got)
	}
}

func TestUT_KL2_ChangesOutsideEveryTargetLeaveNothingToLint(t *testing.T) {
	got := scope([]KubeChange{{"M", "docs/README.md"}, {"M", "docs/notes.yaml"}, {"D", "docs/old.yaml"}}, fluxHead, nil)
	if got.Full || len(got.Targets) != 0 {
		t.Errorf("docs/ is no lint target: %+v", got)
	}
}

func TestUT_KL3_AnythingThatCanChangeAnObjectKeepsTheFullTree(t *testing.T) {
	inert := map[string]string{"prime/images/kustomization.yaml": comp("1")}
	for _, tc := range []struct {
		name    string
		changes []KubeChange
		base    map[string]string
	}{
		{"a manifest edit", []KubeChange{{"M", "prime/star-a.yaml"}}, nil},
		{"an added manifest", []KubeChange{{"A", "spire/new.yaml"}}, nil},
		{"a deleted manifest", []KubeChange{{"D", "aiws/op.yaml"}}, nil},
		{"a kustomization that is not images-only", []KubeChange{{"M", "spire/kustomization.yaml"}}, map[string]string{"spire/kustomization.yaml": "resources: []\n"}},
		{"the kube-linter config", []KubeChange{{"M", ".kube-linter.yaml"}}, nil},
		{"the justifications record", []KubeChange{{"M", ".kube-justifications.json"}}, nil},
		{"a pin bump beside a manifest edit", []KubeChange{{"M", "prime/images/kustomization.yaml"}, {"M", "prime/star-a.yaml"}}, inert},
		{"a pin file added", []KubeChange{{"A", "prime/images/kustomization.yaml"}}, nil},
		{"a pin file whose base was not images-only", []KubeChange{{"M", "prime/images/kustomization.yaml"}}, map[string]string{"prime/images/kustomization.yaml": "resources:\n  - x.yaml\nimages: []\n"}},
		{"a pin file whose base cannot be read", []KubeChange{{"M", "prime/images/kustomization.yaml"}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := scope(tc.changes, fluxHead, tc.base)
			if !got.Full || got.Why == "" {
				t.Errorf("want the full tree and a reason, got %+v", got)
			}
		})
	}
}

func TestUT_KL4_AnUnreadableOrUnparseableKustomizationFailsTowardFull(t *testing.T) {
	changes := []KubeChange{{"M", "prime/images/kustomization.yaml"}}
	base := map[string]string{"prime/images/kustomization.yaml": comp("1")}
	head := map[string]string{"prime/images/kustomization.yaml": comp("2"), "prime/kustomization.yaml": "resources: [\n", "aiws/kustomization.yaml": ""}
	if got := scope(changes, head, base); !got.Full {
		t.Errorf("a target whose kustomization does not parse: %+v", got)
	}
	// Unreadable: no kustomization content for prime at all.
	if got := scope(changes, map[string]string{"prime/images/kustomization.yaml": comp("2")}, base); !got.Full {
		t.Errorf("a target whose kustomization cannot be read: %+v", got)
	}
}

func TestUT_KL5_APlainTargetCoversItsWholeSubtree(t *testing.T) {
	got := scope([]KubeChange{{"M", "clusters/pantheon/extra/deep.yaml"}}, fluxHead, nil)
	if !got.Full {
		t.Errorf("a manifest under clusters/ is an object change: %+v", got)
	}
}

func TestUT_KL6_ImagesOnlyIsExactlyAnImagesTransform(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want bool
	}{
		{"component", comp("1"), true},
		{"kustomization kind", "kind: Kustomization\nimages: []\n", true},
		{"with metadata", "kind: Component\nmetadata:\n  name: x\nimages: []\n", true},
		{"resources", "kind: Kustomization\nresources: [a.yaml]\nimages: []\n", false},
		{"patches", "kind: Kustomization\npatches: []\n", false},
		{"a Deployment", "kind: Deployment\nimages: []\n", false},
		{"two documents", comp("1") + "---\nkind: Component\nresources: [a.yaml]\n", false},
		{"not yaml", "a: [\n", false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ImagesOnlyKustomization(tc.src); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestUT_KL7_NameStatusParsesNulPairsAndRefusesATornRecord(t *testing.T) {
	got, ok := ParseKubeNameStatus("M\x00a b.yaml\x00D\x00x/y.yaml\x00")
	if !ok || len(got) != 2 || got[0] != (KubeChange{"M", "a b.yaml"}) || got[1] != (KubeChange{"D", "x/y.yaml"}) {
		t.Errorf("%v %v", got, ok)
	}
	if got, ok := ParseKubeNameStatus(""); !ok || len(got) != 0 {
		t.Errorf("no change is a valid empty set: %v %v", got, ok)
	}
	if _, ok := ParseKubeNameStatus("M\x00a.yaml\x00D"); ok {
		t.Error("a status with no path is a torn record")
	}
}

func TestUT_KL8_TheDerivedConfigKeepsEveryExcludeAndAddsTheCrossObjectChecks(t *testing.T) {
	src := "# keep\nchecks:\n  exclude:\n    - dangling-service\n    - sorted-keys\n  doNotAutoAddDefaults: false\ncustomChecks: []\n"
	out, err := KubeLinterConfigWith(src, KubeLinterCrossObject)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"dangling-service", "sorted-keys", "non-existent-service-account", "invalid-target-ports", "pdb-min-available", "customChecks"} {
		if !strings.Contains(out, want) {
			t.Errorf("derived config lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "- dangling-service\n") != 1 {
		t.Errorf("an exclude already present is not repeated:\n%s", out)
	}
	// No file at all, and no checks key.
	if out, err = KubeLinterConfigWith("", []string{"x"}); err != nil || !strings.Contains(out, "- x") {
		t.Errorf("%q %v", out, err)
	}
	if _, err = KubeLinterConfigWith("checks: [\n", []string{"x"}); err == nil {
		t.Error("a config that does not parse must be refused, not replaced")
	}
	if _, err = KubeLinterConfigWith("checks: 3\n", []string{"x"}); err == nil {
		t.Error("a checks key that is not a mapping must be refused")
	}
	if _, err = KubeLinterConfigWith("checks:\n  exclude: nope\n", []string{"x"}); err == nil {
		t.Error("a checks.exclude that is not a list must be refused")
	}
}

func TestUT_KL9_AReferenceReachedTwiceIsFollowedOnceAndABrokenOneFailsFull(t *testing.T) {
	changes := []KubeChange{{"M", "prime/images/kustomization.yaml"}}
	base := map[string]string{"prime/images/kustomization.yaml": comp("1")}
	// blades names the Component twice and prime once more through ../prime:
	// the second visit must not loop or double-count.
	head := map[string]string{
		"prime/images/kustomization.yaml": comp("2"),
		"prime/kustomization.yaml":        "resources:\n  - star-a.yaml\ncomponents:\n  - images\n",
		"aiws/kustomization.yaml":         "resources:\n  - op.yaml\n",
		"blades/kustomization.yaml":       "components:\n  - ../prime/images\n  - ../prime/images\nresources:\n  - ../prime\n",
		"spire/kustomization.yaml":        "resources:\n  - server.yaml\n",
	}
	got := scope(changes, head, base)
	if got.Full || !slices.Equal(got.Targets, []string{"prime", "blades"}) {
		t.Errorf("want prime and blades once each: %+v", got)
	}
	// A directory reached through a reference whose own kustomization is broken
	// cannot be read: the whole decision fails toward full.
	head["aiws/kustomization.yaml"] = "resources:\n  - ../broken\n"
	head["broken/kustomization.yaml"] = "resources: [\n"
	files := append(slices.Clone(fluxFiles), "broken/kustomization.yaml")
	got = ScopeKubeLinter(KubeScopeIn{Targets: fluxTargets, Files: files, Changes: changes, Head: reader(head), Base: reader(base)})
	if !got.Full {
		t.Errorf("a nested kustomization that does not parse: %+v", got)
	}
}
