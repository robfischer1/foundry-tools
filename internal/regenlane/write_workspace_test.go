package regenlane

import (
	"os"
	"path/filepath"
	"testing"
)

// The dev lane hands dev:ts-regen the repo root whatever subtree the request came
// from, and stellar-core's root is a bare workspace root whose one member, ts/, holds
// the justfile and the cores: the write twin reads them there, as ts:regen does.
func TestDevTSRegenOfABareWorkspaceRootRegeneratesTheMembersWorld(t *testing.T) {
	_, in, _ := withFake(t, writeStar(t, "1.250.0", "stamp"))
	outer := t.TempDir()
	if err := os.Rename(in.Root, filepath.Join(outer, "ts")); err != nil {
		t.Fatal(err)
	}
	put(t, outer, "package.json", wsRoot)
	put(t, outer, "ts/package.json", "{}")
	in.Root = outer
	in.Files = []string{"package.json", "ts/package.json", "ts/justfile", "ts/src/stampcore-core-gen/provenance.json"}
	wantWrite(t, CheckWrite(t.Context(), in), 0, "dev:ts-regen: PASS - 1 world(s) regenerated", "dev:ts-regen(stamp): guest.wasm unchanged")
}

func TestDevTSRegenOfABareRootWithTwoMembersLeavesTheTreeAsItIs(t *testing.T) {
	in := bare(t, map[string]string{"package.json": `{"private": true, "workspaces": ["a", "b"]}`, "a/package.json": "{}", "b/package.json": "{}"})
	in.Args = []string{"stamp"}
	wantWrite(t, CheckWrite(t.Context(), in), 1, "no src/stampcore-gen/provenance.json")
}
