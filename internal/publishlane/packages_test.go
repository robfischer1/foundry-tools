package publishlane

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/buildlane"
)

func tree(t *testing.T, files map[string]string) Source {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return DiskSource(root)
}

func TestDEF_B6_publish_APlainRootIsItsOwnPackage(t *testing.T) {
	pkgs, code, why := Packages(tree(t, map[string]string{"package.json": `{"name":"@forge/x","version":"1.2.3"}`}))
	if code != buildlane.Clean || why != "" || len(pkgs) != 1 || pkgs[0].Dir != "" || pkgs[0].What() != "@forge/x@1.2.3" {
		t.Fatalf("%+v %d %q", pkgs, code, why)
	}
}

func TestDEF_B6_publish_APrivateOrUnnamedRootIsAFinding(t *testing.T) {
	_, code, why := Packages(tree(t, map[string]string{"package.json": `{"name":"x","version":"1","private":true}`}))
	if code != buildlane.Findings || why != "findings: the record says this repo ships a package, and its root package.json (x) is private" {
		t.Errorf("%d %q", code, why)
	}
	_, code, why = Packages(tree(t, map[string]string{"package.json": `{"private":true}`}))
	if code != buildlane.Findings || !strings.Contains(why, "(the root package) is private") {
		t.Errorf("%d %q", code, why)
	}
	_, code, why = Packages(tree(t, map[string]string{"package.json": `{"name":"x"}`}))
	if code != buildlane.Findings || why != "findings: the root package.json names no package or no version — there is nothing to publish" {
		t.Errorf("%d %q", code, why)
	}
}

func TestDEF_B6_publish_AWorkspacePublishesItsDeclaredMembersSorted(t *testing.T) {
	pkgs, code, why := Packages(tree(t, map[string]string{
		"package.json":                 `{"name":"root","private":true,"workspaces":["apps/*","packages/*"]}`,
		"packages/b/package.json":      `{"name":"@f/b","version":"1","publishConfig":{"registry":"https://r/"},"scripts":{"prepublishOnly":"bun run build"}}`,
		"packages/a/package.json":      `{"name":"@f/a","version":"2","publishConfig":{"registry":"https://r/"}}`,
		"packages/hidden/package.json": `{"name":"@f/h","version":"2"}`,
		"apps/web/package.json":        `{"name":"web","version":"1","private":true,"publishConfig":{"registry":"https://r/"}}`,
	}))
	if code != buildlane.Clean || why != "" || len(pkgs) != 2 || pkgs[0].Dir != "packages/a" || pkgs[1].Dir != "packages/b" || !pkgs[1].Builds() {
		t.Fatalf("%+v %d %q", pkgs, code, why)
	}
}

func TestDEF_B6_publish_AWorkspaceWithNoDeclaredMemberIsAFinding(t *testing.T) {
	_, code, why := Packages(tree(t, map[string]string{
		"package.json":            `{"name":"root","workspaces":{"packages":["packages/*"]}}`,
		"packages/a/package.json": `{"name":"a","version":"1"}`,
	}))
	if code != buildlane.Findings || !strings.HasPrefix(why, "findings: root is a workspace root, and none of its members declares where it publishes") {
		t.Errorf("%d %q", code, why)
	}
}

func TestDEF_B6_publish_AMemberWithARegistryButNoNameIsAFinding(t *testing.T) {
	_, code, why := Packages(tree(t, map[string]string{
		"package.json":     `{"name":"root","workspaces":["p/*"]}`,
		"p/a/package.json": `{"publishConfig":{"registry":"https://r/"}}`,
	}))
	if code != buildlane.Findings || why != "findings: p/a/package.json declares a registry and names no package or no version" {
		t.Errorf("%d %q", code, why)
	}
}

func TestDEF_B6_publish_ManifestsThatCannotBeReadOrParse(t *testing.T) {
	_, code, why := Packages(tree(t, map[string]string{"x": "y"}))
	if code != buildlane.CouldNotRun || !strings.HasPrefix(why, "could not run: package.json could not be read") {
		t.Errorf("%d %q", code, why)
	}
	_, code, why = Packages(tree(t, map[string]string{"package.json": `{`}))
	if code != buildlane.Findings || !strings.HasPrefix(why, "findings: package.json does not parse") {
		t.Errorf("%d %q", code, why)
	}
	_, code, why = Packages(tree(t, map[string]string{"package.json": `{"workspaces":["p/*"]}`, "p/a/package.json": `[`}))
	if code != buildlane.Findings || !strings.HasPrefix(why, "findings: p/a/package.json does not parse") {
		t.Errorf("%d %q", code, why)
	}
}

func TestDEF_B6_publish_ABadWorkspaceGlobCannotBeListed(t *testing.T) {
	_, code, why := Packages(tree(t, map[string]string{"package.json": `{"name":"r","workspaces":["["]}`}))
	if code != buildlane.CouldNotRun || !strings.HasPrefix(why, "could not run: the workspace [ could not be listed") {
		t.Errorf("%d %q", code, why)
	}
}

func TestDEF_B6_publish_AMemberDirListedTwiceIsReadOnce(t *testing.T) {
	pkgs, _, _ := Packages(tree(t, map[string]string{
		"package.json":     `{"name":"r","workspaces":["p/*","p/a"]}`,
		"p/a/package.json": `{"name":"a","version":"1","publishConfig":{"registry":"https://r/"}}`,
	}))
	if len(pkgs) != 1 {
		t.Errorf("%+v", pkgs)
	}
}

func TestDEF_B6_publish_PackDirIsTheMembersOwnOrTheRootsPlaceholder(t *testing.T) {
	if got := (Package{}).PackDir("/out"); got != "/out/npm/_root" {
		t.Error(got)
	}
	if got := (Package{Dir: "packages/a"}).PackDir("/out"); got != "/out/npm/packages/a" {
		t.Error(got)
	}
}
