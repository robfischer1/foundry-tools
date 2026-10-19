package bundlekit

import (
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/checks"
)

type atomFn func(context.Context, Run) *Fault

// Every program an atom runs can fail to start, and then the lane could not run.
func TestEveryProgramThatNeverRunsIsTheLanesFault(t *testing.T) {
	derive := map[string]string{bundlelane.DecisionsDerive: "package t\n", "fleet/derive.rego": "package fleet\n"}
	for _, tc := range []struct {
		name  string
		atom  atomFn
		files map[string]string
		dead  string
		want  string
	}{
		{"policy: opa build", Policy, nil, "build", "opa build never ran"},
		{"policy: the derive", Policy, derive, "data.authz_v2_table.decisions", "the decision table's derive never ran"},
		{"policy: the probe", Policy, nil, "data.authz.callable.allowed", "the session-principal probe never ran"},
		{"policy: the diff", Policy, nil, "diff", "the change set could not be read"},
		{"fleet: the roster", Fleet, nil, "--format raw data", "evaluating the built roster never ran"},
		{"fleet: the composition build", Fleet, nil, "--revision gate", "opa build never ran"},
		{"fleet: the composition probe", Fleet, nil, "data.admission.deny", "the composition probe never ran"},
		{"fleet: the diff", Fleet, nil, "diff", "the change set could not be read"},
		{"orbits: the diff", Orbits, nil, "diff", "the change set could not be read"},
	} {
		w := newWorld(t, tc.files)
		if tc.files != nil {
			w.table = derived
		}
		w.dead = []string{tc.dead}
		expect(t, tc.atom(context.Background(), w.run()), false, tc.want)
	}
}

func TestAFailingDiffIsTheLanesFault(t *testing.T) {
	for _, atom := range []atomFn{Policy, Fleet, Orbits} {
		w := newWorld(t, nil)
		w.diffCode = 128
		expect(t, atom(context.Background(), w.run()), false, "the change set could not be read (exit 128)")
	}
}

// The commit cannot be read for any of the three.
func TestNoCommitIsTheLanesFault(t *testing.T) {
	for _, atom := range []atomFn{Fleet, Orbits} {
		w := newWorld(t, nil)
		t.Setenv("ATOM_SHA", "")
		w.gitDead = true
		expect(t, atom(context.Background(), w.run()), false, "git rev-parse HEAD never ran")
	}
}

func TestFleetBuildsFromADeriveTree(t *testing.T) {
	w := newWorld(t, map[string]string{checks.FleetDerive: "package fleet\n"})
	if f := Fleet(context.Background(), w.run()); f != nil {
		t.Fatal(f)
	}
}

func TestFleetBuildThatNeverRuns(t *testing.T) {
	w := newWorld(t, nil)
	w.dead = []string{"-b " + w.stage}
	expect(t, Fleet(context.Background(), w.run()), false, "opa build never ran")
}

// What the lane reads from a tree and a flux checkout can be present and unreadable.
func TestUnreadableInputsAreTheLanesFault(t *testing.T) {
	broken := func(root, rel string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.Remove(p)
		if err := os.Symlink("/nonexistent/x", p); err != nil {
			t.Fatal(err)
		}
	}
	w := newWorld(t, nil)
	broken(w.in.Root, "fleet/repos/chaos.json")
	expect(t, Fleet(context.Background(), w.run()), false, "could not be read")
	w = newWorld(t, nil)
	broken(w.flux, "data/bad.yaml")
	expect(t, Fleet(context.Background(), w.run()), false, "could not be read")
	w = newWorld(t, nil)
	broken(w.flux, "blades/bad.yaml")
	expect(t, Fleet(context.Background(), w.run()), false, "could not be read")
	w = newWorld(t, nil)
	broken(w.in.Root, "fleet/repos/chaos.json")
	expect(t, Orbits(context.Background(), w.run()), false, "could not be read")
}

// A build that left no file, or a file of the wrong shape, is the lane's fault or a finding.
func TestBuiltArtifactsThatAreNotWhatTheyShouldBe(t *testing.T) {
	for _, tc := range []struct {
		name     string
		atom     atomFn
		edit     func(*world)
		findings bool
		want     string
	}{
		{"policy: nothing written", Policy, func(w *world) { w.noOutput = []string{"/bundle.tar.gz"} }, false, "bundle.tar.gz"},
		{"policy: no data.json", Policy, func(w *world) { w.noData = true }, true, "carries no /data.json"},
		{"fleet: nothing written", Fleet, func(w *world) { w.noOutput = []string{"fleet-bundle-signed"} }, false, "fleet-bundle-signed.tar.gz"},
		{"fleet: no manifest", Fleet, func(w *world) { w.noManifest = true }, true, "carries no /.manifest"},
	} {
		w := newWorld(t, nil)
		tc.edit(w)
		expect(t, tc.atom(context.Background(), w.run()), tc.findings, tc.want)
	}
}

func TestACopyKeepsFilesAndLeavesLinksOut(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(src, "a"), []byte("x"), 0o644)
	os.Symlink("a", filepath.Join(src, "link"))
	if err := copyTree(src, dst, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "a")); err != nil {
		t.Error(err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "link")); err == nil {
		t.Error("a link was copied")
	}
	// A file that cannot be written stops the copy.
	defer failWrite(t, filepath.Join(dst, "b"))()
	os.WriteFile(filepath.Join(src, "b"), []byte("y"), 0o644)
	if err := copyTree(src, dst, nil); err == nil {
		t.Error("a failed write was not an error")
	}
}

func TestATarballThatBreaksMidStreamIsAnError(t *testing.T) {
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	zw.Write(bytes.Repeat([]byte("not a tar header "), 40))
	zw.Close()
	if _, err := Members(b.Bytes()); err == nil || !strings.Contains(err.Error(), "not a tar") {
		t.Errorf("%v", err)
	}
}
