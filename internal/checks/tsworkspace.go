package checks

import (
	"encoding/json"
	"path"
	"slices"
	"strings"
)

// THE BARE WORKSPACE ROOT (monorepo migration, runbook D-B4).
//
// The ts lane's atoms run the repository's scripts at its root: `bun run gate`,
// `bun run test`, the justfile's regen-check. That holds while the repository IS
// the package. When the TS kit lives in a subdirectory (stellar-core's ts/), the
// root package.json is only the workspace declaration, `{"private": true,
// "workspaces": ["ts"]}`, it has no scripts, and every one of those commands
// fails there, or reads a justfile that is not the kit's.
//
// A BARE WORKSPACE ROOT is the opt-in shape: private, workspaces declared, and
// NO scripts of its own. A workspace root with scripts (theia's turbo root with
// its gate script) runs its scripts at the root exactly as it always has, and a
// package root is untouched; both answer "not bare", and nothing about their
// plans changes.

// TSBareWorkspaceMembers answers the member directories of a bare workspace
// root, sorted, from the root package.json and the tree's file list, and whether
// the root is bare at all. A bare root whose globs match no member answers
// (nil, true): the opt-in shape was declared and there is nothing to run, which
// a caller must not read as a pass.
func TSBareWorkspaceMembers(rootPackageJSON string, files []string) (members []string, bare bool) {
	var doc struct {
		Private    bool            `json:"private"`
		Scripts    map[string]any  `json:"scripts"`
		Workspaces json.RawMessage `json:"workspaces"`
	}
	if json.Unmarshal([]byte(rootPackageJSON), &doc) != nil || !doc.Private || len(doc.Scripts) > 0 {
		return nil, false
	}
	globs := workspaceGlobs(doc.Workspaces)
	if len(globs) == 0 {
		return nil, false
	}
	for _, f := range files {
		dir, name := path.Split(f)
		dir = strings.TrimSuffix(dir, "/")
		if name != "package.json" || dir == "" || strings.Contains("/"+dir+"/", "/node_modules/") {
			continue
		}
		if matchesAnyGlob(globs, dir) && !slices.Contains(members, dir) {
			members = append(members, dir)
		}
	}
	slices.Sort(members)
	return members, true
}

// workspaceGlobs reads `workspaces` in either spelling (a list, or an object
// with packages); anything else declares none.
func workspaceGlobs(raw json.RawMessage) []string {
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var obj struct {
		Packages []string `json:"packages"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Packages
	}
	return nil
}

// matchesAnyGlob matches a member directory against the declared globs. `*`
// stays within one path segment (path.Match); a trailing `/**` takes everything
// below its prefix. A negated glob ("!x") excludes nothing here: it only ever
// narrows a set, and a member the lane runs that bun would skip is found by its
// own script failing, not hidden.
func matchesAnyGlob(globs []string, dir string) bool {
	for _, g := range globs {
		g = strings.TrimPrefix(strings.TrimPrefix(g, "./"), "/")
		if strings.HasPrefix(g, "!") {
			continue
		}
		if prefix, ok := strings.CutSuffix(g, "/**"); ok {
			if dir == prefix || strings.HasPrefix(dir, prefix+"/") {
				return true
			}
			continue
		}
		if ok, _ := path.Match(strings.TrimSuffix(g, "/"), dir); ok {
			return true
		}
	}
	return false
}
