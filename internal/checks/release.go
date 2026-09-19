package checks

import (
	"encoding/json"
	"fmt"
	"path"
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
//	one binary   27 of 29 build exactly one, named for the star
//	more         2 do: blade-runner (+blade-controller), clio (+clio-consume, clio-query)
//
// So nothing is declared that can be derived: the flags are a constant, vendor
// is a directory, and the package is ./cmd/<star>. A record says something only
// when the repo ships MORE than its own name.
//
// AND "BUILD EVERY cmd/ DIRECTORY" IS THE TRAP THIS AVOIDS. helios carries
// cmd/helios-backfill, thalia carries cmd/calibration-fit, cmd/cognition-migrate
// and cmd/seamprobe, and NONE of those are in their images. A derivation that
// walked cmd/ would ship four binaries the fleet deliberately does not ship, so
// the walk is never the rule: the star's own name, plus what the record names.

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
	// Declared is whether the record named the binaries, rather than the
	// convention deriving the one.
	Declared bool
}

// GoReleasePlan derives a Go repo's release build: the star's own binary, or
// the ones its record declares, each from ./cmd/<name>.
//
// An empty star is an error rather than a guess — a binary with no name would
// land in the image as whatever the convention invented.
func GoReleasePlan(star string, declared []string, vendored bool) (ReleasePlan, error) {
	p, err := releasePlan(star, declared, LaneGo, func(n string) string { return "./cmd/" + n })
	if err != nil {
		return ReleasePlan{}, err
	}
	p.Vendored = vendored
	return p, nil
}

// RustReleasePlan derives a Rust repo's release build: the star's own binary,
// or the ones its record declares, each the workspace package of that name
// (`cargo build -p <name>`).
//
// MEASURED BEFORE DECIDING, 2026-09-19, the fleet's Rust star Dockerfiles and
// the template that pours them: `cargo build --release -p <star>` from a
// workspace whose binary crate is named for the star, the binary read back
// from target/release/<star>. One shape, so the package IS the name: a record
// speaks only when the repo ships more than its own name, exactly as for Go.
func RustReleasePlan(star string, declared []string) (ReleasePlan, error) {
	return releasePlan(star, declared, LaneRust, func(n string) string { return n })
}

// releasePlan is the two plans' shared half: the star's own name or the
// record's binaries, each name checked, each mapped to the package its lane
// builds it from, sorted by name.
func releasePlan(star string, declared []string, lane Lane, pkg func(string) string) (ReleasePlan, error) {
	if star == "" {
		return ReleasePlan{}, fmt.Errorf("the repository names no star, so its release build has no binary to name")
	}
	names := declared
	if len(names) == 0 {
		names = []string{star}
	}
	p := ReleasePlan{Star: star, Lane: lane, Declared: len(declared) > 0}
	seen := map[string]bool{}
	for _, n := range names {
		if n == "" || strings.ContainsAny(n, `/\`) || strings.HasPrefix(n, ".") || strings.HasPrefix(n, "-") {
			return ReleasePlan{}, fmt.Errorf("tools.build.binaries: %q is not a binary name", n)
		}
		if seen[n] {
			return ReleasePlan{}, fmt.Errorf("tools.build.binaries names %q twice", n)
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

// buildBlock is a v3 record's tools.build — one key per toolchain, and a
// record carries at most one (hephaestus slag.BuildV3 refuses two).
type buildBlock struct {
	Binaries []string   `json:"binaries"`
	Release  [][]string `json:"release"`
	Extras   []string   `json:"extras"`
}

// buildOf reads tools.build off a v3 record: the zero block for a record
// that does not parse or carries none, so every reader's absence is the
// convention's.
func buildOf(slag string) buildBlock {
	var rec struct {
		Tools struct {
			Build buildBlock `json:"build"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(slag), &rec); err != nil {
		return buildBlock{}
	}
	return rec.Tools.Build
}

// ReleaseBinaries reads tools.build.binaries from a v3 record. A record that
// does not parse, or names none, leaves the convention to answer — the field
// exists for the two repos that ship more than their own name.
func ReleaseBinaries(slag string) []string { return buildOf(slag).Binaries }

// THE BUN AND PYTHON RELEASES (CA F17). A compiled star's artifact is a
// binary; a bun star's is its bundle and a python star's is its venv, and
// both are BUILT ON THE IMAGE'S OWN BASE — the base the Dockerfile's runtime
// stage FROMs — so the interpreter, the runtime and the paths baked into the
// artifact are the ones the image runs. A venv built on another python is a
// venv whose every shebang and symlink points at an interpreter the image
// does not have.
//
// WHAT THE RECORD SAYS, AND ONLY THAT. A bun star's bundle is its own
// (calliope bundles one server.js, demeter builds a SPA beside it), so there
// is no convention and the record names the steps — tools.build.release, one
// argv each (Rob, 2026-09-19: "argv fine"). A python star's release derives
// entirely but for the extras its venv installs — tools.build.extras.

// ReleaseTree is where a bun release's tree sits in its container, and so
// where the steps run; the steps leave the image's files under
// ReleaseTree/release.
const ReleaseTree = "/src"

// PythonReleaseApp is where a python release's venv is built — /app, the
// directory the image carries it at, because a venv is not relocatable: its
// console scripts name <PythonReleaseApp>/.venv/bin/python in their shebang.
const PythonReleaseApp = "/app"

// TSReleaseSteps reads tools.build.release from a v3 record: the bun
// release's steps, one argv each. None is not a convention — the bun atom
// says so rather than guessing a bundle.
func TSReleaseSteps(slag string) [][]string { return buildOf(slag).Release }

// PythonExtras reads tools.build.extras from a v3 record. None is the
// convention: the venv installs the project and its required dependencies.
func PythonExtras(slag string) []string { return buildOf(slag).Extras }

// PythonReleaseArgs is the python release, as an argv: the star's own lock
// (--locked, so a uv.lock behind its pyproject is a finding and not a silent
// re-resolve), no dev group, and the project installed as a WHEEL rather than
// editable — the image carries the venv alone, never the source tree an
// editable install would point back into.
func PythonReleaseArgs(extras []string) []string {
	args := []string{"uv", "sync", "--locked", "--no-dev", "--no-editable"}
	for _, e := range extras {
		args = append(args, "--extra", e)
	}
	return args
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
// when the module vendors, and the output under ReleaseOut.
func GoReleaseArgs(b ReleaseBinary, vendored bool) []string {
	args := []string{"go", "build"}
	if vendored {
		args = append(args, "-mod=vendor")
	}
	args = append(args, ReleaseFlags...)
	return append(args, "-o", path.Join(ReleaseOut, b.Name), b.Package)
}

// RustReleaseTarget is where a Rust release build writes, inside the lane:
// the container's own filesystem, not the lane's cargo-target cache volume,
// because a binary in a cache mount is not in the container and could never
// be read back out (the cast lane measured it first). Cargo puts the binary
// at <target>/release/<name>; RustReleaseBinary names it.
const RustReleaseTarget = "/work/target"

// RustReleaseArgs is one binary's compile, as an argv: the release profile,
// the package of that name, and --locked so a Cargo.lock behind its manifest
// is a finding rather than a silent re-resolve — the image's compile answers
// for the tree as committed.
func RustReleaseArgs(b ReleaseBinary) []string {
	return []string{"cargo", "build", "--release", "--locked", "-p", b.Package}
}

// RustReleaseBinary is where cargo left one built binary.
func RustReleaseBinary(b ReleaseBinary) string {
	return path.Join(RustReleaseTarget, "release", b.Name)
}

// ReleaseScope is the line the atom prints: what it built and where the names
// came from, so a reader never has to guess whether a record spoke.
func ReleaseScope(p ReleasePlan) string {
	var names []string
	for _, b := range p.Binaries {
		names = append(names, b.Name)
	}
	source := "the star's own name (no tools.build.binaries in the record)"
	if p.Declared {
		source = "tools.build.binaries in the record"
	}
	how := "the module resolves its dependencies"
	switch {
	case p.Lane == LaneRust:
		how = "cargo builds the release profile against Cargo.lock (--locked)"
	case p.Vendored:
		how = "the module vendors (vendor/ is tracked, so -mod=vendor)"
	}
	return fmt.Sprintf("release build: %s, from %s; %s", strings.Join(names, ", "), source, how)
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

// RecordRepo reads meta.repo off a v3 record — the custody key of the
// repository the record is about. "" when the record does not parse or
// names none.
func RecordRepo(slag string) string {
	var rec struct {
		Meta struct {
			Repo string `json:"repo"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(slag), &rec); err != nil {
		return ""
	}
	return rec.Meta.Repo
}

// StarOfRecordPath is the star a record path names: fleet/stars/<star>/slag.json
// → <star>. "" for any other shape.
func StarOfRecordPath(p string) string {
	rest, ok := strings.CutPrefix(p, "fleet/stars/")
	if !ok {
		return ""
	}
	star, ok := strings.CutSuffix(rest, "/slag.json")
	if !ok || star == "" || strings.Contains(star, "/") {
		return ""
	}
	return star
}
