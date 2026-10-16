package atomkit

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atoms"
)

func TestUT_211_CollectTreeOfAChange(t *testing.T) {
	dir := newRepo(t)
	fx1(t, dir)
	p := commitAll(t, dir, "p")
	put(t, dir, "notes.txt", "<<<<<<< HEAD\n")
	commitAll(t, dir, "h")
	got := CollectTree(context.Background(), dir, p, "http://door.test/rob/nyx.git", "")
	want := Tree{Version: 1, Root: dir, Files: []string{"go.mod", "main.go", "notes.txt"}, Committable: []string{"go.mod", "main.go", "notes.txt"},
		Tracked: []string{"go.mod", "main.go", "notes.txt"}, Changed: []string{"notes.txt"}, Base: p, Origin: "http://door.test/rob/nyx.git"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if again := CollectTree(context.Background(), dir, p, got.Origin, "gravity"); again.Fork != "gravity" {
		t.Fatalf("UT-215: fork %q", again.Fork)
	}
}

func TestUT_212_TheFleetExcludeIsNotARepositorys(t *testing.T) {
	dir := newRepo(t)
	all := []string{".claude/s.md", ".furnace/f", ".github/w.yml", "_x/a.go", "a.melt", "go.mod", "node_modules/m/i.js", "testdata/t.go", "vendor/v/a.go"}
	for _, f := range all {
		put(t, dir, f, "x\n")
	}
	commitAll(t, dir, "c")
	got := CollectTree(context.Background(), dir, "", "", "")
	if want := []string{".github/w.yml", "_x/a.go", "go.mod", "testdata/t.go"}; !slices.Equal(got.Files, want) {
		t.Fatalf("files %v", got.Files)
	}
	if !slices.Equal(got.Committable, all) {
		t.Fatalf("committable %v", got.Committable)
	}
}

func TestUT_213_CommittableIsTrackedPlusUntrackedUnignored(t *testing.T) {
	dir := newRepo(t)
	put(t, dir, ".gitignore", "*.log\n")
	put(t, dir, "go.mod", "module x\n")
	commitAll(t, dir, "c")
	put(t, dir, "new.txt", "n\n")
	put(t, dir, "out.log", "o\n")
	got := CollectTree(context.Background(), dir, "", "", "")
	if want := []string{".gitignore", "go.mod", "new.txt"}; !slices.Equal(got.Committable, want) {
		t.Fatalf("committable %v", got.Committable)
	}
	if want := []string{".gitignore", "go.mod"}; !slices.Equal(got.Tracked, want) {
		t.Fatalf("tracked %v", got.Tracked)
	}
}

func TestUT_214_APlainDirectoryIsNoTreeButStillEncodesLists(t *testing.T) {
	got := CollectTree(context.Background(), t.TempDir(), "", "", "")
	if got.TrackedErr == "" || got.FilesErr == "" || got.ChangedErr == "" {
		t.Fatalf("errors %+v", got)
	}
	b, err := Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"files":[]`, `"committable":[]`, `"tracked":[]`, `"changed":[]`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("%s missing from %s", key, b)
		}
	}
}

// TestTheTreeIsTheInputBuildersFacts: the facts CollectTree writes are the ones
// internal/atoms' Input carries, field for field.
func TestTheTreeIsTheInputBuildersFacts(t *testing.T) {
	dir := fx3(t)
	in := atoms.Collect(context.Background(), dir, "", "http://door.test/x.git", time.Now())
	got := CollectTree(context.Background(), dir, "", "http://door.test/x.git", "")
	if !slices.Equal(got.Files, in.Files) || !slices.Equal(got.Committable, in.Committable) || !slices.Equal(got.Tracked, in.Tracked) || !slices.Equal(got.Changed, in.Changed) {
		t.Fatalf("%+v vs %+v", got, in)
	}
	if got.Origin != in.Origin || got.Base != in.Base || got.Root != in.Root {
		t.Fatalf("%+v vs %+v", got, in)
	}
}

// TestTreeAndInRoundTrip: tree@1 -> Input -> tree@1 loses nothing.
func TestTreeAndInRoundTrip(t *testing.T) {
	tf := TF1()
	tf.Fork, tf.Base, tf.FilesErr, tf.TrackedErr, tf.ChangedErr = "gravity", "b", "f", "t", "c"
	if got := treeOf(inOf(tf), tf.Fork); !reflect.DeepEqual(got, tf) {
		t.Fatalf("got %+v", got)
	}
	var raw map[string]any
	b, _ := Marshal(treeOf(In{}, ""))
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"files", "committable", "tracked", "changed"} {
		if l, ok := raw[k].([]any); !ok || len(l) != 0 {
			t.Errorf("%s = %v, want []", k, raw[k])
		}
	}
}
