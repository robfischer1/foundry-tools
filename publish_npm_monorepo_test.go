package main

import "testing"

// THE MONOREPO'S TS KIT (runbook D-B4). Once the kit lives in ts/, the root
// package.json is a private workspace declaration with no name and no version
// (the door's npmVerRe matches any "version", so the root must carry none), and
// the member ts/package.json is @forge/stellar-core-ts. The npm half already
// publishes a workspace's declared members and never the root; these hold the
// monorepo's shape to it, so a release comes from ts/ and nothing else.

func monorepoTree() map[string]string {
	return map[string]string{
		"package.json":    `{"private": true, "workspaces": ["ts"]}`,
		"bun.lock":        "",
		".npmrc":          "@forge:registry=" + npmHosted + "\n",
		"ts/package.json": stellarCoreTS,
		// the Rust and Go kits beside it are no npm package
		"rust/Cargo.toml": "[package]\nname = \"stellar-core\"\n",
		"go/go.mod":       "module git.notusmi.com/rob/stellar-core-go\n",
	}
}

func TestTheMonorepoPublishesTheTsMemberAndNeverTheRoot(t *testing.T) {
	m := npmTreeOn(t, monorepoTree())
	packumentAnswers("@forge/stellar-core-ts", "Not Found", "404")
	npmPublishes(t, m)
	settledOn(t, "0", "@forge/stellar-core-ts@0.6.0 — published to "+npmHosted)
	if n := probes(); n != 1 {
		t.Errorf("the registry was asked %d times, want once, for the member", n)
	}
	// built in the member's own directory (no turbo.json), published from it
	wantCalls(t, publishOf("ts"),
		[]string{"withExec", `"bun"`, `"install"`, `"--frozen-lockfile"`},
		[]string{"withWorkdir", `"/src/ts"`},
		[]string{"withExec", `"bun"`, `"run"`, `"build"`},
		[]string{"withMountedSecret", `"/src/.npmrc"`},
		[]string{"withWorkdir", `"/src/ts"`},
		[]string{"withExec", `"bun"`, `"publish"`, `"--registry"`, `"` + npmHosted + `"`, ignoreScripts},
	)
	if n := installs(); n != 1 {
		t.Errorf("the workspace installed %d times, want once at the root", n)
	}
}

// A released version is immutable: the first import of ts/ at 0.80.0 is a no-op.
func TestTheMonorepoWithTheMembersVersionReleasedPublishesNothing(t *testing.T) {
	m := npmTreeOn(t, monorepoTree())
	registryAnswers(`{"versions": {"0.5.0": {}, "0.6.0": {}}}`, "200")
	npmPublishes(t, m)
	settledOn(t, "0", "nothing to do — @forge/stellar-core-ts@0.6.0 is already on "+npmHosted)
	noNpmPublish(t)
}
