package orbitkit

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
)

func TestOrbitHelpers(t *testing.T) {
	for in, want := range map[string]string{"http://door/rob/a.git": "a", "git@h:rob/blade-runner": "blade-runner", "": "."} {
		if got := StarOf(in); got != want {
			t.Errorf("StarOf(%q) = %q, want %q", in, got, want)
		}
	}
	if StarSet(nil) != nil || len(StarSet(map[string]string{"a": "x"})) != 1 {
		t.Error("an empty roster is nil (the two-part file-name rule alone); a roster is its set")
	}
	tr := atomtest.TreeIn(t, map[string]string{
		"fleet/repos/a.json": atomtest.StarData("a"),
		"fleet/repos/b.json": "not json at all",
	}).Tree()
	got, err := Roster(tr)
	if err != nil || !reflect.DeepEqual(got, map[string]string{"a": "a", "b": ""}) {
		t.Errorf("roster %v, %v: a shard that is not JSON still names its star, with no prefix", got, err)
	}
	broken := atomtest.TreeIn(t, map[string]string{"fleet/repos/a.json/x": "x"}).Tree()
	if _, err := Roster(broken); err == nil || !strings.Contains(err.Error(), "fleet/repos/a.json/ could not be read") {
		t.Errorf("a shard that will not read: %v", err)
	}
	if _, err := FilesIn(atomtest.TreeIn(t, map[string]string{"x/y/z": "z"}).Tree(), "x/*"); err != nil {
		t.Errorf("a directory is named, not read: %v", err)
	}
	dir := atomtest.TreeIn(t, map[string]string{"orbits/a.toml": "x", "orbits/sub/f": "f"}).Tree()
	byName, err := FilesByName(dir, "orbits/*")
	if err != nil || byName["a.toml"] == nil || byName["sub"] != nil || len(byName) != 2 {
		t.Errorf("filesByName %v, %v: a directory is a nil entry", byName, err)
	}
	if _, err := FilesIn(atomtest.TreeIn(t, map[string]string{"orbits/a.toml/f": "x"}).Tree(), "orbits/*/f"); err != nil {
		t.Errorf("a readable file: %v", err)
	}
	tmp := t.TempDir()
	link := filepath.Join(tmp, "orbits")
	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tmp, "gone"), filepath.Join(link, "x.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := FilesIn(atoms.Input{Root: tmp}.Tree(), "orbits/*.toml"); err == nil || !strings.Contains(err.Error(), "orbits/*.toml could not be read") {
		t.Errorf("a file that will not read names its pattern: %v", err)
	}
}

// EVERY ENTRY IS READ, A DIRECTORY ANYWHERE AMONG THEM INCLUDED: one that sorts
// first is named and the files after it are still read.
func TestFilesByNameNamesADirectoryAndReadsWhatFollows(t *testing.T) {
	in := atomtest.TreeIn(t, map[string]string{
		"prime/orbits/a/x":               "",
		"prime/orbits/themis.orbit.toml": "sidecar\n",
	})
	got, err := FilesByName(in.Tree(), "prime/orbits/*")
	if err != nil {
		t.Fatal(err)
	}
	if raw, ok := got["a"]; !ok || raw != nil {
		t.Errorf("the directory must be named with nil bytes: %v", got)
	}
	if string(got["themis.orbit.toml"]) != "sidecar\n" {
		t.Errorf("the file after the directory was not read: %v", got)
	}
}

func TestDiesWithoutACheckoutIsAnError(t *testing.T) {
	if _, err := Dies(atoms.Input{}); err == nil {
		t.Error("no checkout, no contracts")
	}
	if _, err := Dies(atoms.Input{Dies: t.TempDir()}); err != nil {
		t.Error(err)
	}
}

func TestHasContractNeedsATomlFile(t *testing.T) {
	if HasContract(map[string][]byte{"README.md": nil}) || !HasContract(map[string][]byte{"a-b.toml": nil}) {
		t.Error("a contract is a .toml")
	}
}
