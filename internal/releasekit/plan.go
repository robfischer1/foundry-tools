package releasekit

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
)

// Subject is the image a release atom was asked about: the star, and the first
// tracked Dockerfile that asks for release/ with its body.
type Subject struct {
	Star       string
	Dockerfile string // the path
	Body       string
}

// Dockerfiles is the tracked Dockerfiles of a population.
func Dockerfiles(files []string) []string { return checks.DockerfilePopulation(files) }

// ReleaseDockerfile is the first tracked Dockerfile that asks for release/,
// with its body — "" when none asks. A Dockerfile under bases/ is not a star's. A Dockerfile that cannot be read is an
// error, never "does not ask".
func ReleaseDockerfile(ctx context.Context, t Tree, dockerfiles []string) (string, string, error) {
	for _, p := range dockerfiles {
		// A base's Dockerfile is the per-base release's: bases:release and
		// bases:go-release build what it copies (DEF-B6-037).
		if strings.HasPrefix(p, BasesDir+"/") {
			continue
		}
		body, err := t.File(ctx, p)
		if err != nil {
			return "", "", fmt.Errorf("%s could not be read: %w", p, err)
		}
		if buildlane.CopiesRelease(body) {
			return p, body, nil
		}
	}
	return "", "", nil
}

// What an image that asks for no release does for itself, in the words each
// atom's absence has always used: a compiled star's Dockerfile compiles, a bun
// or python star's builds.
const (
	Compiles = "compiles"
	Builds   = "builds"
)

// Resolve is the question every release atom asks first: is this a star image
// that asks for a release build? It answers the subject, or the verdict that
// stands the atom down — an absence where the image is not this atom's, a
// could-not-run where the question itself could not be answered. itself is the
// verb the absence uses for an image that makes its own artifact (Compiles or
// Builds). The order is
// the contract: the tree, the Dockerfiles, the star, then the Dockerfile that
// asks, so a repository that is not a star says so even when its Dockerfile
// would not have asked either.
func Resolve(ctx context.Context, t Tree, a checks.AtomDef, origin, itself string) (Subject, *checks.Verdict) {
	files, err := t.Population(ctx)
	if err != nil {
		return Subject{}, stand(a, 2, a.ID+": CANNOT RUN - the tree could not be read: "+err.Error())
	}
	dockerfiles := Dockerfiles(files)
	if len(dockerfiles) == 0 {
		return Subject{}, stand(a, 0, a.ID+": ABSENT - this repository tracks no Dockerfile or Containerfile, so it ships no image and has no release build")
	}
	star, err := StarName(ctx, t, origin)
	if err != nil {
		if !IsNotAStar(err) {
			return Subject{}, stand(a, 2, a.ID+": CANNOT RUN - "+err.Error())
		}
		// NOT A STAR, NOT A STAR IMAGE, ABSENT. Measured on foundry-stocks the
		// afternoon go:release landed (Wonka17's record, 2026-09-17, nine gate
		// runs on #205): the forge ships images — the CI bases, blade-base,
		// forge-tools — but it is not copier-templated, carries no
		// .copier-answers.yml, and names no star. The first cut read "names no
		// star" as a could-not-run and wedged every landing there. A repo with
		// Dockerfiles and no star identity builds its images through other
		// lanes; the STAR release build has nothing to say about it.
		return Subject{}, stand(a, 0, a.ID+": ABSENT - "+err.Error()+", so this is not a star image and there is no star release build")
	}
	// THE COMPILE IS ASKED FOR BY THE DOCKERFILE, and a Dockerfile that does not
	// ask is an ABSENCE. The build lane reads one contract off the Dockerfile
	// (buildlane.CopiesRelease; build.go stageRelease): a COPY from release/ asks
	// for the Gate's artifact, and a Dockerfile that carries its own build stage
	// copies nothing from release/ and is built exactly as before. A release
	// build the image never copies is a compile nobody asked for — and it is the
	// compile that reds a cgo star (narcissus, foundry-tools #10307).
	path, body, err := ReleaseDockerfile(ctx, t, dockerfiles)
	if err != nil {
		return Subject{}, stand(a, 2, a.ID+": CANNOT RUN - "+err.Error())
	}
	if path == "" {
		return Subject{}, stand(a, 0, a.ID+": ABSENT - no tracked Dockerfile copies from "+buildlane.ReleaseDir+"/, so this image "+itself+" itself: its build is the build lane's, and there is no release build to make here")
	}
	return Subject{Star: star, Dockerfile: path, Body: body}, nil
}

// OnBase narrows a subject to the atom whose toolchain made the image: the base
// the Dockerfile's runtime stage FROMs decides which release runs. A tree can
// declare two toolchains — mnemosyne carries a go.mod and the pyproject its Go
// port left behind, on the go base — and planning runs every lane the tree
// declares, so each base-built atom stands down unless the image is on its own
// base. It answers the base reference to build on, or the verdict that stands
// the atom down.
func OnBase(a checks.AtomDef, s Subject, want string) (string, *checks.Verdict) {
	ref, _, ok := buildlane.RuntimeBase(s.Body)
	if !ok || buildlane.BaseToolchain(ref) != want {
		on := ref
		if !ok {
			on = "a base this lane cannot name"
		}
		return "", stand(a, 0, a.ID+": ABSENT - "+s.Dockerfile+" is built on "+on+", not "+buildlane.FleetBases+want+", so its release is another lane's")
	}
	return ref, nil
}

// stand is a verdict that ends an atom before it builds.
func stand(a checks.AtomDef, state int, why string) *checks.Verdict {
	v := checks.VerdictOf(a, state, why)
	return &v
}

// GoPlan derives what the star's Go release build produces: the binaries its
// Dockerfile copies out of release/, each from a ./cmd/<name> the tree must
// carry, and whether the module vendors. Every refusal is about the repository
// and settles as a could-not-run; the string is the refusal, "" for none.
func GoPlan(ctx context.Context, t Tree, s Subject) (checks.ReleasePlan, string) {
	vendored := false
	if entries, err := t.Entries(ctx); err == nil {
		vendored = slices.Contains(entries, "vendor/")
	}
	plan, why := goPlanOf(ctx, t, s, vendored)
	if why != "" {
		return checks.ReleasePlan{}, why
	}
	// A COPY with no main package behind it would ship a half image: say so
	// here, by name, instead of as the compiler's "directory not found".
	for _, b := range plan.Binaries {
		if b.Package == RootPackage {
			continue // GoRoot already requires the root's go.mod
		}
		dir := strings.TrimPrefix(b.Package, "./")
		ok, err := t.IsDir(ctx, dir)
		if err != nil {
			return checks.ReleasePlan{}, fmt.Sprintf("the tree could not be read for %s: %v", dir, err)
		}
		if !ok && plan.Manifest {
			return checks.ReleasePlan{}, fmt.Sprintf("%s declares %s and the tree carries no %s to build it from", ManifestFile, b.Package, dir)
		}
		if !ok {
			return checks.ReleasePlan{}, fmt.Sprintf("the Dockerfile copies release/%s and the tree carries no %s to build it from", b.Name, dir)
		}
	}
	return plan, ""
}

// goPlanOf is the plan before the tree is checked: the tree's release.toml when
// it carries one, else the Dockerfile's COPY lines. A manifest that does not
// build a binary the Dockerfile copies is refused, so the image never copies a
// file nothing made.
func goPlanOf(ctx context.Context, t Tree, s Subject, vendored bool) (checks.ReleasePlan, string) {
	copies := buildlane.ReleaseCopies(s.Body)
	body, declared := t.Present(ctx, ManifestFile)
	if !declared {
		plan, err := checks.GoReleasePlan(s.Star, copies, vendored)
		if err != nil {
			return checks.ReleasePlan{}, err.Error()
		}
		return plan, ""
	}
	bins, err := ReadManifest(body)
	if err != nil {
		return checks.ReleasePlan{}, err.Error()
	}
	for _, c := range copies {
		if !slices.ContainsFunc(bins, func(b checks.ReleaseBinary) bool { return b.Name == c }) {
			return checks.ReleasePlan{}, fmt.Sprintf("the Dockerfile copies release/%s and %s declares no such binary", c, ManifestFile)
		}
	}
	return checks.ReleasePlan{Star: s.Star, Lane: checks.LaneGo, Binaries: bins, Vendored: vendored, Manifest: true}, ""
}

// RustPlan derives what the star's Rust release build produces: the binaries
// its Dockerfile copies out of release/, each the workspace package of that
// name. A COPY with no workspace package behind it fails `cargo build -p` as a
// finding about the tree. Every refusal is about the repository and settles as
// a could-not-run; the string is the refusal, "" for none.
func RustPlan(s Subject) (checks.ReleasePlan, string) {
	plan, err := checks.RustReleasePlan(s.Star, buildlane.ReleaseCopies(s.Body))
	if err != nil {
		return checks.ReleasePlan{}, err.Error()
	}
	return plan, ""
}

// GoRoot is the check a Go compile makes before it starts: the tree's modules
// can be enumerated and one of them is the root's. The convention is the STAR's
// binary and a star is one module; a repo whose root carries no go.mod has no
// image to build from it. The error is worded for a could-not-run.
func GoRoot(ctx context.Context, t Tree) error {
	files, err := t.Population(ctx, "**/go.mod")
	if err != nil {
		return fmt.Errorf("could not enumerate the tree's Go modules: %w", err)
	}
	if !slices.Contains(checks.GoModuleDirs(files), ".") {
		return fmt.Errorf("no go.mod at the repository root, so there is no star binary to build")
	}
	return nil
}
