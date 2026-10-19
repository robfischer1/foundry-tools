package checks

import (
	"slices"
	"testing"
)

func TestTSBareWorkspaceMembers(t *testing.T) {
	files := []string{"package.json", "ts/package.json", "ts/src/a.ts", "tools/x/package.json", "node_modules/q/package.json", "ts/node_modules/z/package.json", "packages/a/package.json", "packages/b/package.json", "packages/b/sub/package.json"}
	for _, tc := range []struct {
		name, root string
		want       []string
		bare       bool
	}{
		{"the monorepo's root", `{"private": true, "workspaces": ["ts"]}`, []string{"ts"}, true},
		{"the object spelling", `{"private": true, "workspaces": {"packages": ["packages/*"]}}`, []string{"packages/a", "packages/b"}, true},
		{"a double star takes everything below", `{"private": true, "workspaces": ["packages/**"]}`, []string{"packages/a", "packages/b", "packages/b/sub"}, true},
		{"a leading ./ and a trailing / are the same glob", `{"private": true, "workspaces": ["./ts/"]}`, []string{"ts"}, true},
		{"an empty scripts object is still bare", `{"private": true, "scripts": {}, "workspaces": ["ts"]}`, []string{"ts"}, true},
		{"bare, and nothing matches: declared, and nothing to run", `{"private": true, "workspaces": ["apps/*"]}`, nil, true},
		{"node_modules are never members", `{"private": true, "workspaces": ["*/*"]}`, []string{"packages/a", "packages/b", "tools/x"}, true},
		// every shape that is NOT bare keeps running its root scripts
		{"theia: a workspace root with scripts", `{"private": true, "scripts": {"gate": "turbo run gate"}, "workspaces": ["packages/*"]}`, nil, false},
		{"a public workspace root", `{"workspaces": ["ts"]}`, nil, false},
		{"a package root", `{"name": "@forge/x", "version": "1.0.0", "scripts": {"gate": "x"}}`, nil, false},
		{"a private package that is no workspace", `{"private": true}`, nil, false},
		{"an empty workspace list", `{"private": true, "workspaces": []}`, nil, false},
		{"not JSON", `{`, nil, false},
		{"workspaces of the wrong type", `{"private": true, "workspaces": 3}`, nil, false},
	} {
		got, bare := TSBareWorkspaceMembers(tc.root, files)
		if bare != tc.bare || !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v %v, want %v %v", tc.name, got, bare, tc.want, tc.bare)
		}
	}
}
