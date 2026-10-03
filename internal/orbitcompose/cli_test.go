package orbitcompose

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPlanWritesOnlyWhatDiffersAndRemovesStaleSidecars(t *testing.T) {
	kust := []byte(OwnerMark + "\nold\n")
	want := map[string][]byte{
		"chaos.orbit.toml":  []byte("same"),
		"urania.orbit.toml": []byte("new"),
		KustomizationFile:   []byte(OwnerMark + "\nnew\n"),
	}
	have := map[string][]byte{
		"chaos.orbit.toml": []byte("same"),
		"nyx.orbit.toml":   []byte("gone"),
		KustomizationFile:  kust,
	}
	c, err := Plan(want, have)
	if err != nil {
		t.Fatal(err)
	}
	var wrote []string
	for n := range c.Write {
		wrote = append(wrote, n)
	}
	slices.Sort(wrote)
	if !slices.Equal(wrote, []string{KustomizationFile, "urania.orbit.toml"}) || !slices.Equal(c.Remove, []string{"nyx.orbit.toml"}) {
		t.Errorf("write %v remove %v", wrote, c.Remove)
	}
	if c.Empty() {
		t.Error("a change reads empty")
	}
}

func TestPlanRemovesInOrder(t *testing.T) {
	c, err := Plan(map[string][]byte{}, map[string][]byte{"b.orbit.toml": nil, "a.orbit.toml": nil, "c.orbit.toml": nil})
	if err != nil || !slices.Equal(c.Remove, []string{"a.orbit.toml", "b.orbit.toml", "c.orbit.toml"}) {
		t.Errorf("remove %v err %v", c.Remove, err)
	}
	if c.Empty() {
		t.Error("a remove-only change reads empty")
	}
}

func TestPlanOfTheSameSetIsEmpty(t *testing.T) {
	set := map[string][]byte{"chaos.orbit.toml": []byte("x")}
	c, err := Plan(set, set)
	if err != nil || !c.Empty() {
		t.Errorf("change %+v err %v", c, err)
	}
}

func TestPlanRefusesAFileThePersonWrote(t *testing.T) {
	for _, have := range []map[string][]byte{
		{"README.md": []byte("hi")},
		{KustomizationFile: []byte("# mine\n")},
	} {
		if _, err := Plan(map[string][]byte{}, have); err == nil || !strings.Contains(err.Error(), "not the composer's") {
			t.Errorf("%v: err %v", have, err)
		}
	}
}

func writeContracts(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for n, body := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func run(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := Main(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestMainComposesThenIsANoOp(t *testing.T) {
	contracts := writeContracts(t, map[string]string{
		"urania-themis.toml": contractBody,
		"chaos-urania.toml":  strings.Replace(contractBody, `"neighbors"`, `"shapes"`, 1),
		"README.md":          "not a contract",
	})
	out := filepath.Join(t.TempDir(), "prime", "orbits")

	code, stdout, stderr := run("-contracts", contracts, "-out", out)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	if stdout != "write  chaos.orbit.toml\nwrite  kustomization.yaml\nwrite  themis.orbit.toml\nwrite  urania.orbit.toml\n" {
		t.Errorf("stdout %q", stdout)
	}
	raw, err := os.ReadFile(filepath.Join(out, "themis.orbit.toml"))
	if err != nil || !strings.Contains(string(raw), "[[consumes]]\nfrom = \"urania\"\ncontract = \"urania-themis\"") {
		t.Errorf("themis sidecar %q %v", raw, err)
	}
	kust, _ := os.ReadFile(filepath.Join(out, KustomizationFile))
	if !strings.Contains(string(kust), "namespace: prime\n") || !strings.Contains(string(kust), "orbit-themis") {
		t.Errorf("kustomization %s", kust)
	}

	code, stdout, _ = run("-contracts", contracts, "-out", out)
	if code != 0 || stdout != "orbitcompose: up to date\n" {
		t.Errorf("re-run: exit %d stdout %q", code, stdout)
	}
	if code, _, _ := run("-check", "-contracts", contracts, "-out", out); code != 0 {
		t.Errorf("-check on the composed set: exit %d", code)
	}
}

func TestMainCheckFindsDriftAndWritesNothing(t *testing.T) {
	contracts := writeContracts(t, map[string]string{"urania-themis.toml": contractBody})
	out := t.TempDir()
	if code, _, _ := run("-contracts", contracts, "-out", out, "-namespace", "elsewhere"); code != 0 {
		t.Fatal("first compose failed")
	}
	kust, _ := os.ReadFile(filepath.Join(out, KustomizationFile))
	if !strings.Contains(string(kust), "namespace: elsewhere\n") {
		t.Errorf("namespace flag not honoured: %s", kust)
	}
	if err := os.WriteFile(filepath.Join(contracts, "urania-themis.toml"), []byte(strings.Replace(contractBody, `"neighbors"`, `"shapes"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(out, "urania.orbit.toml"))
	code, stdout, _ := run("-check", "-contracts", contracts, "-out", out, "-namespace", "elsewhere")
	if code != 1 || stdout != "write  themis.orbit.toml\nwrite  urania.orbit.toml\n" {
		t.Errorf("exit %d stdout %q", code, stdout)
	}
	after, _ := os.ReadFile(filepath.Join(out, "urania.orbit.toml"))
	if !bytes.Equal(before, after) {
		t.Error("-check wrote")
	}
}

func TestMainRemovesAStarWhoseLastContractWent(t *testing.T) {
	contracts := writeContracts(t, map[string]string{"urania-themis.toml": contractBody})
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "nyx.orbit.toml"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := run("-contracts", contracts, "-out", out)
	if code != 0 || !strings.Contains(stdout, "remove nyx.orbit.toml") {
		t.Errorf("exit %d stdout %q", code, stdout)
	}
	if _, err := os.Stat(filepath.Join(out, "nyx.orbit.toml")); !os.IsNotExist(err) {
		t.Errorf("nyx's sidecar survived: %v", err)
	}
}

func TestMainRefusesBadUsage(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"-contracts", "x"},
		{"-out", "y"},
		{"-contracts", "x", "-out", "y", "extra"},
		{"-nope"},
	} {
		if code, _, stderr := run(args...); code != 2 || !strings.Contains(stderr, "usage: orbitcompose") {
			t.Errorf("%v: exit %d stderr %q", args, code, stderr)
		}
	}
	// flag's own complaint goes to the composer's error stream too.
	if _, _, stderr := run("-nope"); !strings.Contains(stderr, "flag provided but not defined: -nope") {
		t.Errorf("stderr %q", stderr)
	}
}

func TestMainCannotComposeIsTwo(t *testing.T) {
	bad := writeContracts(t, map[string]string{"urania-themis.toml": "version = 1"})
	empty := t.TempDir()
	foreign := t.TempDir()
	if err := os.WriteFile(filepath.Join(foreign, "README.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	good := writeContracts(t, map[string]string{"urania-themis.toml": contractBody})
	for name, args := range map[string][]string{
		"bad contract":  {"-contracts", bad, "-out", t.TempDir()},
		"no contracts":  {"-contracts", empty, "-out", t.TempDir()},
		"foreign file":  {"-contracts", good, "-out", foreign},
		"foreign check": {"-check", "-contracts", good, "-out", foreign},
	} {
		if code, _, stderr := run(args...); code != 2 || !strings.HasPrefix(stderr, "orbitcompose: ") {
			t.Errorf("%s: exit %d stderr %q", name, code, stderr)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(foreign, "README.md")); string(raw) != "mine" {
		t.Error("a person's file was touched")
	}
}

func TestMainApplyFailureIsTwo(t *testing.T) {
	good := writeContracts(t, map[string]string{"urania-themis.toml": contractBody})
	file := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// out under a regular file: ReadDir answers an error that is not "absent",
	// and -check (which writes nothing) still cannot compare against it.
	for _, args := range [][]string{{}, {"-check"}} {
		args = append(args, "-contracts", good, "-out", filepath.Join(file, "orbits"))
		if code, _, stderr := run(args...); code != 2 || !strings.Contains(stderr, "not a directory") {
			t.Errorf("%v: exit %d stderr %q", args, code, stderr)
		}
	}
}

func TestReadDirRefusesASubdirectoryAndReadsAbsentAsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDir(dir); err == nil || !strings.Contains(err.Error(), "holds only files") {
		t.Errorf("err %v", err)
	}
	have, err := ReadDir(filepath.Join(dir, "absent"))
	if err != nil || have == nil || len(have) != 0 {
		t.Errorf("have %v err %v", have, err)
	}
}

func TestReadContractsIsSortedByFile(t *testing.T) {
	dir := writeContracts(t, map[string]string{"urania-themis.toml": contractBody, "chaos-themis.toml": contractBody})
	cs, err := ReadContracts(dir, nil)
	if err != nil || len(cs) != 2 || cs[0].Name != "chaos-themis" || cs[1].Name != "urania-themis" {
		t.Errorf("%+v %v", cs, err)
	}
}

func TestApplyWritesAndRemoves(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new")
	if err := Apply(dir, Change{Write: map[string][]byte{"a.orbit.toml": []byte("A")}}); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "a.orbit.toml")); string(raw) != "A" {
		t.Errorf("wrote %q", raw)
	}
	if err := Apply(dir, Change{Remove: []string{"a.orbit.toml"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.orbit.toml")); !os.IsNotExist(err) {
		t.Errorf("not removed: %v", err)
	}
	if err := Apply(dir, Change{Remove: []string{"absent.orbit.toml"}}); err == nil {
		t.Error("removing an absent file answered nil")
	}
}

func TestMainWriteFailureIsTwo(t *testing.T) {
	good := writeContracts(t, map[string]string{"urania-themis.toml": contractBody})
	apply = func(string, Change) error { return os.ErrPermission }
	t.Cleanup(func() { apply = Apply })
	if code, _, stderr := run("-contracts", good, "-out", t.TempDir()); code != 2 || !strings.Contains(stderr, "permission denied") {
		t.Errorf("exit %d stderr %q", code, stderr)
	}
}

func TestReadContractsRefusesWhatItCannotRead(t *testing.T) {
	if _, err := ReadContracts(filepath.Join(t.TempDir(), "absent"), nil); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("an absent directory: err %v", err)
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "chaos-themis.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadContracts(dir, nil); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("a directory named like a contract: err %v", err)
	}
}

func TestReadDirRefusesAnUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "a.orbit.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDir(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a dangling link: err %v", err)
	}
	file := filepath.Join(dir, "plain")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDir(filepath.Join(file, "orbits")); err == nil {
		t.Error("a directory under a file read as absent")
	}
}

func TestApplyFailuresAnswerTheError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Apply(filepath.Join(file, "orbits"), Change{}); err == nil {
		t.Error("a directory under a file was made")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub.orbit.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Apply(dir, Change{Write: map[string][]byte{"sub.orbit.toml": []byte("x")}}); err == nil {
		t.Error("writing over a directory answered nil")
	}
}

// ONE BAD FILE REFUSES THE WRITE, NAMED, AND WRITES NOTHING.
func TestMainWithAnUnreadableContractWritesNothingAndNamesEachFile(t *testing.T) {
	contracts := writeContracts(t, map[string]string{
		"urania-themis.toml": contractBody, "broken-themis.toml": "version = 1", "also-bad.toml": "x",
	})
	out := filepath.Join(t.TempDir(), "prime", "orbits")
	for _, args := range [][]string{{"-contracts", contracts, "-out", out}, {"-contracts", contracts, "-out", out, "-check"}} {
		code, stdout, stderr := run(args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "broken-themis.toml") || !strings.Contains(stderr, "also-bad.toml") {
			t.Errorf("%v: exit %d stdout %q stderr %q", args, code, stdout, stderr)
		}
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("the output directory was created: %v", err)
	}
}

// THE ROSTER BESIDE THE CONTRACTS (foundry-dies' fleet/stars) LETS A
// HYPHENATED STAR COMPOSE, or a named one does.
func TestMainReadsTheRosterBesideTheContracts(t *testing.T) {
	root := t.TempDir()
	for _, star := range []string{"blade-runner", "poseidon"} {
		if err := os.MkdirAll(filepath.Join(root, "fleet", "stars", star), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "orbits"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "orbits", "blade-runner-poseidon.toml"), []byte(contractBody), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "orbits")
	code, stdout, stderr := run("-contracts", filepath.Join(root, "orbits"), "-out", out)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "write  blade-runner.orbit.toml") || !strings.Contains(stdout, "write  poseidon.orbit.toml") {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	plain := writeContracts(t, map[string]string{"urania-themis.toml": contractBody})
	fresh := filepath.Join(t.TempDir(), "orbits")
	if code, stdout, stderr := run("-contracts", plain, "-stars", filepath.Join(root, "absent"), "-out", fresh); code != 2 || stdout != "" || !strings.Contains(stderr, "absent") {
		t.Errorf("a named roster that is not there: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Errorf("a refused run wrote: %v", err)
	}
}

func TestRosterForIsTheNamedDirectoryElseTheOneBesideElseNone(t *testing.T) {
	root := t.TempDir()
	contracts := filepath.Join(root, "orbits")
	if stars, err := RosterFor(contracts, ""); stars != nil || err != nil {
		t.Errorf("no roster beside: %v %v", stars, err)
	}
	named := filepath.Join(root, "named")
	if err := os.MkdirAll(filepath.Join(named, "zeta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(named, "README"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	stars, err := RosterFor(contracts, named)
	if err != nil || len(stars) != 1 || !stars["zeta"] {
		t.Errorf("named: %v %v (a file is not a star)", stars, err)
	}
	beside := filepath.Join(root, "fleet", "stars", "alpha")
	if err := os.MkdirAll(beside, 0o755); err != nil {
		t.Fatal(err)
	}
	if stars, err := RosterFor(contracts, ""); err != nil || !stars["alpha"] {
		t.Errorf("beside: %v %v", stars, err)
	}
	if _, err := RosterFor(contracts, filepath.Join(root, "absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("named but absent: %v", err)
	}
	// a roster beside that cannot be read (here: a file where the directory
	// goes) is an error, never silently no roster
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, "fleet"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "fleet", "stars"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if stars, err := RosterFor(filepath.Join(other, "orbits"), ""); err == nil || stars != nil {
		t.Errorf("an unreadable roster beside: %v %v", stars, err)
	}
}
