package goregen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sqlportWit = "// sqlport\npackage aiws:sqlport@0.2.0;\n\nuse aiws:%result/types.{refusal};\n\nworld sqlport-core {\n  export sqlport;\n}\n"

const tagSHA = "0123456789abcdef0123456789abcdef01234567"

// newScene2 is a tree with one gravity core, and a stellar-core-rust that has a
// sqlport world to bootstrap.
func newbie(t *testing.T) *scene {
	t.Helper()
	s := newScene(t)
	fetchRecorder(t)
	s.add(coreSpec{name: "alarm"})
	s.f.tree["wit/aiws-sqlport.wit"] = sqlportWit
	s.f.lsRemote = tagSHA + "\trefs/tags/v0.78.0^{}\n" + "9999999999999999999999999999999999999999\trefs/tags/v0.78.0\n"
	s.f.guests["sqlport"] = []byte("the sqlport guest")
	s.f.gens["sqlport"] = map[string][]byte{"zz_generated.go": []byte("package sqlport\n"), "sqlport_core_0.wasm": []byte("the sqlport module")}
	return s
}

func TestANewCoreIsBuiltTheComposeWayAndItsFilesAreWritten(t *testing.T) {
	s := newbie(t)
	v := s.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0")
	wantReason(t, v, 0, "sqlport: NEW from stellar-core-rust v0.78.0 ("+tagSHA[:7]+")", "aiws:sqlport/sqlport-core@0.2.0",
		"sqlport_core_0.wasm", "build-guest.sh, provenance.json", line1990+" (laid)",
		"left for a human: wit.commit, tape, wit_result (the world uses aiws:%result)")
	if s.read("sqlport/guest.wasm") != "the sqlport guest" || s.read("sqlport/zz_generated.go") != "package sqlport\n" || s.read("sqlport/sqlport_core_0.wasm") != "the sqlport module" {
		t.Error("the generated files were not written")
	}
	info, err := os.Stat(filepath.Join(s.root, "sqlport/build-guest.sh"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("build-guest.sh: %v %v", info, err)
	}
	bg, err := ParseBuildGuest(s.read("sqlport/build-guest.sh"))
	if err != nil || bg.Rev != tagSHA || len(bg.Compose) != 1 || bg.Compose[0] != "sqlport" {
		t.Errorf("%+v %v", bg, err)
	}
	if !strings.Contains(s.read("sqlport/build-guest.sh"), "wasm-tools 1.262.0 and wac 0.12.0") {
		t.Error("the tool versions are not recorded in the script")
	}
	p, err := ParseProv(s.read("sqlport/provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.CoreCommit != tagSHA || p.GenCommit != gravRev || p.GenCommand != "gravity --name sqlport-core --package sqlport --gofmt --output ./zz_generated.go ./guest.wasm" ||
		p.CoreToolchain != line1990+", wasm-tools 1.262.0, wac 0.12.0" || p.Guest.SHA256 != sum([]byte("the sqlport guest")) || p.Embedded[0].File != "sqlport_core_0.wasm" ||
		p.Bindings.SHA256 != sum([]byte("package sqlport\n")) {
		t.Errorf("%+v", p)
	}
	if !strings.Contains(s.read("sqlport/provenance.json"), sum([]byte(sqlportWit))) {
		t.Error("the WIT's sha256 was not derived from the vendored copy")
	}
	// the compose ran the revision's own script on the newest compiler, with the default cargo home remap not needed
	compose := s.f.call("compose.sh")
	if compose.Args[len(compose.Args)-1] != "sqlport" {
		t.Errorf("compose: %s", line(compose))
	}
	if _, set := envOf(compose, "CARGO_ENCODED_RUSTFLAGS"); set {
		t.Errorf("a remap onto the cargo home the run already has: %v", compose.Env)
	}
	if s.f.has("git ls-remote "+"https://git.notusmi.com/stellar-core-rust.git refs/tags/v0.78.0^{} refs/tags/v0.78.0") == false {
		t.Errorf("calls:\n%s", s.f.dump())
	}
	// and a second run is a refusal, not a rewrite
	wantReason(t, s.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 1, "sqlport/provenance.json exists", "name the core to regenerate it")
}

func TestANewCoreInAnotherCargoHomeIsRemappedToTheDefault(t *testing.T) {
	s := newbie(t)
	t.Setenv("CARGO_HOME", "/work/cargo")
	wantReason(t, s.check("--new", "sqlport", "--rev", tagSHA, "--world", "aiws:sqlport/sqlport-core@0.2.0"), 0, "sqlport: NEW from stellar-core-rust "+tagSHA)
	if flags, _ := envOf(s.f.call("compose.sh"), "CARGO_ENCODED_RUSTFLAGS"); flags != "--remap-path-prefix=/work/cargo=/usr/local/cargo" {
		t.Errorf("flags %q", flags)
	}
	if s.f.has("ls-remote") {
		t.Error("a commit was looked up as a tag")
	}
}

func TestANewCoreUsesTheContainersCompilerWhenItIsTheNewest(t *testing.T) {
	s := newbie(t)
	s.f.native = line1990
	wantReason(t, s.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 0, line1990+" (container's)")
	if s.f.has("install.sh") {
		t.Error("a toolchain was laid for a container that has it")
	}
}

func TestNewAndExistingCoresMixInOneRun(t *testing.T) {
	s := newbie(t)
	v := s.check("alarm", "--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0")
	wantReason(t, v, 0, "2 core(s) regenerated", "alarm: guest unchanged", "sqlport: NEW")
}

func TestATagIsResolvedToItsPeeledCommitOrItsOwn(t *testing.T) {
	s := newbie(t)
	s.f.lsRemote = "junk line\nnotasha\trefs/tags/v0.78.0\n" + tagSHA + "\trefs/tags/v0.78.0\n"
	wantReason(t, s.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 0, "("+tagSHA[:7]+")")

	none := newbie(t)
	none.f.lsRemote = ""
	wantReason(t, none.check("--new", "sqlport", "--rev", "v9.9.9", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 2, "sqlport: FAILED at resolve", "no tag v9.9.9")

	down := newbie(t)
	down.f.fail["ls-remote"] = "fatal: unable to access"
	wantReason(t, down.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 2, "FAILED at resolve", "unable to access")
}

func TestARequestTheRevisionsTreeDoesNotMatchIsRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		arrange func(s *scene)
		args    []string
		state   int
		says    string
	}{
		"a world with no feature":         {nil, []string{"--new", "mode", "--rev", "v0.78.0", "--world", "aiws:mode/mode-core@0.1.0"}, 1, "has no wit-guest-mode feature (it builds: wit-guest-alarm, wit-guest-door, wit-guest-identity, wit-guest-sqlport)"},
		"a Cargo.toml that will not read": {func(s *scene) { delete(s.f.tree, "Cargo.toml") }, nil, 2, "Cargo.toml would not read"},
		"a WIT that is not there":         {func(s *scene) { delete(s.f.tree, "wit/aiws-sqlport.wit") }, nil, 1, "has no wit/aiws-sqlport.wit"},
		"another version":                 {nil, []string{"--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.1.0"}, 1, "declares aiws:sqlport@0.2.0, not aiws:sqlport@0.1.0"},
		"a WIT with no package line":      {func(s *scene) { s.f.tree["wit/aiws-sqlport.wit"] = "world sqlport-core {}\n" }, nil, 1, "declares no package line"},
		"another world":                   {nil, []string{"--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/other-core@0.2.0"}, 1, "declares no world other-core"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newbie(t)
			if tc.arrange != nil {
				tc.arrange(s)
			}
			args := tc.args
			if args == nil {
				args = []string{"--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"}
			}
			wantReason(t, s.check(args...), tc.state, "FAILED at resolve", tc.says)
			if s.exists("sqlport") {
				t.Error("a refused core left files behind")
			}
		})
	}
}

func TestTheNewCoresGeneratorIsTheOneTheTreePins(t *testing.T) {
	s := newbie(t)
	s.add(coreSpec{name: "field"})
	other := strings.Replace(s.read("field/provenance.json"), gravRev, "68d8146b88e53e619bfbc0a36b9d70ef9c3bcf06", 1)
	writeFile(t, filepath.Join(s.root, "field/provenance.json"), []byte(other))
	// two cores pin one revision, one pins another: the majority decides
	s.add(coreSpec{name: "mirror"})
	wantReason(t, s.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 0, "sqlport: NEW")
	if p, _ := ParseProv(s.read("sqlport/provenance.json")); p.GenCommit != gravRev {
		t.Errorf("generator %s", p.GenCommit)
	}

	tie := newbie(t)
	writeFile(t, filepath.Join(tie.root, "alarm/provenance.json"), []byte(strings.Replace(tie.read("alarm/provenance.json"), gravRev, "68d8146b88e53e619bfbc0a36b9d70ef9c3bcf06", 1)))
	tie.add(coreSpec{name: "field"})
	wantReason(t, tie.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 2, "FAILED at generate", "equally often")

	empty := newScene(t)
	fetchRecorder(t)
	empty.f.tree["wit/aiws-sqlport.wit"] = sqlportWit
	wantReason(t, empty.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 2, "no core of the tree records a gravity commit")
}

func TestAGeneratorThatWritesMoreOrLessThanOneModuleIsRefused(t *testing.T) {
	s := newbie(t)
	s.f.gens["sqlport"]["sqlport_core_1.wasm"] = []byte("another")
	wantReason(t, s.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 2, "FAILED at generate", "more than one module")

	none := newbie(t)
	delete(none.f.gens["sqlport"], "sqlport_core_0.wasm")
	wantReason(t, none.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 2, "FAILED at generate", "want zz_generated.go and one module")

	nobinding := newbie(t)
	nobinding.f.gens["sqlport"] = map[string][]byte{"a.wasm": []byte("x")}
	wantReason(t, nobinding.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 2, "want zz_generated.go and one module")
}

func TestABootstrapThatFailsEarlierNamesTheStep(t *testing.T) {
	s := newbie(t)
	s.f.fail["git fetch"] = "fatal: no such commit"
	wantReason(t, s.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 2, "sqlport: FAILED at fetch")

	build := newbie(t)
	build.f.fail["compose.sh"] = "boom"
	wantReason(t, build.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 2, "FAILED at build")

	rustc := newbie(t)
	rustc.f.fail["rustc --version"] = "gone"
	wantReason(t, rustc.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 2, "FAILED at toolchain")

	gen := newbie(t)
	gen.f.fail["--package sqlport"] = "gravity broke"
	wantReason(t, gen.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 2, "FAILED at generate", "gravity broke")

	existing := newbie(t)
	writeFile(t, filepath.Join(existing.root, "sqlport/build-guest.sh"), []byte("x"))
	wantReason(t, existing.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 1, "sqlport/build-guest.sh exists")
}

func TestANewCoreInADirectoryThatHoldsHandWrittenGoIsWrittenBesideIt(t *testing.T) {
	s := newbie(t)
	writeFile(t, filepath.Join(s.root, "sqlport/port.go"), []byte("package sqlport\n"))
	wantReason(t, s.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 0, "sqlport: NEW")
	if s.read("sqlport/port.go") != "package sqlport\n" {
		t.Error("a hand-written file was touched")
	}
}

func TestANewCoreWhoseDirectoryCannotBeMadeIsCannotRun(t *testing.T) {
	s := newbie(t)
	writeFile(t, filepath.Join(s.root, "sqlport"), []byte("a file where the directory goes"))
	wantReason(t, s.check("--new", "sqlport", "--rev", "v0.78.0", "--world", "aiws:sqlport/sqlport-core@0.2.0"), 2, "FAILED at write")
}

func TestAWorldThatFailsToParseIsAFinding(t *testing.T) {
	s := newbie(t)
	if out := s.bring0(); !strings.Contains(out, "is not aiws:") {
		t.Errorf("%s", out)
	}
}

// bring0 drives bootstrap with a world the argument parser would have refused.
func (s *scene) bring0() string {
	r := &runner{}
	_, f := r.bring(NewCore{Core: "sqlport", Rev: "v1", World: "nope"})
	return f.msg
}
