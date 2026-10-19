package goregen

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// the files of a core directory, by name, as the tree holds them.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(b)
	}
	return out
}

func wantReason(t *testing.T, v checks.Verdict, state int, says ...string) {
	t.Helper()
	if v.State != state {
		t.Errorf("state %d, want %d: %s", v.State, state, v.Reason)
	}
	for _, s := range says {
		if !strings.Contains(v.Reason, s) {
			t.Errorf("the reason does not say %q:\n%s", s, v.Reason)
		}
	}
}

func TestACoreThatRegeneratesIdenticalLeavesTheTreeAsItWas(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm"})
	before := snapshot(t, filepath.Join(s.root, "alarm"))
	v := s.check("alarm")
	wantReason(t, v, 0, "dev:go-regen: PASS - 1 core(s) regenerated", "alarm: guest unchanged", "bindings unchanged", "embedded alarm_core.wasm unchanged", lineNat+" (container's)")
	if after := snapshot(t, filepath.Join(s.root, "alarm")); len(after) != len(before) {
		t.Fatalf("files %v, were %v", after, before)
	} else {
		for f, b := range before {
			if after[f] != b {
				t.Errorf("%s moved", f)
			}
		}
	}
	// the chain: the commit fetched shallow, the recorded cargo argv with its own target, the recorded generator
	if !s.f.has("git fetch --quiet --depth 1 "+checks.RustKit.GitURL()+" "+commitA) || !s.f.has("rustup target add wasm32-unknown-unknown") {
		t.Errorf("calls:\n%s", s.f.dump())
	}
	cargo := s.f.call("cargo build --locked --release --no-default-features --features wit-guest-alarm --target wasm32-unknown-unknown")
	if cargo.Name != "cargo" || !strings.HasSuffix(cargo.Dir, "core") && !strings.Contains(cargo.Dir, "core-"+commitA[:12]) {
		t.Errorf("cargo: %+v", cargo)
	}
	if target, _ := envOf(cargo, "CARGO_TARGET_DIR"); !strings.HasSuffix(target, "/go-regen/alarm/target") {
		t.Errorf("target %q", target)
	}
	if _, set := envOf(cargo, "CARGO_ENCODED_RUSTFLAGS"); set {
		t.Errorf("a remap was set with nothing recorded to remap to: %v", cargo.Env)
	}
	if !s.f.has("CARGO_BUILD_JOBS") && !strings.Contains(strings.Join(cargo.Env, " "), "CARGO_BUILD_JOBS=") {
		t.Errorf("the build is not bounded: %v", cargo.Env)
	}
	gen := s.f.call("--package alarm")
	if gen.Dir != filepath.Join(s.root, "alarm") || !strings.HasSuffix(gen.Args[len(gen.Args)-1], "/go-regen/alarm/gen/guest.wasm") ||
		line(gen) == "" || !strings.Contains(line(gen), "--output "+filepath.Join(filepath.Dir(gen.Args[len(gen.Args)-1]), "zz_generated.go")) {
		t.Errorf("gravity: %s in %s", line(gen), gen.Dir)
	}
	if gofmt, _ := envOf(gen, "GOFMT"); gofmt != "/opt/go/bin/gofmt" {
		t.Errorf("GOFMT %q", gofmt)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("ATOM_WORK"), "go-regen")); err == nil {
		t.Error("the work directory was left behind")
	}
}

func TestACoreThatRegeneratesDifferentlyIsWrittenAndItsRecordFollows(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm", bindings: true})
	s.f.guests["alarm"] = []byte("a new guest")
	s.f.gens["alarm"]["zz_generated.go"] = []byte("package alarm // new\n")
	s.f.gens["alarm"]["alarm_core.wasm"] = []byte("a new module")
	oldProv := s.read("alarm/provenance.json")
	v := s.check("alarm")
	wantReason(t, v, 0, "alarm: guest ", "->", "bindings ", "embedded alarm_core.wasm ")
	if strings.Contains(v.Reason, "guest unchanged") {
		t.Errorf("the guest moved and the line says it did not: %s", v.Reason)
	}
	if s.read("alarm/guest.wasm") != "a new guest" || s.read("alarm/zz_generated.go") != "package alarm // new\n" || s.read("alarm/alarm_core.wasm") != "a new module" {
		t.Error("the regenerated files were not written")
	}
	p, err := ParseProv(s.read("alarm/provenance.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Guest.SHA256 != sum([]byte("a new guest")) || p.Guest.Bytes != len("a new guest") || p.Embedded[0].SHA256 != sum([]byte("a new module")) ||
		p.Bindings.SHA256 != sum([]byte("package alarm // new\n")) {
		t.Errorf("the record did not follow: %+v", p)
	}
	// only hashes and sizes moved
	if strings.ReplaceAll(strings.ReplaceAll(oldProv, "\n", ""), " ", "")[:40] != strings.ReplaceAll(strings.ReplaceAll(s.read("alarm/provenance.json"), "\n", ""), " ", "")[:40] {
		t.Error("the record's head moved")
	}
}

func TestAComposedCoreRunsTheRevisionsComposeAndLaysItsRustc(t *testing.T) {
	s := newScene(t)
	urls := fetchRecorder(t)
	s.add(coreSpec{name: "door", commit: commitB, compose: []string{"identity", "door"}, toolline: line1971, bindings: true,
		modules: map[string]string{"embedded_wasm": "door_core_1.wasm", "embedded_wasm_identity": "door_core_0.wasm"}})
	v := s.check("door")
	wantReason(t, v, 0, "door: guest unchanged", "embedded door_core_1.wasm unchanged ", ", door_core_0.wasm unchanged ", line1971+" (laid)",
		"wasm-tools 1.261.0 (recorded 1.261.0), wac 0.12.0 (recorded 0.12.0)")
	dist, _ := checks.RegenRustDist("1.97.1", "")
	want := append(append([]string{}, dist.URLs...), checks.WasmTools1261URL)
	if strings.Join(*urls, " ") != strings.Join(want, " ") {
		t.Errorf("fetched %v, want %v", *urls, want)
	}
	if !strings.HasPrefix(line(s.f.call("wasm-tools-1.261.0.tar.gz")), "tar -xzf ") || !strings.Contains(line(s.f.call("wasm-tools-1.261.0.tar.gz")), "--strip-components=1 wasm-tools-1.261.0-x86_64-linux/wasm-tools") {
		t.Errorf("calls:\n%s", s.f.dump())
	}
	if s.f.count("install.sh --prefix=") != 4 || !s.f.has("--disable-ldconfig") {
		t.Errorf("calls:\n%s", s.f.dump())
	}
	compose := s.f.call("compose.sh")
	if !strings.HasSuffix(compose.Name, "/tools/compose/compose.sh") || compose.Args[len(compose.Args)-2] != "identity" || compose.Args[len(compose.Args)-1] != "door" {
		t.Errorf("compose: %s", line(compose))
	}
	path, _ := envOf(compose, "PATH")
	if !strings.HasPrefix(path, filepath.Join(os.Getenv("ATOM_WORK"), "go-regen", "wasm-tools-1.261.0")+":"+filepath.Join(os.Getenv("ATOM_WORK"), "go-regen", "rust-1.97.1", "bin")+":") {
		t.Errorf("the laid compiler is not first on PATH: %s", path)
	}
	if s.f.has("rustup") {
		t.Error("rustup ran for a toolchain laid with its std")
	}
}

func TestACoreWhoseRevisionIsFetchedOnceAndWhoseToolchainIsLaidOnce(t *testing.T) {
	s := newScene(t)
	urls := fetchRecorder(t)
	s.add(coreSpec{name: "alarm", toolline: line1971})
	s.add(coreSpec{name: "field", toolline: line1971})
	s.add(coreSpec{name: "mirror"})
	v := s.check("alarm", "field", "mirror")
	wantReason(t, v, 0, "3 core(s) regenerated", "alarm: guest", "field: guest", "mirror: guest")
	if len(*urls) != 4 || s.f.count("git fetch") != 1 || s.f.count("rustc --version") != 2 {
		t.Errorf("%d tarballs, %d fetches:\n%s", len(*urls), s.f.count("git fetch"), s.f.dump())
	}
	if s.f.count("rustup target add") != 1 {
		t.Errorf("the container's compiler was provisioned %d times", s.f.count("rustup target add"))
	}
}

func TestAToolchainWithNoPinIsCannotRunNamingBothVersions(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm", toolline: "rustc 1.98.2 (abc123def 2026-08-01)"})
	v := s.check("alarm")
	wantReason(t, v, 2, "alarm: FAILED at toolchain", "pins rustc 1.98.2", "carries rustc 1.97.0", "only 1.97.1, 1.99.0")
	if s.f.has("cargo build") {
		t.Error("a core with no compiler was built anyway")
	}
}

func TestALaidCompilerThatPrintsAnotherLineIsNotUsed(t *testing.T) {
	s := newScene(t)
	fetchRecorder(t)
	s.f.laid["1.97.1"] = "rustc 1.97.1 (deadbeef0 2026-07-14)"
	s.add(coreSpec{name: "alarm", toolline: line1971})
	v := s.check("alarm")
	wantReason(t, v, 2, "FAILED at toolchain", line1971, "deadbeef0")
	if s.f.has("cargo build") {
		t.Error("built with the wrong compiler")
	}
	// the same version asked for by another core with the line the laid one prints
	s2 := newScene(t)
	fetchRecorder(t)
	s2.add(coreSpec{name: "alarm", toolline: line1971})
	s2.add(coreSpec{name: "field", toolline: "rustc 1.97.1 (other0000 2026-07-14)"})
	v = s2.check("alarm", "field")
	wantReason(t, v, 2, "field: FAILED at toolchain", "other0000", line1971)
}

func TestTheContainersCompilerOfAnotherReleaseLineIsNotTheContainers(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm", toolline: "rustc 1.97.0 (0000000aa 2026-07-07)"})
	v := s.check("alarm")
	wantReason(t, v, 2, "FAILED at toolchain", "pins rustc 1.97.0", "can lay only")
}

func TestARecordThatNamesNoRustcIsAFinding(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm", toolline: "gcc 14"})
	wantReason(t, s.check("alarm"), 1, "FAILED at toolchain", "records no `rustc X.Y.Z (hash date)`")
}

func TestACoreWhoseBuildScriptBuildsAnotherCommitIsAFindingNamingBoth(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm"})
	script := s.read("alarm/build-guest.sh")
	writeFile(t, filepath.Join(s.root, "alarm/build-guest.sh"), []byte(strings.Replace(script, commitA, commitB, 1)))
	wantReason(t, s.check("alarm"), 1, "alarm: FAILED at read", commitA, commitB)
	if s.f.has("git fetch") {
		t.Error("fetched a commit the record and the script disagree about")
	}
}

func TestARecordAndAScriptThatDescribeDifferentBuildsAreAFinding(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm"})
	prov := s.read("alarm/provenance.json")
	writeFile(t, filepath.Join(s.root, "alarm/provenance.json"), []byte(strings.Replace(prov, "wit-guest-alarm", "wit-guest-field", 1)))
	wantReason(t, s.check("alarm"), 1, "core.build is", "build-guest.sh runs `cargo build")

	c := newScene(t)
	c.add(coreSpec{name: "door", compose: []string{"identity", "door"}})
	prov = c.read("door/provenance.json")
	writeFile(t, filepath.Join(c.root, "door/provenance.json"), []byte(strings.Replace(prov, "OUT.wasm identity door", "OUT.wasm door", 1)))
	wantReason(t, c.check("door"), 1, "core.build is", "compose.sh over identity door")
}

func TestARequestWithNoCoreListsWhatCanBeRegenerated(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm"})
	s.add(coreSpec{name: "converge"})
	writeFile(t, filepath.Join(s.root, "depth/provenance.json"), []byte(`{"ts":true}`))
	writeFile(t, filepath.Join(s.root, "notacore/provenance.json"), []byte(`not json`))
	writeFile(t, filepath.Join(s.root, "README.md"), []byte("x"))
	writeFile(t, filepath.Join(s.root, "nogravity/provenance.json"), []byte(strings.Replace(s.read("alarm/provenance.json"), "gravity --name", "other --name", 1)))
	writeFile(t, filepath.Join(s.root, "nogravity/build-guest.sh"), []byte("x"))
	wantReason(t, s.check(), 2, "CANNOT RUN", "no core named", "usage:", "alarm, converge")
	if strings.Contains(s.check().Reason, "depth") || strings.Contains(s.check().Reason, "nogravity") {
		t.Error("a core that gravity does not generate was listed")
	}
	wantReason(t, s.check("--fast"), 2, "the arguments do not read", "unknown option --fast", "usage:")
}

func TestACoreTheTreeDoesNotHaveIsCannotRunListingTheOnesItDoes(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm"})
	wantReason(t, s.check("nosuch"), 2, "nosuch: FAILED at read", "nosuch/provenance.json would not read", "alarm")
	os.Remove(filepath.Join(s.root, "alarm/build-guest.sh"))
	wantReason(t, s.check("alarm"), 2, "alarm/build-guest.sh would not read")
}

func TestARecordOrAScriptThatDoesNotReadIsAFinding(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm"})
	writeFile(t, filepath.Join(s.root, "alarm/provenance.json"), []byte("{"))
	wantReason(t, s.check("alarm"), 1, "alarm/provenance.json is not JSON")
	s.add(coreSpec{name: "alarm"})
	writeFile(t, filepath.Join(s.root, "alarm/build-guest.sh"), []byte("#!/bin/sh\n"))
	wantReason(t, s.check("alarm"), 1, "alarm/build-guest.sh has no REV=")
}

func TestTheOneWorstCoreSettlesTheRun(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm"})
	s.add(coreSpec{name: "field", toolline: "rustc 1.98.2 (abc123def 2026-08-01)"})
	s.add(coreSpec{name: "mirror"})
	writeFile(t, filepath.Join(s.root, "mirror/build-guest.sh"), []byte("#!/bin/sh\n"))
	v := s.check("alarm", "field", "mirror")
	wantReason(t, v, 2, "alarm: guest unchanged", "field: FAILED at toolchain", "mirror: FAILED at read")
	if strings.Contains(v.Reason, "PASS") {
		t.Errorf("a failed run reads as a pass: %s", v.Reason)
	}
	s2 := newScene(t)
	s2.add(coreSpec{name: "mirror"})
	writeFile(t, filepath.Join(s2.root, "mirror/build-guest.sh"), []byte("#!/bin/sh\n"))
	wantReason(t, s2.check("mirror"), 1, "FAILED at read")
}

func TestTheGeneratorIsHeldToItsRecord(t *testing.T) {
	for name, tc := range map[string]struct {
		arrange func(s *scene)
		state   int
		says    string
	}{
		"no gravity staged": {func(s *scene) { t.Setenv("GRAVITY", "gravity-nowhere") }, 2, "no gravity staged for " + gravRev},
		"another gravity":   {func(s *scene) { s.f.gravityVersion = "gravity 0.1.0 (0123456789abcdef)" }, 2, "provenance.json pins the generator at " + gravRev},
		"no gofmt": {func(s *scene) {
			lookPath = func(string) (string, error) { return "", errors.New("not found") }
		}, 2, "tool:gofmt is not on PATH"},
		"the generator fails": {func(s *scene) { s.f.fail["--package alarm"] = "gofmt failed" }, 2, "gofmt failed"},
		"a command that is not gravity": {func(s *scene) {
			p := s.read("alarm/provenance.json")
			writeFile(t, filepath.Join(s.root, "alarm/provenance.json"), []byte(strings.Replace(p, "gravity --name", "other --name", 1)))
		}, 1, "does not start with gravity"},
		"a command that maps nothing": {func(s *scene) {
			p := s.read("alarm/provenance.json")
			writeFile(t, filepath.Join(s.root, "alarm/provenance.json"), []byte(strings.Replace(p, "./guest.wasm", "guest.wasm", 1)))
		}, 1, "does not name ./guest.wasm and ./zz_generated.go"},
		"a module it did not write":   {func(s *scene) { delete(s.f.gens["alarm"], "alarm_core.wasm") }, 1, "the generator did not write alarm_core.wasm"},
		"a file it was not asked for": {func(s *scene) { s.f.gens["alarm"]["extra.wasm"] = []byte("x") }, 1, "wrote extra.wasm, which provenance.json does not record"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newScene(t)
			s.add(coreSpec{name: "alarm"})
			tc.arrange(s)
			wantReason(t, s.check("alarm"), tc.state, "alarm: FAILED at generate", tc.says)
		})
	}
}

func TestABuildThatFailsNamesTheCoreAndTheStep(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm"})
	s.f.fail["cargo build"] = "error[E0463]: can't find crate for `core`"
	wantReason(t, s.check("alarm"), 2, "alarm: FAILED at build", "E0463", "wit-guest-alarm", lineNat)

	c := newScene(t)
	c.add(coreSpec{name: "door", compose: []string{"identity", "door"}})
	c.f.fail["compose.sh"] = "compose: import cycle"
	wantReason(t, c.check("door"), 2, "door: FAILED at build", "tools/compose/compose.sh identity door under "+lineNat, "import cycle")
}

func TestABuildThatLeavesNoArtifactIsCannotRun(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm"})
	delete(s.f.guests, "alarm")
	wantReason(t, s.check("alarm"), 2, "alarm: FAILED at build", "the build left no wasm32-unknown-unknown/release/stellar_core.wasm")
}

func TestAComposeThatLeavesNoOutputIsCannotRun(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "door", compose: []string{"door"}})
	delete(s.f.guests, "door")
	s.f.noCompose = true
	wantReason(t, s.check("door"), 2, "door: FAILED at build", "the build left no tools/compose/compose.sh output")
}

func TestACrateWhoseArtifactIsNotTheOneTheScriptCopiesIsAFinding(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm"})
	s.f.tree["Cargo.toml"] = strings.Replace(cargoToml, `name = "stellar-core"`, `name = "other"`, 1)
	wantReason(t, s.check("alarm"), 1, "FAILED at build", "copies stellar_core.wasm", `"other.wasm"`)
	s.f.tree["Cargo.toml"] = ""
	t2 := newScene(t)
	t2.add(coreSpec{name: "alarm"})
	t2.f.tree["Cargo.toml"] = "[package]\n"
	wantReason(t, t2.check("alarm"), 1, "FAILED at build")
}

func TestAnUnpackThatFailsIsCannotRunAtTheFetch(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm"})
	s.f.fail["git fetch"] = "fatal: couldn't find remote ref"
	wantReason(t, s.check("alarm"), 2, "alarm: FAILED at fetch", "stellar-core-rust", commitA, "couldn't find remote ref")
}

func TestTheContainersRustcThatWillNotAnswerIsCannotRun(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm"})
	s.add(coreSpec{name: "field"})
	s.f.fail["rustc --version"] = "rustc: not found"
	v := s.check("alarm", "field")
	wantReason(t, v, 2, "alarm: FAILED at toolchain", "field: FAILED at toolchain", "would not answer")
	if s.f.count("rustc --version") != 1 {
		t.Errorf("the failed probe was repeated %d times", s.f.count("rustc --version"))
	}
}

func TestTheContainersWasmStdThatCannotBeAddedIsCannotRun(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm"})
	s.f.fail["rustup target add"] = "error: network unreachable"
	wantReason(t, s.check("alarm"), 2, "alarm: FAILED at toolchain", "rustup", "network unreachable")
}

func TestALaidToolchainThatCannotBeFetchedInstalledOrRunIsCannotRun(t *testing.T) {
	for name, arrange := range map[string]func(s *scene){
		"a tarball that will not download": func(s *scene) {
			fetchPinned = func(context.Context, *http.Client, []string, string, string) error {
				return errors.New("sha256 mismatch")
			}
		},
		"an installer that fails":         func(s *scene) { fetchRecorder(t); s.f.fail["install.sh"] = "install: no space left" },
		"a compiler that will not answer": func(s *scene) { fetchRecorder(t); s.f.fail["rust-1.97.1/bin/rustc"] = "exec format error" },
	} {
		t.Run(name, func(t *testing.T) {
			old := fetchPinned
			t.Cleanup(func() { fetchPinned = old })
			s := newScene(t)
			s.add(coreSpec{name: "alarm", toolline: line1971})
			arrange(s)
			wantReason(t, s.check("alarm"), 2, "alarm: FAILED at toolchain")
		})
	}
}

func TestTheRecordedCargoHomeIsRemappedToOnlyWhenTheRunsDiffers(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "door", registry: "/home/rob/.cargo"})
	wantReason(t, s.check("door"), 0, "door: guest")
	cargo := s.f.call("cargo build")
	if flags, _ := envOf(cargo, "CARGO_ENCODED_RUSTFLAGS"); flags != "--remap-path-prefix=/usr/local/cargo=/home/rob/.cargo" {
		t.Errorf("flags %q", flags)
	}

	same := newScene(t)
	same.add(coreSpec{name: "door", registry: "/usr/local/cargo"})
	same.check("door")
	if _, set := envOf(same.f.call("cargo build"), "CARGO_ENCODED_RUSTFLAGS"); set {
		t.Error("a remap of a directory onto itself")
	}

	t.Setenv("CARGO_HOME", "")
	t.Setenv("HOME", "/home/pod")
	home := newScene(t)
	t.Setenv("CARGO_HOME", "")
	home.add(coreSpec{name: "door", registry: "/home/rob/.cargo"})
	home.check("door")
	if flags, _ := envOf(home.f.call("cargo build"), "CARGO_ENCODED_RUSTFLAGS"); flags != "--remap-path-prefix=/home/pod/.cargo=/home/rob/.cargo" {
		t.Errorf("flags %q", flags)
	}

	two := newScene(t)
	two.add(coreSpec{name: "door"})
	writeFile(t, filepath.Join(two.root, "door/guest.wasm"), []byte("\x00/a/registry/src/index.crates.io-1/x\x00/b/registry/src/index.crates.io-1/y\x00"))
	wantReason(t, two.check("door"), 1, "door: FAILED at build", "under 2 cargo homes")
}

func TestAToolVersionThatDoesNotReadIsAQuestionMark(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "door", compose: []string{"door"}})
	p := s.read("door/provenance.json")
	// a record that names no wasm-tools uses the container's, whatever it prints
	writeFile(t, filepath.Join(s.root, "door/provenance.json"), []byte(strings.Replace(p, ", wasm-tools 1.261.0", "", 1)))
	s.f.toolVersions["wasm-tools"] = "wasm-tools dev"
	s.f.toolVersions["wac"] = "wac 1.2"
	wantReason(t, s.check("door"), 0, "wasm-tools ? (recorded unrecorded), wac ? (recorded 0.12.0)")
	s.f.fail["wac --version"] = "wac: not found"
	wantReason(t, s.check("door"), 0, "wac ? (recorded 0.12.0)")
	writeFile(t, filepath.Join(s.root, "door/provenance.json"), []byte(strings.Replace(p, ", wac 0.12.0", "", 1)))
	wantReason(t, s.check("door"), 0, "(recorded unrecorded)")
}

func TestTheRecordedWasmToolsIsUsedWhenItIsTheContainersAndNotFetchedTwice(t *testing.T) {
	s := newScene(t)
	urls := fetchRecorder(t)
	s.add(coreSpec{name: "door", compose: []string{"door"}})
	s.f.toolVersions["wasm-tools"] = "wasm-tools 1.261.0 (abc 2026-08-01)"
	wantReason(t, s.check("door"), 0, "wasm-tools 1.261.0 (recorded 1.261.0)")
	if len(*urls) != 0 {
		t.Errorf("fetched %v for the version the container has", *urls)
	}
	c := newScene(t)
	urls = fetchRecorder(t)
	c.add(coreSpec{name: "door", compose: []string{"door"}})
	c.add(coreSpec{name: "fade", compose: []string{"fade"}})
	wantReason(t, c.check("door", "fade"), 0, "2 core(s) regenerated")
	if len(*urls) != 1 || (*urls)[0] != checks.WasmTools1261URL {
		t.Errorf("fetched %v", *urls)
	}
}

func TestARecordedWasmToolsThatCannotBeHadIsCannotRun(t *testing.T) {
	s := newScene(t)
	fetchRecorder(t)
	s.add(coreSpec{name: "door", compose: []string{"door"}})
	p := s.read("door/provenance.json")
	writeFile(t, filepath.Join(s.root, "door/provenance.json"), []byte(strings.Replace(p, "wasm-tools 1.261.0", "wasm-tools 1.260.0", 1)))
	wantReason(t, s.check("door"), 2, "door: FAILED at toolchain", "records wasm-tools 1.260.0", "container has 1.262.0", "no release of 1.260.0 is pinned")
	if s.f.has("cargo") || s.f.has("compose.sh") {
		t.Error("built with the wrong wasm-tools")
	}

	for name, tc := range map[string]struct {
		arrange func(s *scene)
		says    string
	}{
		"a release that will not download": {func(s *scene) {
			fetchPinned = func(context.Context, *http.Client, []string, string, string) error {
				return errors.New("sha256 mismatch")
			}
		}, "wasm-tools 1.261.0: sha256 mismatch"},
		"a directory that cannot be made": {func(s *scene) {
			fetchPinned = func(_ context.Context, _ *http.Client, sources []string, _, dest string) error {
				if !strings.Contains(dest, "wasm-tools-1.261.0") {
					writeFile(t, dest, []byte("tgz"))
					return nil
				}
				writeFile(t, dest, []byte("tgz"))
				writeFile(t, strings.TrimSuffix(dest, ".tar.gz"), []byte("a file where the directory goes"))
				return nil
			}
		}, "wasm-tools 1.261.0"},
		"an archive that will not unpack":      {func(s *scene) { fetchRecorder(t); s.f.fail["wasm-tools-1.261.0.tar.gz"] = "tar: bad archive" }, "bad archive"},
		"a binary that prints another version": {func(s *scene) { fetchRecorder(t); s.f.laidWasm = "wasm-tools 1.262.0 (abc 2026-09-01)" }, "the wasm-tools laid for 1.261.0 prints 1.262.0"},
	} {
		t.Run(name, func(t *testing.T) {
			old := fetchPinned
			t.Cleanup(func() { fetchPinned = old })
			s := newScene(t)
			s.add(coreSpec{name: "door", compose: []string{"door"}})
			tc.arrange(s)
			wantReason(t, s.check("door"), 2, "door: FAILED at toolchain", tc.says)
		})
	}
}

func TestACargoHomeIsSearchedForWhenTheRecordedGuestDoesNotShowOne(t *testing.T) {
	// the build in the container's own home is not the record; the workstation's is
	s := newScene(t)
	s.add(coreSpec{name: "alarm"})
	recorded := s.f.guests["alarm"]
	s.f.guests["alarm"] = []byte("natural")
	s.f.remapped["alarm"] = recorded
	v := s.check("alarm")
	wantReason(t, v, 0, "alarm: guest unchanged", "built under cargo home "+workstationCargoHome)
	if s.f.count("cargo build") != 2 {
		t.Errorf("%d builds:\n%s", s.f.count("cargo build"), s.f.dump())
	}
	if _, set := envOf(s.f.call("cargo build"), "CARGO_ENCODED_RUSTFLAGS"); set {
		t.Errorf("the first build was remapped in a run that already has the canonical home")
	}

	// the container's own home is the record: one build
	same := newScene(t)
	same.add(coreSpec{name: "alarm"})
	wantReason(t, same.check("alarm"), 0, "alarm: guest unchanged")
	if same.f.count("cargo build") != 1 {
		t.Errorf("%d builds", same.f.count("cargo build"))
	}

	// neither is the record: the canonical build is kept, whatever home the runner has,
	// and the line names the home kept
	for _, podHome := range []string{"/usr/local/cargo", "/work/cargo"} {
		t.Setenv("CARGO_HOME", podHome)
		none := newScene(t)
		t.Setenv("CARGO_HOME", podHome)
		none.add(coreSpec{name: "alarm"})
		none.f.guests["alarm"] = []byte("natural")
		none.f.remapped["alarm"] = []byte("workstation")
		v := none.check("alarm")
		wantReason(t, v, 0, "alarm: guest ", "built under cargo home /usr/local/cargo")
		if none.read("alarm/guest.wasm") != "natural" || none.f.count("cargo build") != 2 {
			t.Errorf("pod home %s: the kept guest is %q after %d builds", podHome, none.read("alarm/guest.wasm"), none.f.count("cargo build"))
		}
		flags, set := envOf(none.f.call("cargo build"), "CARGO_ENCODED_RUSTFLAGS")
		if (podHome == "/work/cargo") != set || set && flags != "--remap-path-prefix=/work/cargo=/usr/local/cargo" {
			t.Errorf("pod home %s: first build flags %q (%v)", podHome, flags, set)
		}
	}
}

func TestARecordTheTreeCannotWriteIsCannotRun(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm"})
	s.f.guests["alarm"] = []byte("moved")
	// a directory where the guest should be: it reads as nothing and will not be written
	if err := os.Remove(filepath.Join(s.root, "alarm/guest.wasm")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(s.root, "alarm/guest.wasm"), 0o755); err != nil {
		t.Fatal(err)
	}
	wantReason(t, s.check("alarm"), 2, "alarm: FAILED at write")
}

func TestARecordThatLacksARecordedBlockIsAFindingAtTheWrite(t *testing.T) {
	s := newScene(t)
	s.add(coreSpec{name: "alarm", bindings: true})
	// a bindings block that names no hash: it parses, and cannot be rewritten
	p := s.read("alarm/provenance.json")
	i := strings.Index(p, `"bindings"`)
	broken := p[:i] + `"bindings": {"file": "zz_generated.go", "sha256": "none", "bytes": 3}` + "\n}\n"
	writeFile(t, filepath.Join(s.root, "alarm/provenance.json"), []byte(broken))
	wantReason(t, s.check("alarm"), 1, "alarm: FAILED at write", "bindings has no sha256 to rewrite")
}
