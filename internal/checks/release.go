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

// ReleaseBinaries reads tools.build.binaries from a v3 record. A record that
// does not parse, or names none, leaves the convention to answer — the field
// exists for the two repos that ship more than their own name.
func ReleaseBinaries(slag string) []string {
	var rec struct {
		Tools struct {
			Build *struct {
				Binaries []string `json:"binaries"`
			} `json:"build"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(slag), &rec); err != nil || rec.Tools.Build == nil {
		return nil
	}
	return rec.Tools.Build.Binaries
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
