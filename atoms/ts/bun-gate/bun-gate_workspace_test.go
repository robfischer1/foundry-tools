package tsbungate

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
)

// THE BARE WORKSPACE ROOT (runbook D-B4): the monorepo's root package.json
// declares ts/ as a workspace and has no scripts, so `bun run gate` runs in the
// member. Every other root runs its own gate where it always did.

const bareRoot = `{"private": true, "workspaces": ["ts"]}`

func rooted(t *testing.T, s *script, manifest string, files ...string) atomkit.In {
	t.Helper()
	in := withRoot(t, tree(s, files...))
	if err := os.WriteFile(filepath.Join(in.Root, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return in
}

func TestABareWorkspaceRootRunsTheGateInItsMember(t *testing.T) {
	s := &script{}
	in := rooted(t, s, bareRoot, "package.json", "ts/package.json", "ts/src/a.test.ts")
	v := check(t.Context(), in)
	if v.State != 0 || v.Reason != "ts:bun-gate: PASS" || !strings.Contains(strings.Join(v.Logs, "\n"), "PASS in 1 workspace members") {
		t.Fatalf("%+v", v)
	}
	if want := []string{"bun install --frozen-lockfile", "bun run gate"}; !slices.Equal(s.argvs(), want) {
		t.Errorf("%v", s.argvs())
	}
	// one frozen install at the root (a workspace's lockfile is the root's), the gate in the member
	if s.calls[0].Dir != in.Root || s.calls[1].Dir != filepath.Join(in.Root, "ts") {
		t.Errorf("install in %q, gate in %q", s.calls[0].Dir, s.calls[1].Dir)
	}
}

func TestTheFirstMemberThatFailsIsTheVerdictAndNamesItself(t *testing.T) {
	s := &script{exits: []int{0, 0, 2}, out: []string{"", "", "TS2769 type error"}}
	in := rooted(t, s, `{"private": true, "workspaces": ["packages/*"]}`,
		"package.json", "packages/a/package.json", "packages/b/package.json", "packages/b/x.test.ts")
	v := check(t.Context(), in)
	// tsc's 2 is a finding, as for any gate (checks.BunGateExit)
	if v.State != 1 || !strings.Contains(v.Reason, "ts:bun-gate [b]") {
		t.Fatalf("%+v", v)
	}
	if got := len(s.calls); got != 3 {
		t.Errorf("install, a, b: %v", s.argvs())
	}
}

// A bare root whose workspaces match nothing was declared and has nothing to run:
// not a pass.
func TestABareRootWithNoMemberIsACouldNotRun(t *testing.T) {
	s := &script{}
	in := rooted(t, s, `{"private": true, "workspaces": ["apps/*"]}`, "package.json", "src/a.test.ts")
	if v := check(t.Context(), in); v.State != 2 || !strings.Contains(v.Reason, "bare workspace root") {
		t.Errorf("%+v", v)
	}
	if len(s.calls) != 1 {
		t.Errorf("nothing runs without a member: %v", s.argvs())
	}
}

// theia's shape, a private workspace root WITH scripts, and a package root keep
// running at the root.
func TestARootThatHasItsOwnScriptsStillRunsThem(t *testing.T) {
	for name, manifest := range map[string]string{
		"theia":   `{"private": true, "scripts": {"gate": "turbo run gate"}, "workspaces": ["packages/*"]}`,
		"package": `{"name": "@forge/x", "version": "1.0.0", "scripts": {"gate": "x"}}`,
	} {
		s := &script{}
		in := rooted(t, s, manifest, "package.json", "packages/a/package.json", "packages/a/x.test.ts")
		if v := check(t.Context(), in); v.State != 0 || v.Reason != "ts:bun-gate: PASS" {
			t.Fatalf("%s: %+v", name, v)
		}
		if s.calls[1].Dir != in.Root {
			t.Errorf("%s: the gate ran in %q, not the root", name, s.calls[1].Dir)
		}
	}
}
