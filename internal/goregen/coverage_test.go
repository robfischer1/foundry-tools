package goregen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atoms"
)

// work is the run's work directory in a scene.
func work() string { return filepath.Join(os.Getenv("ATOM_WORK"), "go-regen") }

// breakAt runs mutate the first time a call's line contains text.
func (s *scene) breakAt(text string, mutate func(c atoms.Cmd)) {
	done := false
	s.f.onCall = func(c atoms.Cmd) {
		if !done && strings.Contains(line(c), text) {
			done = true
			mutate(c)
		}
	}
}

func mkdirAll(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestEachStepOfTheBuildAndTheGeneratorNamesItsFailure(t *testing.T) {
	for name, tc := range map[string]struct {
		arrange func(s *scene)
		step    string
		says    string
	}{
		"a guest path that cannot be cleared": {func(s *scene) {
			s.breakAt("git archive", func(atoms.Cmd) { writeFile(t, filepath.Join(work(), "alarm", "guest.wasm", "x"), []byte("x")) })
		}, "build", "could not clear"},
		"a source with no Cargo.toml":        {func(s *scene) { delete(s.f.tree, "Cargo.toml") }, "build", "Cargo.toml would not read"},
		"an artifact that will not copy out": {func(s *scene) { s.f.artifactDir = true }, "build", "would not copy out of the target"},
		"a gen directory that cannot be made": {func(s *scene) {
			s.breakAt("gravity-aeb03bf --version", func(atoms.Cmd) { writeFile(t, filepath.Join(work(), "alarm", "gen"), []byte("a file")) })
		}, "generate", "not a directory"},
		"a guest that cannot be put in the gen directory": {func(s *scene) {
			s.breakAt("gravity-aeb03bf --version", func(atoms.Cmd) { mkdirAll(t, filepath.Join(work(), "alarm", "gen", "guest.wasm")) })
		}, "generate", "is a directory"},
		"a gen directory that vanishes": {func(s *scene) {
			s.f.afterGravity = func() {
				_ = os.RemoveAll(filepath.Join(work(), "alarm", "gen"))
				writeFile(t, filepath.Join(work(), "alarm", "gen"), []byte("a file"))
			}
		}, "generate", "not a directory"},
		"an output that is a directory": {func(s *scene) { s.f.gens["alarm"]["sub/inner.wasm"] = []byte("x") }, "generate", "is a directory"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newScene(t)
			s.add(coreSpec{name: "alarm"})
			tc.arrange(s)
			wantReason(t, s.check("alarm"), 2, "alarm: FAILED at "+tc.step, tc.says)
		})
	}
}

func TestTheContainersWasmStdIsCheckedOnceForManyCores(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm"})
	s.add(coreSpec{name: "field"})
	wantReason(t, s.check("alarm", "field"), 0, "2 core(s) regenerated")
	if s.f.count("rustup target add") != 1 {
		t.Errorf("%d", s.f.count("rustup target add"))
	}
}

func TestSemverReadsOnlyDigits(t *testing.T) {
	for in, want := range map[string]string{"wasm-tools 1.262.0 (x)": "1.262.0", "x 1.x.3": "?", "x 1..3": "?", "x 1.2": "?", "": "?"} {
		if got := semver(in); got != want {
			t.Errorf("semver(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestANewCoreWhoseWorkDirectoryCannotBeMadeCannotLookUpATag(t *testing.T) {
	s := newbie(t)
	blocked := filepath.Join(t.TempDir(), "file")
	writeFile(t, blocked, []byte("x"))
	t.Setenv("ATOM_WORK", blocked)
	wantReason(t, s.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 2, "FAILED at resolve")
}

func TestANewCoresFilesThatCannotBeWrittenAreCannotRun(t *testing.T) {
	s := newbie(t)
	mkdirAll(t, filepath.Join(s.root, "sqlport", "guest.wasm", "keep"))
	wantReason(t, s.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 2, "sqlport: FAILED at write")
}
