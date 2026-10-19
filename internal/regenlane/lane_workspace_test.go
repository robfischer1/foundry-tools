package regenlane

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
)

// THE BARE WORKSPACE ROOT (runbook D-B4): the monorepo's root declares ts/ and has
// no justfile of its own, so the regen check reads the member's.

func bare(t *testing.T, files map[string]string) atomkit.In {
	t.Helper()
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
	return atomkit.In{Root: root, Files: names}
}

const wsRoot = `{"private": true, "workspaces": ["ts"]}`

func TestABareRootIsCheckedThroughItsMembersJustfile(t *testing.T) {
	in := bare(t, map[string]string{
		"package.json": wsRoot, "ts/package.json": "{}",
		"ts/justfile": "regen-check:\n\tscripts/regen-check.sh circuit\n",
	})
	v := Check(t.Context(), in)
	// the member's world has no provenance: a finding named for the member, not an ABSENT
	if v.Result == "absent" || v.State != 1 || !strings.Contains(v.Reason, "[ts]") || !strings.Contains(v.Reason, "no src/circuitcore-gen/provenance.json") {
		t.Fatalf("%+v", v)
	}
}

func TestABareRootWhoseMembersDeclareNoWorldIsAbsent(t *testing.T) {
	in := bare(t, map[string]string{"package.json": wsRoot, "ts/package.json": "{}", "ts/justfile": "default:\n\ttrue\n"})
	if v := Check(t.Context(), in); v.Result != "absent" || !strings.Contains(v.Reason, "ts:regen: ABSENT - no workspace member's justfile") {
		t.Errorf("%+v", v)
	}
}

func TestABareRootWithNoMemberIsACouldNotRun(t *testing.T) {
	in := bare(t, map[string]string{"package.json": `{"private": true, "workspaces": ["apps/*"]}`})
	if v := Check(t.Context(), in); v.State != 2 || v.Result == "absent" {
		t.Errorf("%+v", v)
	}
}

// A root that is not bare is read as it always was: the ROOT justfile.
func TestARootWithItsOwnScriptsIsReadAtTheRoot(t *testing.T) {
	in := bare(t, map[string]string{
		"package.json": `{"name": "x", "scripts": {"gate": "x"}}`,
		"justfile":     "regen-check:\n\tscripts/regen-check.sh circuit\n",
	})
	v := Check(t.Context(), in)
	if v.State != 1 || strings.Contains(v.Reason, "[ts]") || !strings.Contains(v.Reason, "no src/circuitcore-gen/provenance.json") {
		t.Errorf("%+v", v)
	}
}
