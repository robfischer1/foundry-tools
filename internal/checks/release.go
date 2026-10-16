package checks

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// THE RELEASE BUILD, DERIVED (CA master-plan F13/F17). The image's compile
// lives in each repo's Dockerfile today; F17 shrinks that Dockerfile to a base
// and a COPY, so the three facts the compile carries — what package, what
// output name, which modifiers — have to move somewhere the Gate can read.
//
// MEASURED BEFORE DECIDING, 2026-09-17, every build invocation in the fleet's
// 33 Dockerfiles:
//
//	flags        identical in all 29 Go repos: CGO_ENABLED=0 -trimpath -ldflags="-s -w"
//	target       always -o /out/<X> ./cmd/<X>, never another shape
//	-mod=vendor  present in exactly the 5 repos carrying vendor/, absent in every
//	             repo without one — zero disagreements
//
// So nothing is declared that can be derived: the flags are a constant, vendor
// is a directory, and the binaries are the Dockerfile's own COPY lines — the
// files the image carries are the ones it copies out of release/ (buildlane.
// ReleaseCopies), each built from ./cmd/<name>.
//
// AND "BUILD EVERY cmd/ DIRECTORY" IS THE TRAP THIS AVOIDS. helios carries
// cmd/helios-backfill, thalia carries cmd/calibration-fit, cmd/cognition-migrate
// and cmd/seamprobe, and NONE of those are in their images. A derivation that
// walked cmd/ would ship four binaries the fleet deliberately does not ship, so
// the walk is never the rule: the Dockerfile's COPY lines are. (A binary REPO's
// cast is the other case — its binaries are its product, so it ships every
// main; see cast.go.)

// ReleaseFlags are the compile flags every image in the fleet is built with.
// They are a constant because the measurement says they are one: a repo that
// wants different flags is a decision, not a default.
var ReleaseFlags = []string{"-trimpath", "-ldflags=-s -w"}

// ReleaseOut is where a release build puts its binaries inside the lane.
const ReleaseOut = "/out"

// ReleaseBinary is one binary a release build produces.
type ReleaseBinary struct {
	// Name is the file the image carries, e.g. blade-controller.
	Name string
	// Package is the main package it is built from, e.g. ./cmd/blade-controller.
	Package string
}

// ReleasePlan is a repo's whole release build.
type ReleasePlan struct {
	// Star is the name the record and the convention are keyed on.
	Star string
	// Lane is the toolchain that compiles it: LaneGo or LaneRust.
	Lane Lane
	// Binaries are what the image will carry, sorted by name.
	Binaries []ReleaseBinary
	// Vendored is whether the module vendors its dependencies, which is the
	// only build modifier the fleet varies. Go only; cargo resolves from its
	// lock.
	Vendored bool
}

// GoReleasePlan derives a Go repo's release build: the binaries the Dockerfile
// copies out of release/, each from ./cmd/<name>.
//
// No binaries is an error rather than a guess — a Dockerfile that asks for
// release/ and copies no file from it has nothing for the build to name.
func GoReleasePlan(star string, copies []string, vendored bool) (ReleasePlan, error) {
	p, err := releasePlan(star, copies, LaneGo, func(n string) string { return "./cmd/" + n })
	if err != nil {
		return ReleasePlan{}, err
	}
	p.Vendored = vendored
	return p, nil
}

// RustReleasePlan derives a Rust repo's release build: the binaries the
// Dockerfile copies out of release/, each the workspace package of that name
// (`cargo build -p <name>`).
//
// MEASURED BEFORE DECIDING, 2026-09-19, the fleet's Rust star Dockerfiles and
// the template that pours them: `cargo build --release -p <star>` from a
// workspace whose binary crate is named for the star, the binary read back
// from target/release/<star>. One shape, so the package IS the name.
func RustReleasePlan(star string, copies []string) (ReleasePlan, error) {
	return releasePlan(star, copies, LaneRust, func(n string) string { return n })
}

// releasePlan is the two plans' shared half: the Dockerfile's copied names,
// each checked, each mapped to the package its lane builds it from, sorted by
// name.
func releasePlan(star string, copies []string, lane Lane, pkg func(string) string) (ReleasePlan, error) {
	if star == "" {
		return ReleasePlan{}, fmt.Errorf("the repository names no star, so its release build has no binary to name")
	}
	if len(copies) == 0 {
		return ReleasePlan{}, fmt.Errorf("the Dockerfile asks for %s/ and copies no file out of it, so the release build has no binary to name", "release")
	}
	names := copies
	p := ReleasePlan{Star: star, Lane: lane}
	seen := map[string]bool{}
	for _, n := range names {
		if n == "" || strings.ContainsAny(n, `/\`) || strings.HasPrefix(n, ".") || strings.HasPrefix(n, "-") {
			return ReleasePlan{}, fmt.Errorf("the Dockerfile copies %q out of release/, which is not a binary name", n)
		}
		if seen[n] {
			return ReleasePlan{}, fmt.Errorf("the Dockerfile copies %q out of release/ twice", n)
		}
		seen[n] = true
		p.Binaries = append(p.Binaries, ReleaseBinary{Name: n, Package: pkg(n)})
	}
	// Ordered by a COMPARISON, not an inequality. `a.Name < b.Name` and
	// `a.Name <= b.Name` sort a duplicate-free list identically — duplicates
	// are refused above — so the boundary mutant on `<` is unkillable by
	// construction and a test written to kill it would be testing nothing.
	// strings.Compare says the same thing with no operator to mutate.
	slices.SortFunc(p.Binaries, func(a, b ReleaseBinary) int { return strings.Compare(a.Name, b.Name) })
	return p, nil
}

// THE BUN AND PYTHON RELEASES (CA F17). A compiled star's artifact is a
// binary; a bun star's is its bundle and a python star's is its venv, and
// both are BUILT ON THE IMAGE'S OWN BASE — the base the Dockerfile's runtime
// stage FROMs — so the interpreter, the runtime and the paths baked into the
// artifact are the ones the image runs. A venv built on another python is a
// venv whose every shebang and symlink points at an interpreter the image
// does not have.
//
// WHAT THE TREE SAYS, AND ONLY THAT. A bun star's bundle is its own
// (calliope bundles one server.js, demeter builds a SPA beside it), so there
// is no convention for the steps: the repo's root package.json defines a
// "release" script, the same interface as its gate, build and test scripts,
// and the fleet runs it after the frozen install. A python star's release
// derives entirely: the project and its required dependencies, no extras.

// ReleaseTree is where a bun release's tree sits in its container, and so
// where the steps run; the steps leave the image's files under
// ReleaseTree/release.
const ReleaseTree = "/src"

// PythonReleaseApp is where a python release's venv is built — /app, the
// directory the image carries it at, because a venv is not relocatable: its
// console scripts name <PythonReleaseApp>/.venv/bin/python in their shebang.
const PythonReleaseApp = "/app"

// TSReleaseArgs is the bun release, as an argv: the repo's own release script,
// which leaves the image's files under ReleaseTree/release. A package.json
// with no release script fails here, as a finding about the tree.
var TSReleaseArgs = []string{"bun", "run", "release"}

// PythonReleaseArgs is the python release, as an argv: the star's own lock
// (--locked, so a uv.lock behind its pyproject is a finding and not a silent
// re-resolve), no dev group, no extras, and the project installed as a WHEEL
// rather than editable — the image carries the venv alone, never the source
// tree an editable install would point back into.
func PythonReleaseArgs() []string {
	return []string{"uv", "sync", "--locked", "--no-dev", "--no-editable"}
}

// ReleaseStepState classifies one bun or python release step's exit.
//
// uv and bun do not speak the atoms' 0/1/2: uv exits 2 on every error,
// among them a lock behind its pyproject, which is the tree's fault, and bun
// passes through whatever the step's own command exits. So the code alone
// cannot say whose fault a failure is, and this reads it the way the audits
// do: an output that names a network fault is the substrate's (2, run it
// again); any other exit a command chose — 1 through 125 — is the tree's
// (1); 126 and 127 (the step's command could not be run or found) and a
// signal are the run's (2).
func ReleaseStepState(code int, out string) int {
	switch {
	case code == 0:
		return 0
	case networkFault.MatchString(out):
		return 2
	case code < 126:
		return 1
	}
	return 2
}

// GoReleaseArgs is one binary's compile, as an argv: the fleet's flags, vendor
// when the module vendors, and the output under ReleaseOut. It stamps no
// version: this is the Gate's compile, which runs before the commit that will
// carry the tag exists.
func GoReleaseArgs(b ReleaseBinary, vendored bool) []string {
	return GoReleaseStampedArgs(b, vendored, "", "")
}

// stampSafe is what a module path or version may be made of: it is spliced
// into one -ldflags string that go splits on spaces and quotes.
var stampSafe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/+~-]*$`)

// ReleaseStamp is the -X list that stamps version into a Go binary built from
// module (a go.mod's module path). THE LINKER IGNORES A -X NAMING A SYMBOL THE
// BINARY LACKS, so one list covers every convention the fleet's stars keep
// their version var in, measured 2026-10-15 over the Go stars carrying a
// string var: main.version (athena, techne, themis ...), main.Version
// (nereus), <module>/internal/config.Version (eros, kairos, harmonia),
// <module>/internal/verbs.Version (athena, iris, mnemosyne ...) and
// <module>/verbs.Version (chaos). A const cannot be set by -X (thalia,
// aletheia, ares ...): those stars own a change of their own.
func ReleaseStamp(module, version string) (string, error) {
	if !stampSafe.MatchString(version) {
		return "", fmt.Errorf("the version %q cannot be stamped into -ldflags", version)
	}
	if !stampSafe.MatchString(module) {
		return "", fmt.Errorf("the module path %q cannot be named in -ldflags", module)
	}
	var out []string
	for _, sym := range []string{"main.Version", "main.version", module + "/internal/config.Version", module + "/internal/verbs.Version", module + "/verbs.Version"} {
		out = append(out, "-X "+sym+"="+version)
	}
	return strings.Join(out, " "), nil
}

// GoReleaseStampedArgs is GoReleaseArgs with the version stamped in. An empty
// version stamps nothing (the Gate's compile). A version or module that
// ReleaseStamp refuses also stamps nothing here; the caller validated it
// first, so this is the argv builder and not the gate.
func GoReleaseStampedArgs(b ReleaseBinary, vendored bool, module, version string) []string {
	return GoReleaseArgsTo(ReleaseOut, b, vendored, module, version)
}

// GoReleaseArgsTo is GoReleaseStampedArgs with the output directory named: the
// lane's ReleaseOut for the engine's chain, the atom's own release directory
// for a pod. It is the one place the compile's argv is built.
func GoReleaseArgsTo(out string, b ReleaseBinary, vendored bool, module, version string) []string {
	args := []string{"go", "build"}
	if vendored {
		args = append(args, "-mod=vendor")
	}
	flags := slices.Clone(ReleaseFlags)
	if version != "" {
		if x, err := ReleaseStamp(module, version); err == nil {
			for i, f := range flags {
				if strings.HasPrefix(f, "-ldflags=") {
					flags[i] = f + " " + x
				}
			}
		}
	}
	args = append(args, flags...)
	return append(args, "-o", path.Join(out, b.Name), b.Package)
}

// RustReleaseTarget is where a Rust release build writes: the repository's
// release cache volume (ReleaseCacheFor has the measurement), not the gate's
// debug cargo-target. A binary in a cache mount is not in the container and
// could never be read back out (the cast lane measured it first), so the build
// copies each binary to ReleaseOut in the same exec (RustReleaseBuilt is the
// source, RustReleaseBinary the copy every reader reads).
const RustReleaseTarget = ReleaseCachePath

// RustReleaseArgs is one binary's compile, as an argv: the release profile,
// the package of that name, and --locked so a Cargo.lock behind its manifest
// is a finding rather than a silent re-resolve — the image's compile answers
// for the tree as committed.
func RustReleaseArgs(b ReleaseBinary) []string {
	return []string{"cargo", "build", "--release", "--locked", "-p", b.Package}
}

// RustReleaseBuilt is where cargo leaves one built binary, in the volume.
func RustReleaseBuilt(b ReleaseBinary) string {
	return RustReleaseBuiltIn(RustReleaseTarget, b)
}

// RustReleaseBuiltIn is where cargo leaves one built binary under a target
// directory: the volume's for the engine's chain, CARGO_TARGET_DIR's for a pod.
func RustReleaseBuiltIn(target string, b ReleaseBinary) string {
	return path.Join(target, "release", b.Name)
}

// RustReleaseBinary is where the build's own exec copied that binary, in the
// container: the path every reader of the release build reads.
func RustReleaseBinary(b ReleaseBinary) string {
	return path.Join(ReleaseOut, b.Name)
}

// ReleaseScope is the line the atom prints: what it built and where the names
// came from, so a reader never has to guess.
func ReleaseScope(p ReleasePlan) string {
	var names []string
	for _, b := range p.Binaries {
		names = append(names, b.Name)
	}
	how := "the module resolves its dependencies"
	switch {
	case p.Lane == LaneRust:
		how = "cargo builds the release profile against Cargo.lock (--locked)"
	case p.Vendored:
		how = "the module vendors (vendor/ is tracked, so -mod=vendor)"
	}
	return fmt.Sprintf("release build: %s, from the Dockerfile's COPY lines; %s", strings.Join(names, ", "), how)
}

// RepoKey is the custody key a clone URL names — `rob/hephaestus` for
// http://ourea…:8215/hephaestus.git and https://git.notusmi.com/hephaestus.git
// alike, `foundry/foundry-dies` for …/foundry/foundry-dies.git — the form a
// record's meta.repo carries. A bare name is the default owner's, as the door
// qualifies it. "" when the URL names no path.
//
// Cut, not Index: a position compared to zero is a boundary a mutant can
// move, and the three it moved on the first push were all here.
func RepoKey(cloneURL string) string {
	u := strings.TrimSpace(cloneURL)
	if _, after, ok := strings.Cut(u, "://"); ok {
		// scheme://host/path — the path is the key.
		_, p, _ := strings.Cut(after, "/")
		u = p
	} else if host, p, ok := strings.Cut(u, ":"); ok && !strings.Contains(host, "/") {
		// scp-like: host:path
		u = p
	}
	u = strings.Trim(u, "/")
	u = strings.TrimSuffix(u, ".git")
	if u == "" {
		return ""
	}
	if !strings.Contains(u, "/") {
		return "rob/" + u
	}
	return u
}

// RecordRepo reads repo off a catalog entry (fleet/repos/<name>.json) — the
// custody key of the repository the entry is about. "" when the entry does not
// parse or names none.
func RecordRepo(entry string) string {
	var rec struct {
		Repo string `json:"repo"`
	}
	if err := json.Unmarshal([]byte(entry), &rec); err != nil {
		return ""
	}
	return rec.Repo
}

// KindVendored is the record kind foundry-dies' fleet-star schema gives a fork
// of an upstream ("vendored: a fork of an upstream, exempt from conform").
const KindVendored = "vendored"

// RecordIsFork reports whether a catalog entry declares its repository a fork
// of an upstream: kind vendored. An entry that does not parse is not a fork,
// so a record nobody can read takes the fleet's whole bar, never less.
func RecordIsFork(entry string) bool {
	var rec struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(entry), &rec); err != nil {
		return false
	}
	return rec.Kind == KindVendored
}

// StarOfRecordPath is the name a catalog entry path carries:
// fleet/repos/<name>.json → <name>. "" for any other shape.
func StarOfRecordPath(p string) string {
	rest, ok := strings.CutPrefix(p, "fleet/repos/")
	if !ok {
		return ""
	}
	star, ok := strings.CutSuffix(rest, ".json")
	if !ok || star == "" || strings.Contains(star, "/") {
		return ""
	}
	return star
}
