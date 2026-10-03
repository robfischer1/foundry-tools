package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// Built rather than spelled: a 40-hex literal read as a secret to
// detect-secrets, retired 2026-09-25. The shape stays because these are a
// commit and a tree that never existed, and it should be obvious.
var (
	fakeSha  = strings.Repeat("ab", 20)
	fakeTree = strings.Repeat("cd", 20)
)

// THE ENGINE FETCHES THE TREE WHEN --repo/--sha NAME IT, whole, through the
// repository's warm history (sourceAt): the fetch announces the one ref the
// volume keeps and lands the commit under it, the checkout is copied OUT of
// the volume (never linked into it), detached at the commit, with origin
// naming the door. No depth: fleet:witness and the mutation lane diff against
// the pull's merge base, which no fixed depth reaches. Nothing is fetched
// until a function asks; Lanes asks.
func TestNewFetchesTheCommitOnTheEngineWithFullHistory(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	const repo = "http://door:8215/infra.git"
	m, err := New(dag.Directory(), repo, fakeSha)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Lanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	fetch := `args:["git","-C","/cache/src.git","-c","gc.autoDetach=false","-c","maintenance.autoDetach=false","fetch","--quiet","--no-tags","` + repo + `","+` + fakeSha + `:refs/ca/fetched"]`
	c := engine.chain(fetch)
	if c == "" {
		t.Fatalf("no fetch of the named repo into its warm history was asked for; chains: %v", engine.chains())
	}
	// In order: the volume mounted LOCKED, the history initialised, fetched,
	// kept, copied out, checked out at the commit, origin pointed at the door,
	// and the checkout — not the volume — is the tree. The fetch keeps the
	// volume itself (its auto-maintenance, in the foreground).
	steps := []string{
		`sharing:LOCKED`,
		`args:["git","init","-q","--bare","/cache/src.git"]`,
		fetch,
		`args:["git","clone","--quiet","--no-checkout","--no-hardlinks","--no-tags","/cache/src.git","/out"]`,
		`args:["git","-C","/out","-c","advice.detachedHead=false","checkout","--quiet","--detach","` + fakeSha + `"]`,
		`args:["git","-C","/out","remote","set-url","origin","` + repo + `"]`,
		`directory(path:"/out")`,
		// GitRef.tree's timestamps: go's test cache keys on mtimes.
		`withTimestamps(timestamp:1)`,
	}
	at := 0
	for _, s := range steps {
		i := strings.Index(c[at:], s)
		if i < 0 {
			t.Fatalf("the fetch chain lacks %s after offset %d: %s", s, at, c)
		}
		at += i + len(s)
	}
	if strings.Contains(c, "--depth") || strings.Contains(c, "--filter") {
		t.Errorf("the history must be whole: %s", c)
	}
	if !strings.Contains(c, `path:"/cache"`) {
		t.Errorf("the warm history is mounted at /cache: %s", c)
	}
	if engine.chain(`cacheVolume(key:"`+checks.SourceCacheKey(repo)+`")`) == "" {
		t.Errorf("the volume is the repository's own (%s): %v", checks.SourceCacheKey(repo), engine.chains())
	}
	if engine.chain(`git(url:`) != "" {
		t.Errorf("the engine's own git mirror must not be asked: %v", engine.chains())
	}
}

// A FETCH THAT FAILS IS NO TREE, NEVER AN EMPTY ONE. The door down, the commit
// unknown: the chain does not evaluate, and Tree refuses with the engine's
// error rather than answering a hash.
func TestNewFetchFailureRefusesTheTree(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"fetch","--quiet","--no-tags","http://door:8215/infra.git"`, "fatal: remote error: upload-pack: not our ref")
	m, err := New(dag.Directory(), "http://door:8215/infra.git", fakeSha)
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Tree(context.Background())
	if err == nil || got != "" {
		t.Fatalf("a failed fetch must refuse the tree, got %q, %v", got, err)
	}
	if !strings.Contains(err.Error(), "not our ref") {
		t.Errorf("the fetch's own error is carried: %v", err)
	}
}

// NEITHER: the caller's own tree, untouched — no git query at all.
func TestNewWithoutRepoBindsTheCallersTree(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	m, err := New(dag.Directory(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	out, err := m.Lanes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "go (go.mod)") {
		t.Errorf("the caller's tree was not bound: %q", out)
	}
	if engine.chain(`git(url:`) != "" {
		t.Errorf("no repo was named, yet something was fetched: %v", engine.chains())
	}
}

// ONE WITHOUT THE OTHER IS AN ERROR, and each error names the missing half.
// A sha alone has nowhere to fetch from; a repo alone would grade whatever
// the branch pointed at by the time the engine looked.
func TestNewRefusesHalfACoordinate(t *testing.T) {
	engine.reset()
	if m, err := New(dag.Directory(), "", fakeSha); err == nil || m != nil {
		t.Errorf("--sha without --repo must be refused, got module %v err %v", m, err)
	} else if !strings.Contains(err.Error(), "--repo") {
		t.Errorf("the refusal must name --repo as the missing half: %v", err)
	}
	if m, err := New(dag.Directory(), "http://door:8215/infra.git", ""); err == nil || m != nil {
		t.Errorf("--repo without --sha must be refused, got module %v err %v", m, err)
	} else if !strings.Contains(err.Error(), "--sha") || !strings.Contains(err.Error(), "http://door:8215/infra.git") {
		t.Errorf("the refusal must name --sha as the missing half and the repo given: %v", err)
	}
}

// TREE ANSWERS HEAD^{tree} OF THE BOUND REPOSITORY — the key the receipt is
// written under — and refuses to answer anything else: a non-zero rev-parse
// (a snapshot with no commit), an empty answer, or an engine that could not
// evaluate the chain are all errors, never "". Nothing keyed on "" may be
// attested.
func TestTreeAnswersTheTreeHashOrRefuses(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stdout(`"rev-parse","HEAD^{tree}"`, fakeTree+"\n")
	m := &FoundryTools{Source: dag.Directory()}
	got, err := m.Tree(context.Background())
	if err != nil || got != fakeTree {
		t.Fatalf("Tree = %q, %v; want the trimmed hash and no error", got, err)
	}
	if engine.chain(`"rev-parse","HEAD^{tree}"`, `withNewFile(`, `path:"/etc/gitconfig"`) == "" {
		t.Errorf("the tree is read through gitReady (safe.directory first): %v", engine.chains())
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"rev-parse","HEAD^{tree}"`, 128)
	engine.stdout(`"rev-parse","HEAD^{tree}"`, "fatal: bad revision\n")
	if got, err := m.Tree(context.Background()); err == nil || got != "" {
		t.Errorf("a failed rev-parse must be an error, got %q, %v", got, err)
	} else if !strings.Contains(err.Error(), "could not read the tree: exit code: 128") {
		t.Errorf("git's fatal 128 is the engine's error, never a code Tree reads: %v", err)
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stdout(`"rev-parse","HEAD^{tree}"`, "")
	if got, err := m.Tree(context.Background()); err == nil || got != "" {
		t.Errorf("an empty answer must be an error, got %q, %v", got, err)
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"rev-parse","HEAD^{tree}"`, "engine went away")
	if got, err := m.Tree(context.Background()); err == nil || got != "" {
		t.Errorf("an engine error must be an error, got %q, %v", got, err)
	} else if !strings.Contains(err.Error(), "engine went away") {
		t.Errorf("the engine's error is carried: %v", err)
	}
}

// THE BASE RIDES IN WITH A FETCHED TREE, and only then. An atom that reads
// GATE_BASE on an engine-fetched Source fetches the base from the same door
// by sha before its tool runs; on the caller's own tree nothing is fetched,
// because the clone that tree came from already reaches it.
func TestWithBaseFetchesTheBaseOnlyForAFetchedTree(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	m := &FoundryTools{Source: dag.Directory(), Repo: "http://door:8215/infra.git"}
	if _, err := m.Verdicts(context.Background(), "", "fleet:witness", fakeSha); err != nil {
		t.Fatal(err)
	}
	c := engine.chain(`"--get","ca.snapshot"`)
	if !strings.Contains(c, `args:["git","-c","safe.directory=*","-C","/src","fetch","--quiet","--no-tags","http://door:8215/infra.git","`+fakeSha+`"]`) {
		t.Errorf("the witness chain on a fetched tree must fetch the base from the door: %s", c)
	}
	if strings.Index(c, `"fetch"`) > strings.Index(c, `"--get","ca.snapshot"`) {
		t.Errorf("the fetch must precede the tool: %s", c)
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	m = &FoundryTools{Source: dag.Directory()}
	if _, err := m.Verdicts(context.Background(), "", "fleet:witness", fakeSha); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(engine.chain(`"--get","ca.snapshot"`), `"fetch"`) {
		t.Errorf("the caller's own tree already reaches the base; nothing must be fetched")
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	m = &FoundryTools{Source: dag.Directory(), Repo: "http://door:8215/infra.git"}
	if _, err := m.Verdicts(context.Background(), "", "fleet:witness", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(engine.chain(`"--get","ca.snapshot"`), `"fetch"`) {
		t.Errorf("a tip has no base to fetch")
	}
}
