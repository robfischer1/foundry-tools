package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/pins"
)

const (
	governRepo = "http://door:8215/foundry/foundry-stocks.git"
	// governStage is where each consumer's render is staged, and governChannel
	// the repository its digest is verified at.
	governStage   = "foundry.notusmi.com/staging/runtime-gov-governance."
	governChannel = "foundry.notusmi.com/runtime-gov/governance."
)

// governDies is foundry-stocks' dies.toml where the governance cast reads it:
// the three path dies, one of them home.
const governDies = `
[dies."governance/forge-root"]
source  = "https://git.notusmi.com/foundry/foundry-stocks.git@main"
path    = "renders/forge-root/claude"

[dies."governance/vault"]
source  = "https://git.notusmi.com/foundry/foundry-stocks.git@main"
path    = "renders/vault/claude"

[dies."governance/home"]
source  = "https://git.notusmi.com/foundry/foundry-stocks.git@main"
path    = "renders/home/claude"
`

// governOn is the module constructed on foundry-stocks at a landing the engine
// fetched. A tree entry with an empty value removes that file.
func governOn(t *testing.T, tree map[string]string) *FoundryTools {
	t.Helper()
	engine.reset()
	base := map[string]string{
		"dies.toml":                           governDies,
		"cosign.pub":                          "-----BEGIN PUBLIC KEY-----\nabc\n-----END PUBLIC KEY-----\n",
		"renders/forge-root/claude/AGENTS.md": "root",
		"renders/forge-root/claude/.furnace/spans.json": "{}",
		"renders/vault/claude/AGENTS.md":                "vault",
		"renders/home/claude/AGENTS.md":                 "home",
	}
	for k, v := range tree {
		if v == "" {
			delete(base, k)
			continue
		}
		base[k] = v
	}
	engine.withTree(base)
	return &FoundryTools{Source: dag.Directory(), Repo: governRepo, Sha: buildSha}
}

// scriptAGovern answers every step of a cast that lands: the pin, the staging
// push and the mint.
func scriptAGovern() {
	engine.stdout(castpinNeedle, castPin+"\n.furnace/spans.json\nAGENTS.md\n")
	engine.stdout(stageNeedle, castStaged+"\n")
	engine.stdout(mintNeedle, "HTTP 200\n"+toolAnswer(false, castResult(castPin, true)))
}

func governs(t *testing.T, m *FoundryTools, consumers ...string) {
	t.Helper()
	governWith(t, m, dag.LoadSocketFromID("spire-agent-socket"), dag.SetSecret("registry-token", "tok"), consumers, false)
}

func governWith(t *testing.T, m *FoundryTools, spire *dagger.Socket, token *dagger.Secret, consumers []string, dryRun bool) {
	t.Helper()
	if err := m.Govern(context.Background(), spire, token, consumers, "https://hades:8102", "spiffe://notusmi.com/star/hades", dryRun); err != nil {
		t.Fatalf("govern: %v", err)
	}
}

// With no consumer named, the cast takes forge-root and vault: each render is
// taken from the tree at the landing, pinned, staged under its own name, minted
// SIGNED as a runtime-gov bundle against the landing's sha and verified at the
// channel the mint answered.
func TestAGovernCastEachRenderAsASignedRuntimeGovBundle(t *testing.T) {
	m := governOn(t, nil)
	scriptAGovern()
	said := sayings(t, func() { governs(t, m) })
	settledOn(t, "0", "govern: governance/forge-root: clean: cast runtime-gov/governance.forge-root:stable at index 7 ("+castPin+", "+castLanded+")")
	// Each minted head is declared, so the door counts it in use.
	for _, consumer := range []string{"forge-root", "vault"} {
		if !strings.Contains(said, `"artifact":"`+governChannel+consumer+`"`) {
			t.Errorf("%s: the minted head was not declared:\n%s", consumer, said)
		}
	}
	settledOn(t, "0", "governance/vault: clean: cast runtime-gov/governance.vault:stable at index 7")

	for _, consumer := range []string{"forge-root", "vault"} {
		if engine.chain(`directory(path:"renders/`+consumer+`/claude")`) == "" {
			t.Errorf("%s: its render was not taken from the tree", consumer)
		}
		wantCalls(t, engine.chain(stageNeedle, governStage+consumer+":"+castPin),
			[]string{"withMountedSecret", `"/run/docker/config.json"`},
			[]string{"withExec", `"--registry-config"`, `"{{.digest}}"`, `.furnace/spans.json","AGENTS.md"]`},
		)
		mint := engine.chain(mintNeedle, "runtime-gov/governance."+consumer+":stable")
		wantCalls(t, mint, []string{"withExec", governStage + consumer + ":" + castPin + "@" + castStaged, buildSha})
		// SIGNED ON PURPOSE: the lane asks for the signature by name.
		if !strings.Contains(mint, `sign\":true`) {
			t.Errorf("%s: the mint did not ask for sign=true:\n%s", consumer, mint)
		}
		wantCalls(t, engine.chain(verifyNeedle, governChannel+consumer),
			[]string{"withFile", `"/run/cosign/cosign.pub"`},
			[]string{"withExec", `"--insecure-ignore-tlog=true"`, `"` + governChannel + consumer + `@` + castLanded + `"`},
		)
	}
	// No bell: delivery of a governance channel is tongs' sweep, not a doorbell.
	if engine.chain(bellNeedle) != "" {
		t.Error("the governance cast rang the release doorbell")
	}
}

func TestAGovernCastsOnlyTheConsumersAsked(t *testing.T) {
	m := governOn(t, nil)
	scriptAGovern()
	governs(t, m, "vault")
	settledOn(t, "0", "governance/vault: clean: cast runtime-gov/governance.vault:stable")
	if engine.chain(stageNeedle, "governance.forge-root") != "" || engine.chain(`renders/forge-root`) != "" {
		t.Error("a consumer that was not asked for was read or staged")
	}
}

// THE HOLD. home is refused by name, alone or among others, and nothing is
// pinned, staged or minted for any consumer of a request that names it.
func TestAGovernNeverCastsTheHeldConsumer(t *testing.T) {
	for _, ask := range [][]string{{"home"}, {"vault", "home"}, {"home", "forge-root"}} {
		m := governOn(t, nil)
		scriptAGovern()
		governs(t, m, ask...)
		settledOn(t, "1", "governance/home is held")
		for _, needle := range []string{castpinNeedle, stageNeedle, mintNeedle, verifyNeedle, `renders/home`} {
			if engine.chain(needle) != "" {
				t.Errorf("%v reached %s", ask, needle)
			}
		}
	}
}

func TestAGovernWithoutItsCredentialsCannotRun(t *testing.T) {
	m := governOn(t, nil)
	governWith(t, m, nil, dag.SetSecret("registry-token", "tok"), nil, false)
	settledOn(t, "2", "--spire and --registry-token are both required")

	m = governOn(t, nil)
	governWith(t, m, dag.LoadSocketFromID("spire-agent-socket"), nil, nil, false)
	settledOn(t, "2", "--spire and --registry-token are both required")

	m = governOn(t, nil)
	m.Sha = ""
	governs(t, m)
	settledOn(t, "2", "construct the module with --repo and --sha")

	m = governOn(t, nil)
	m.Repo = ""
	governs(t, m)
	settledOn(t, "2", "construct the module with --repo and --sha")
}

// A dry run pins for real and stages, mints and verifies nothing, with no
// credentials at all.
func TestAGovernDryRunPinsAndPublishesNothing(t *testing.T) {
	m := governOn(t, nil)
	scriptAGovern()
	governWith(t, m, nil, nil, []string{"vault"}, true)
	settledOn(t, "0", "governance/vault: clean: dry run — 2 file(s) pin to "+castPin+" for runtime-gov/governance.vault:stable")
	if engine.chain(castpinNeedle) == "" {
		t.Fatal("a dry run did not pin")
	}
	for _, needle := range []string{stageNeedle, mintNeedle, verifyNeedle} {
		if engine.chain(needle) != "" {
			t.Errorf("a dry run reached %s", needle)
		}
	}
}

// Everything the lane refuses before it pins is refused with nothing pinned.
func TestAGovernThatCannotStartPinsNothing(t *testing.T) {
	cases := map[string]struct {
		tree         map[string]string
		script       func()
		consumers    []string
		code, reason string
	}{
		"no dies.toml":              {map[string]string{"dies.toml": ""}, nil, nil, "1", "carries no dies.toml"},
		"dies.toml unreadable":      {nil, func() { engine.failLeaf(`pattern:"dies.toml"`, "glob", "the tree went away") }, nil, "2", "the tree could not be read for dies.toml: "},
		"a stanza the plan lacks":   {nil, nil, []string{"nomos"}, "1", `stanza`},
		"another repository's tree": {nil, nil, nil, "1", "not from this repository (rob/other)"},
		"no cosign.pub":             {map[string]string{"cosign.pub": ""}, nil, nil, "1", "carries no cosign.pub"},
		"cosign.pub unreadable":     {nil, func() { engine.failLeaf(`pattern:"cosign.pub"`, "glob", "the tree went away") }, nil, "2", "the tree could not be read for cosign.pub: "},
		"the registry token cannot be read": {nil, func() {
			engine.failLeaf(`"registry-token"`, "plaintext", "the secret went away")
		}, nil, "2", "the registry token did not read"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := governOn(t, c.tree)
			if name == "another repository's tree" {
				m.Repo = "http://door:8215/rob/other.git"
			}
			scriptAGovern()
			if c.script != nil {
				c.script()
			}
			governs(t, m, c.consumers...)
			settledOn(t, c.code, c.reason)
			if engine.chain(castpinNeedle) != "" || engine.chain(stageNeedle) != "" {
				t.Fatal("a cast that could not start pinned or staged")
			}
		})
	}
}

// A consumer with no render at this commit is a finding that says so, and its
// neighbours are still cast: one die does not starve the other, and the lane
// settles on the failure.
func TestAGovernOfAnUnrenderedConsumerIsAFindingAndTheRestStillCast(t *testing.T) {
	m := governOn(t, map[string]string{"renders/forge-root/claude/AGENTS.md": "", "renders/forge-root/claude/.furnace/spans.json": ""})
	scriptAGovern()
	governs(t, m)
	settledOn(t, "1", "governance/forge-root: findings: this commit holds no renders/forge-root/claude")
	settledOn(t, "1", "governance/vault: clean: cast runtime-gov/governance.vault:stable")
	if engine.chain(stageNeedle, "governance.forge-root") != "" {
		t.Error("a consumer with no render was staged")
	}
}

func TestAGovernSaysWhenTheRenderCannotBeProbed(t *testing.T) {
	m := governOn(t, nil)
	scriptAGovern()
	engine.failLeaf("DIRECTORY_TYPE", "exists", "the tree went away")
	governs(t, m, "vault")
	settledOn(t, "2", "the tree could not be read for renders/vault/claude")
	if engine.chain(castpinNeedle) != "" {
		t.Error("a render that could not be probed was pinned")
	}
}

// Every step that fails after the pin settles with what it means and nothing
// after it runs, for the die that failed.
func TestAGovernThatFailsStopsWhereItFailed(t *testing.T) {
	cases := map[string]struct {
		script       func()
		code, reason string
		never        []string
	}{
		"castpin fails": {func() {
			engine.exitCode(castpinNeedle, 2)
		}, "2", "castpin exited 2", []string{stageNeedle}},
		"the registry refuses the push": {func() {
			engine.exitCode(stageNeedle, 1)
			engine.stdout(stageNeedle, "Error: failed to push: unauthorized")
		}, "1", "findings in staging push", []string{mintNeedle}},
		"the push answers no digest": {func() {
			engine.stdout(stageNeedle, "Pushed\n")
		}, "2", "answered no digest", []string{mintNeedle}},
		"the policy refuses the lane": {func() {
			engine.stdout(mintNeedle, "HTTP 403\n{\"detail\":\"denied\"}")
		}, "1", "not granted layer_cast", []string{verifyNeedle}},
		"hephaestus refuses the payload": {func() {
			engine.stdout(mintNeedle, "HTTP 200\n"+toolAnswer(true, "layer_cast: staged payload pin mismatch"))
		}, "1", "findings in layer_cast", []string{verifyNeedle}},
		"hephaestus mints another pin": {func() {
			engine.stdout(mintNeedle, "HTTP 200\n"+toolAnswer(false, castResult("gffffffffffff", true)))
		}, "2", "minted pin gffffffffffff", []string{verifyNeedle}},
		"the landed digest does not verify": {func() {
			engine.exitCode(verifyNeedle, 10)
			engine.stdout(verifyNeedle, "Error: no matching signatures")
		}, "1", "findings in cosign verify " + governChannel + "vault@" + castLanded, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := governOn(t, nil)
			scriptAGovern()
			c.script()
			governs(t, m, "vault")
			settledOn(t, c.code, c.reason)
			for _, needle := range c.never {
				if engine.chain(needle) != "" {
					t.Errorf("went on to %s", needle)
				}
			}
		})
	}
}

// A no-op mint is clean and says the channel already carried the render.
func TestAGovernOfAnUnchangedRenderSaysSo(t *testing.T) {
	m := governOn(t, nil)
	scriptAGovern()
	engine.stdout(mintNeedle, "HTTP 200\n"+toolAnswer(false, strings.Replace(castResult(castPin, true), `"noop":false`, `"noop":true`, 1)))
	governs(t, m, "vault")
	settledOn(t, "0", "already carried this pin")
}

// resolveNeedle is the head check: the channel tag and the render's pin tag
// resolved in one exec.
const resolveNeedle = `oras resolve`

// THE POLL COSTS NOTHING. A channel whose head is already this render's own
// manifest, and verifies, is left alone: nothing is staged, nothing is minted,
// so hephaestus never re-signs the digest and adds a referrer every period.
func TestAGovernOfARenderTheHeadAlreadyCarriesStagesAndMintsNothing(t *testing.T) {
	m := governOn(t, nil)
	scriptAGovern()
	engine.stdout(resolveNeedle, castLanded+"\n"+castLanded+"\n")
	said := sayings(t, func() { governs(t, m, "vault") })
	// The head is still declared, so the door keeps counting it in use.
	if !strings.Contains(said, pins.MarkerPrefix) || !strings.Contains(said, `"artifact":"`+strings.TrimSuffix(governChannel, ".")+`.vault"`) || !strings.Contains(said, castLanded) {
		t.Errorf("the current head was not declared:\n%s", said)
	}
	settledOn(t, "0", "governance/vault: clean: runtime-gov/governance.vault:stable already carries "+castPin+" ("+castLanded+"), signed")
	wantCalls(t, engine.chain(resolveNeedle),
		[]string{"withMountedSecret", `"/run/docker/config.json"`},
		[]string{"withExec", `"` + governChannel + `vault:stable"`, `"` + governChannel + `vault:` + castPin + `"`},
	)
	if engine.chain(verifyNeedle, governChannel+"vault@"+castLanded) == "" {
		t.Error("the current head was not verified before it was trusted")
	}
	for _, needle := range []string{stageNeedle, mintNeedle} {
		if engine.chain(needle) != "" {
			t.Errorf("a current head still reached %s", needle)
		}
	}
}

// A head that is not this render, or that does not verify, or a registry that
// could not be asked, is cast in full: the check only ever saves a cast.
func TestAGovernCastsInFullWhenTheHeadIsNotKnownCurrent(t *testing.T) {
	other := "sha256:" + strings.Repeat("9", 64)
	cases := map[string]struct {
		script       func()
		code, reason string
	}{
		"the head is another render": {func() {
			engine.stdout(resolveNeedle, other+"\n"+castLanded+"\n")
		}, "0", "clean: cast runtime-gov/governance.vault:stable at index 7"},
		"the pin tag does not resolve": {func() {
			engine.exitCode(resolveNeedle, 1)
			engine.stdout(resolveNeedle, castLanded+"\n")
		}, "0", "clean: cast runtime-gov/governance.vault:stable at index 7"},
		"the current head does not verify": {func() {
			engine.stdout(resolveNeedle, castLanded+"\n"+castLanded+"\n")
			engine.exitCode(verifyNeedle, 10)
		}, "1", "findings in cosign verify " + governChannel + "vault@" + castLanded},
		"the head check cannot run": {func() {
			engine.failLeaf(resolveNeedle, "exitCode", "the engine went away")
		}, "0", "clean: cast runtime-gov/governance.vault:stable at index 7"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := governOn(t, nil)
			scriptAGovern()
			c.script()
			governs(t, m, "vault")
			settledOn(t, c.code, c.reason)
			if engine.chain(stageNeedle) == "" || engine.chain(mintNeedle) == "" {
				t.Error("a head not known current was not cast in full")
			}
		})
	}
}

// With no oras to ask with, the head check answers nothing and the cast runs,
// to fail where it pushes and say so.
func TestAGovernWithNoOrasStillReachesTheStagingPush(t *testing.T) {
	m := governOn(t, nil)
	scriptAGovern()
	engine.fail(`http(url:"`+checks.OrasURL+`")`, "502 from upstream")
	governs(t, m, "vault")
	settledOn(t, "2", "oras could not be provisioned")
	if engine.chain(mintNeedle) != "" {
		t.Error("minted without a staged payload")
	}
}
