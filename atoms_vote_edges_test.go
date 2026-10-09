package main

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// THE BINARY STARTS BEFORE THE CHAINS ARE DONE, not after: the lane's wall time
// is the longer of the two, not their sum. The one chain atom left in this
// vector waits until the binary has been started, and says so if it never is.
func TestTheBinaryIsStartedBesideTheChainsNotAfterThem(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	casting(t, voterBinary)
	castBinary = func(_ context.Context, _ *FoundryTools, stage, _ string) (string, error) {
		once.Do(func() { close(started) })
		return binaryVector(t, stage), nil
	}
	const slow = "ops:kube-linter"
	orig := registry[slow]
	t.Cleanup(func() { registry[slow] = orig })
	registry[slow] = func(_ context.Context, r *run) checks.Verdict {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Error("the chains ran to completion before the binary was started")
		}
		return checks.VerdictOf(checks.AtomByID(slow), 0, "")
	}
	engine.reset()
	engine.withTree(bareTree)
	only := strings.Join(append(atoms.StageIDs(checks.StagePrecommit), slow), ",")
	vs, err := bareModule().vector(soon(t), checks.StagePrecommit, only, "")
	if err != nil || len(vs) != len(atoms.StageIDs(checks.StagePrecommit))+1 {
		t.Fatalf("vector %d long, err %v", len(vs), err)
	}
}

// A lane that ends before the binary answers does not wait for it: each atom is
// a could-not-run that says why.
func TestAVoteTheLaneCannotWaitForIsACouldNotRun(t *testing.T) {
	casting(t, voterBinary)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	castBinary = func(context.Context, *FoundryTools, string, string) (string, error) {
		<-release
		return "[]", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	p := bareModule().pollFor(voterBinary, checks.StagePrecommit, "", checks.AtomsForStage(checks.StagePrecommit))
	v, ok := p.vote(ctx, "fleet:check-yaml")
	if !ok || v.State != 2 || v.Atom != "fleet:check-yaml" || !strings.Contains(v.Reason, "the lane ended before the atoms binary answered") {
		t.Errorf("verdict %+v (%v)", v, ok)
	}
}

// The record is settled: a shadow still waiting for a vote is told none is
// coming, so it does not sit out the grace.
func TestFinishTellsTheShadowNoVoteIsComing(t *testing.T) {
	origRun, origGrace, origOut := shadowRun, shadowGrace, shadowOut
	t.Cleanup(func() { shadowRun, shadowGrace, shadowOut = origRun, origGrace, origOut })
	casting(t, voterBinary)
	shadowRun = func(ctx context.Context, m *FoundryTools, _, _ string) string {
		_, err := m.box.wait(ctx)
		if err == nil {
			return "a vote came"
		}
		return "waited: " + err.Error()
	}
	shadowGrace = 8 * time.Second
	var buf strings.Builder
	shadowOut = func() io.Writer { return &buf }
	h := (&FoundryTools{}).startShadow(soon(t), "", "b")
	start := time.Now()
	h.finish()
	if got := buf.String(); !strings.Contains(got, "waited: the lane settled without casting a vote") || time.Since(start) > 4*time.Second {
		t.Errorf("report %q after %v", got, time.Since(start))
	}
}

// With the chains voting there is no ballot box, and the shadow is the dry one,
// which says the chains voted.
func TestTheShadowOfAChainVotedLaneIsTheDryOne(t *testing.T) {
	origToday, origBinary := shadowToday, shadowBinary
	t.Cleanup(func() { shadowToday, shadowBinary = origToday, origBinary })
	yaml := checks.AtomByID("fleet:check-yaml")
	vec := []checks.Verdict{checks.VerdictOf(yaml, 0, "")}
	shadowToday = func(context.Context, *FoundryTools, string, string, string) ([]checks.Verdict, error) {
		return vec, nil
	}
	shadowBinary = func(context.Context, *FoundryTools, string, string) (string, error) {
		return `[{"atom":"fleet:check-yaml","stage":"precommit","lane":"any","state":0,"result":"pass","logs":null}]`, nil
	}
	got := defaultShadow(soon(t), &FoundryTools{}, "", "b")
	if !strings.Contains(got, "shadow atoms (chains voted): 1 compared") {
		t.Errorf("report %q", got)
	}
}

// THE INTERPRETER STAGE NAMES WHAT STOPPED IT: a pin with no checksum, an asset
// that will not fetch, an archive that will not verify or unpack. A stage that
// ran is a container.
func TestThePythonInterpreterStageNamesWhatStoppedIt(t *testing.T) {
	url := checks.PythonStandaloneURL
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T)
		want  string
	}{
		{"a pin with no checksum", func(t *testing.T) {
			sum := checks.ToolSHA256[url]
			delete(checks.ToolSHA256, url)
			t.Cleanup(func() { checks.ToolSHA256[url] = sum })
		}, "has no checksum"},
		{"an asset that will not fetch", func(*testing.T) { engine.failLeaf(url, "sync", "dial tcp: i/o timeout") }, "could not fetch"},
		{"an archive that will not unpack", func(*testing.T) { engine.failLeaf(`"/opt/python/bin/python3","--version"`, "sync", "bad archive") }, "did not verify and unpack"},
		{"a stage that ran", func(*testing.T) {}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine.reset()
			tc.setup(t)
			ctr, err := pythonInterpreter(soon(t), toolsOS())
			switch {
			case tc.want == "" && (err != nil || ctr == nil):
				t.Errorf("container %v, err %v", ctr, err)
			case tc.want != "" && (err == nil || ctr != nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("container %v, err %v, want one containing %q", ctr, err, tc.want)
			}
		})
	}
}
