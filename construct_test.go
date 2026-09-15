package main

import (
	"context"
	"strings"
	"testing"
)

// Built rather than spelled: a 40-hex literal reads as a secret to
// detect-secrets, and these are a commit and a tree that never existed.
var (
	fakeSha  = strings.Repeat("ab", 20)
	fakeTree = strings.Repeat("cd", 20)
)

// THE ENGINE FETCHES THE TREE WHEN --repo/--sha NAME IT. The constructor
// builds git(url).ref(sha).tree(depth: -1) — full history, because
// fleet:witness and the mutation lane diff against the pull's base and the
// engine's default depth is 1 (measured 2026-09-13: default 1 commit, -1 the
// whole 135). Nothing is fetched until a function asks; Lanes asks.
func TestNewFetchesTheCommitOnTheEngineWithFullHistory(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	m, err := New(dag.Directory(), "http://door:8215/infra.git", fakeSha)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Lanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := engine.chain(`git(url:"http://door:8215/infra.git")`)
	if c == "" {
		t.Fatalf("no git fetch of the named repo was asked for; chains: %v", engine.chains())
	}
	for _, want := range []string{`ref(name:"` + fakeSha + `")`, `tree(depth:-1)`} {
		if !strings.Contains(c, want) {
			t.Errorf("the fetch chain lacks %s: %s", want, c)
		}
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
	if engine.chain(`"rev-parse","HEAD^{tree}"`, `"safe.directory","*"`) == "" {
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
