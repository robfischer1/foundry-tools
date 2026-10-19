package bundlekit

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"dagger/foundry-tools/internal/bundlelane"
)

func TestOrbitsComposeBothDiesAndLeaveTheirFiles(t *testing.T) {
	w := newWorld(t, map[string]string{"orbits/README.md": "not a contract"})
	if f := Orbits(context.Background(), w.run()); f != nil {
		t.Fatal(f)
	}
	c, err := ReadMeta("contracts")
	if err != nil {
		t.Fatal(err)
	}
	o, err := ReadMeta("orbits")
	if err != nil {
		t.Fatal(err)
	}
	if c.Die != bundlelane.ContractsDie || c.Kind != "tree" || c.Dir != ContractsDir || len(c.Files) != 1 || c.Files[0] != "urania-themis.toml" || !c.Publish || c.Pin != "g0123456" {
		t.Errorf("contracts meta %+v", c)
	}
	if o.Die != bundlelane.OrbitsDie || o.Dir != OrbitsDir || len(o.Files) == 0 {
		t.Errorf("orbits meta %+v", o)
	}
	if got := w.read(ContractsDir + "/urania-themis.toml"); got != contractBody {
		t.Errorf("a contract is byte for byte: %q", got)
	}
	for _, f := range o.Files {
		if _, err := os.Stat(filepath.Join(w.out, OrbitsDir, f)); err != nil {
			t.Error(err)
		}
	}
	// Re-running leaves exactly the dies' files: nothing of an earlier run stays.
	os.WriteFile(filepath.Join(w.out, ContractsDir, "stale.toml"), []byte("x"), 0o644)
	if f := Orbits(context.Background(), w.run()); f != nil {
		t.Fatal(f)
	}
	if _, err := os.Stat(filepath.Join(w.out, ContractsDir, "stale.toml")); err == nil {
		t.Error("a stale file stayed")
	}
}

func TestOrbitsPublishOnlyWhenOrbitsMoved(t *testing.T) {
	for name, tc := range map[string]struct {
		diff    string
		parent  int
		publish bool
	}{"orbits moved": {"orbits/a-b.toml\n", 0, true}, "policy only": {"policy/x.rego\n", 0, false}, "unknown tip": {"", 1, true}} {
		w := newWorld(t, nil)
		w.diff, w.parent = tc.diff, tc.parent
		if f := Orbits(context.Background(), w.run()); f != nil {
			t.Fatalf("%s: %v", name, f)
		}
		if m, _ := ReadMeta("contracts"); m.Publish != tc.publish {
			t.Errorf("%s: publish = %v", name, m.Publish)
		}
	}
}

func TestOrbitsRefusals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		files    map[string]string
		edit     func(*world)
		findings bool
		needle   string
	}{
		{"no contract", map[string]string{"orbits/urania-themis.toml": ""}, nil, true, "does not compose"},
		{"a contract that does not parse", map[string]string{"orbits/urania-themis.toml": "version = 1"}, nil, true, "does not compose"},
		{"git cannot read history", nil, func(w *world) { w.gitDead = true }, false, "the history could not be read"},
		{"a short commit", nil, func(w *world) { t.Setenv("ATOM_SHA", "abc") }, false, "too short for a pin"},
	} {
		w := newWorld(t, tc.files)
		if tc.edit != nil {
			tc.edit(w)
		}
		expect(t, Orbits(context.Background(), w.run()), tc.findings, tc.needle)
	}
}

func TestOrbitsFilesystemFaults(t *testing.T) {
	w := newWorld(t, nil)
	bad := filepath.Join(t.TempDir(), "file")
	os.WriteFile(bad, []byte("x"), 0o644)
	t.Setenv(OutEnv, filepath.Join(bad, "sub"))
	expect(t, Orbits(context.Background(), w.run()), false, "could not be written")
	// A roster entry that cannot be read is the lane's.
	w = newWorld(t, nil)
	if got, f := roster(func(string) []string { return []string{"fleet/repos/x.json"} },
		func(string) (string, error) { return "", os.ErrPermission }); got != nil || f == nil {
		t.Errorf("%v %v", got, f)
	}
	// A shard that is not JSON still names a repo.
	got, f := roster(func(string) []string { return []string{"fleet/repos/x.json"} }, func(string) (string, error) { return "{", nil })
	if f != nil || len(got) != 1 {
		t.Errorf("%v %v", got, f)
	}
	if starSet(nil) != nil || !starSet(got)["x"] {
		t.Error("starSet")
	}
}

func TestOrbitsWriteFaults(t *testing.T) {
	w := newWorld(t, nil)
	failWrite(t, "contracts"+MetaSuffix)
	expect(t, Orbits(context.Background(), w.run()), false, "contracts.meta.json")
}
