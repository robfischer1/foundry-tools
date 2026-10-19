package visuallane

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// layerFiles reads a layer back as the fetch hint does: gunzip, untar.
func layerFiles(t *testing.T, l Layer) map[string]string {
	t.Helper()
	zr, err := gzip.NewReader(strings.NewReader(string(l.Data)))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(raw); l.DiffID != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Errorf("diff id %s is not the uncompressed tar's digest", l.DiffID)
	}
	out := map[string]string{}
	tr := tar.NewReader(strings.NewReader(string(raw)))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		out[h.Name] = string(body)
	}
}

func put(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A layer holds the files under the prefix the artifact keeps them at, and its
// digests are the registry's.
func TestUT_VIS_TarLayerKeepsFilesAtTheirArtifactPath(t *testing.T) {
	root := t.TempDir()
	put(t, root, "g/dusk-diff.png", "diff")
	put(t, root, "report.json", "{}")
	l, err := TarLayer("results/apps/gallery", root, []string{"g/dusk-diff.png", "report.json"})
	if err != nil {
		t.Fatal(err)
	}
	got := layerFiles(t, l)
	want := map[string]string{"results/apps/gallery/g/dusk-diff.png": "diff", "results/apps/gallery/report.json": "{}"}
	if len(got) != 2 || got["results/apps/gallery/g/dusk-diff.png"] != "diff" || got["results/apps/gallery/report.json"] != "{}" {
		t.Errorf("%v, want %v", got, want)
	}
	sum := sha256.Sum256(l.Data)
	if l.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Errorf("digest %s", l.Digest)
	}
}

// The same files make the same layer, so a re-run pushes the blobs it already
// pushed.
func TestUT_VIS_TarLayerIsDeterministic(t *testing.T) {
	root := t.TempDir()
	put(t, root, "a", "1")
	put(t, root, "b", "2")
	one, err := TarLayer("p", root, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := TarLayer("p", root, []string{"a", "b"})
	if err != nil || one.Digest != two.Digest {
		t.Errorf("%s vs %s, %v", one.Digest, two.Digest, err)
	}
}

func TestUT_VIS_TarLayerRefusesAFileThatWillNotRead(t *testing.T) {
	if _, err := TarLayer("p", t.TempDir(), []string{"missing"}); err == nil {
		t.Fatal("a file that is not there made a layer")
	}
}

// Files lists a directory's regular files, relative, in order; a directory that
// is not there is no files, not an error (a suite that wrote no results).
func TestUT_VIS_FilesListsRegularFilesInOrder(t *testing.T) {
	root := t.TempDir()
	put(t, root, "b/2.png", "")
	put(t, root, "a/1.png", "")
	put(t, root, "z.json", "")
	put(t, root, "node_modules/pkg/x.png", "")
	got, err := Files(root)
	if err != nil || !slices.Equal(got, []string{"a/1.png", "b/2.png", "z.json"}) {
		t.Errorf("%v %v", got, err)
	}
	if got, err := Files(filepath.Join(root, "nope")); err != nil || len(got) != 0 {
		t.Errorf("an absent directory: %v %v", got, err)
	}
	// A path that runs through a file cannot be walked, and that is an error.
	if _, err := Files(filepath.Join(root, "z.json", "below")); err == nil {
		t.Error("a root under a file listed")
	}
}

// Baselines are the snapshots the update run wrote, wherever they sit in the
// suite, and never what node_modules holds.
func TestUT_VIS_SnapshotsKeepsOnlyTheSnapshots(t *testing.T) {
	got := Snapshots([]string{
		"gallery.spec.ts-snapshots/dusk-linux.png",
		"tests/a.spec.ts-snapshots/x/y.png",
		"gallery.spec.ts",
		"node_modules/pkg/a.spec.ts-snapshots/z.png",
		"package.json",
	})
	want := []string{"gallery.spec.ts-snapshots/dusk-linux.png", "tests/a.spec.ts-snapshots/x/y.png"}
	if !slices.Equal(got, want) {
		t.Errorf("%v, want %v", got, want)
	}
}
