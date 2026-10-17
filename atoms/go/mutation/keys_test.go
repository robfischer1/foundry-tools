package gomutation

import (
	"path/filepath"
	"testing"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/unitkey"
)

// THE ENGINE ID IS PINNED. Any input to it moving — a tool, an image, a flag, the
// epoch — moves this value, and every stored grading misses: correct, and a
// decision, so it fails here until someone writes the new value down.
func TestTheGoEngineIsPinned(t *testing.T) {
	const want = "9406164247ebf8dbab500b6221abf8eb84cf1d01899e5e1bd12d4ab93af05931"
	if got := goMutationEngine(); got != want {
		t.Fatalf("goMutationEngine() = %s, want %s — an input moved; every stored Go grading now misses. If that is meant, pin the new value", got, want)
	}
}

// The Go lane keys the units its diff touched and hands back one grading per
// unit, built from the score it settled on.
func TestGoMutationGradesEachUnitItTouched(t *testing.T) {
	p := newPull(t, ".")
	p.first(rule{"ls-tree", "100644 blob b1\tgo.mod\x00100644 blob b2\ta.go\x00100644 blob b3\tinternal/x/x.go\x00", 0, ""})
	p.first(rule{"-deps -test", "m\t" + p.in.Root + "\tm/internal/x\nm/internal/x\t" + filepath.Join(p.in.Root, "internal/x") + "\t\n", 0, ""})
	v := p.run()
	if v.State != 0 || len(v.Gradings) != 1 {
		t.Fatalf("%s %+v", said(v), v.Gradings)
	}
	g := v.Gradings[0]
	entries := checks.ParseLsTree("100644 blob b1\tgo.mod\x00100644 blob b2\ta.go\x00100644 blob b3\tinternal/x/x.go\x00")
	if g.Unit != "." || g.Lang != "go" || g.Engine != goMutationEngine() || !g.Reusable || g.Counts.Killed != 1 ||
		g.Hash != unitkey.Hash(unitkey.Go, entries, []string{".", "internal/x"}) ||
		g.Ranges != unitkey.Ranges(unitkey.Go, entries, ".", map[string][]unitkey.Range{"a.go": {{Start: 1, End: 1}}}) {
		t.Fatalf("grading = %+v", g)
	}
	// the three reads, in the module, of the repository
	for _, w := range []string{
		"git -C " + p.in.Root + " ls-tree -r -z --full-tree HEAD",
		"git -C " + p.in.Root + " diff --unified=0 --no-color --no-ext-diff " + sinceSha + " HEAD",
		"go list -e -deps -test -f " + checks.GoListDepsFormat + " ./...",
	} {
		if !p.ran(w) {
			t.Errorf("the key read %q never ran: %v", w, p.runs)
		}
	}
}

// A key read that fails never touches the verdict: the units are graded and
// stored unkeyable.
func TestAKeyReadThatFailsLeavesTheVerdictAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		rule rule
		why  string
	}{
		{"go list fails", rule{"-deps -test", "", 1, ""}, "go list exited 1"},
		{"git cannot read the tree", rule{"ls-tree", "fatal", 128, ""}, "git ls-tree exited 128"},
		{"git never started", rule{"ls-tree", "git: not found", -1, ""}, "git ls-tree never ran: git: not found"},
		{"the key diff fails", rule{"git -C", "boom", 1, ""}, "git diff exited 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPull(t, ".")
			p.first(tc.rule)
			v := p.run()
			if v.State != 0 {
				t.Fatalf("a failed key read changed the verdict: %s", said(v))
			}
			if len(v.Gradings) != 1 || v.Gradings[0].Reusable || v.Gradings[0].WhyNot != checks.WhyNotUnkeyable || v.Gradings[0].Hash != "" {
				t.Errorf("the unit must be stored unkeyable (%s): %+v", tc.why, v.Gradings)
			}
		})
	}
}
