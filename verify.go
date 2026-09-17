package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE VERIFY STAGE (CA master-plan F14). The image, scanned for what the star
// itself can fix, and inventoried: two atoms over the built image, settled as
// one stage answer like Check and Push. It runs with no secrets, on the image
// the engine builds from the tree — the same chain Build publishes from, so a
// Verify that passed here passes on the bytes the registry will hold.
//
// WHAT IT GATES ON IS THE STAR'S OWN LAYER (internal/buildlane/verify.go has
// the measurement): the base's findings are the base lane's, listed but never
// counted. What it records is the composed SBOM (build_sbom.go): the star's
// own components, the base's document linked.

// The two atoms' ids. `image:` is a surface namespace like compose or dies:
// a tree with no star image has neither atom, and says so.
const (
	atomScan    = "image:trivy"
	atomSBOM    = "image:sbom"
	stageVerify = "verify"
)

// Verify is the verify stage: the tree's star image, scanned and inventoried.
//
// NEVER CACHED AS A WHOLE, for Verdicts' reason: an atom that could not run is
// still a successful return. The scans are cached by the image they scanned.
//
// +cache="never"
func (m *FoundryTools) Verify(
	ctx context.Context,
	// The python index a Dockerfile RUN reads as UV_INDEX_URL, as Build takes it.
	// +optional
	indexURL string,
	// Where org.opencontainers.image.source points: <sourceBase>/<star>.
	// +optional
	// +default="https://forgejo.notusmi.com/rob"
	sourceBase string,
) (*StageResult, error) {
	v := &verifyLane{m: m, indexURL: indexURL, sourceBase: sourceBase, stamp: strconv.FormatInt(time.Now().UnixNano(), 10)}
	vs, err := v.run(ctx)
	if err != nil {
		return nil, err
	}
	return stageResult(checks.SettleStage(stageVerify, vs)), nil
}

type verifyLane struct {
	m                    *FoundryTools
	indexURL, sourceBase string
	// stamp keys the registry reads, which must not answer from an older run:
	// a base attaches its SBOM once, after its own publish, and a listing
	// cached from before that would keep saying none.
	stamp string
}

// run answers both atoms' verdicts.
func (v *verifyLane) run(ctx context.Context) ([]checks.Verdict, error) {
	m := v.m
	if m.Repo == "" || m.Sha == "" {
		why := "the verify stage scans the image of a commit the engine fetched — construct the module with --repo and --sha"
		return []checks.Verdict{atomVerdict(atomScan, buildlane.CouldNotRun, why), atomVerdict(atomSBOM, buildlane.CouldNotRun, why)}, nil
	}
	root, err := m.Source.Glob(ctx, "Dockerfile")
	if err != nil {
		return nil, err
	}
	if len(root) == 0 {
		bases, err := m.Source.Glob(ctx, buildlane.BasesDir+"/*/Dockerfile")
		if err != nil {
			return nil, err
		}
		why := "this tree ships no image: no Dockerfile at its root"
		if len(bases) > 0 {
			why = fmt.Sprintf("this tree forges %d base image(s), and the bases lane scans each of those itself", len(bases))
		}
		return []checks.Verdict{absent(atomScan, why), absent(atomSBOM, why)}, nil
	}

	star := starOf(m.Repo)
	args, err := (&buildLane{m: m, indexURL: v.indexURL}).buildArgs(ctx)
	if err != nil {
		return []checks.Verdict{atomVerdict(atomScan, buildlane.CouldNotRun, err.Error()), atomVerdict(atomSBOM, buildlane.CouldNotRun, err.Error())}, nil
	}
	img, err := m.Image(ctx, m.Sha, v.sourceBase+"/"+star, star, args, "")
	if err != nil {
		return []checks.Verdict{atomVerdict(atomScan, buildlane.CouldNotRun, err.Error()), atomVerdict(atomSBOM, buildlane.CouldNotRun, err.Error())}, nil
	}
	if _, err := img.Container().Sync(ctx); err != nil {
		code, why := buildlane.Failed("the image build", err.Error())
		return []checks.Verdict{atomVerdict(atomScan, code, why), atomVerdict(atomSBOM, code, why)}, nil
	}

	scanCode, scanWhy := v.scan(ctx, img)
	oras, err := orasIn(ctx, nil)
	if err != nil {
		return []checks.Verdict{atomVerdict(atomScan, scanCode, scanWhy), atomVerdict(atomSBOM, buildlane.CouldNotRun, "could not run: "+err.Error())}, nil
	}
	_, note, sbomCode, sbomWhy := sbomOf(ctx, m.Source, img, oras, v.stamp)
	if sbomCode == buildlane.Clean {
		sbomWhy = note
	}
	return []checks.Verdict{atomVerdict(atomScan, scanCode, scanWhy), atomVerdict(atomSBOM, sbomCode, sbomWhy)}, nil
}

// scan is the trivy atom: the image's fixable HIGH and CRITICAL findings, less
// the ones its base already carries.
//
// THE BASE IS SCANNED TOO, by its digest, so the subtraction is exact: the
// engine caches that scan by the base it read, so every star on the same base
// pays for it once. A Dockerfile that pins no base has nothing to subtract and
// every finding is the star's — which is the right answer for an image built
// FROM scratch, and a loud one for a star that forgot to pin.
func (v *verifyLane) scan(ctx context.Context, img *Image) (int, string) {
	image, code, why := v.trivy(ctx, img.Tarball(), "image")
	if code != buildlane.Clean {
		return code, why
	}
	var inherited []string
	if img.BaseDigest != "" {
		at := buildlane.RepoOf(img.Base) + "@" + img.BaseDigest
		base, code, why := v.trivy(ctx, dag.Container().From(at).AsTarball(dagger.ContainerAsTarballOpts{MediaTypes: dagger.ImageMediaTypesOcimediaTypes}), "base")
		if code != buildlane.Clean {
			return code, why
		}
		image, inherited = buildlane.SplitFindings(image, base)
	}
	return buildlane.ScanVerdict(image, inherited, img.Base)
}

// trivy scans one OCI tarball with the base lane's flags — the fleet's mirrored
// database, HIGH and CRITICAL, fixable only — and answers the sorted finding
// lines. Trivy's own exit code is not the verdict: it exits non-zero on its
// own failures too, so the scan asks for exit 0 on findings and reads the
// report. A scan that did not run is could-not-run, never a pass.
//
// The tarball is mounted and the report written under the name of what is
// scanned — image or base — so the two scans are two execs to the engine and
// two reports, never one cached for the other.
func (v *verifyLane) trivy(ctx context.Context, tarball *dagger.File, what string) ([]string, int, string) {
	tar, report := "/scan/"+what+".tar", "/scan/"+what+".json"
	ctr := dag.Container().From(checks.ImageTrivy).
		WithEnvVariable("TRIVY_DB_REPOSITORY", checks.TrivyDBRepo).
		WithEnvVariable("TRIVY_JAVA_DB_REPOSITORY", checks.TrivyJavaDBRepo).
		WithEnvVariable("TRIVY_CACHE_DIR", "/cache").
		WithMountedCache("/cache", dag.CacheVolume("trivy-db")).
		WithMountedFile(tar, tarball).
		WithExec([]string{"image", "--input", tar,
			"--scanners", "vuln", "--severity", "HIGH,CRITICAL", "--ignore-unfixed",
			"--exit-code", "0", "--no-progress", "--quiet", "--format", "json", "--output", report,
		}, entrypointAnyExit)
	out, code, err := output(ctx, ctr)
	if err != nil {
		return nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: the scan of the %s did not run: %v", what, err)
	}
	if code != 0 {
		return nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: trivy exited %d scanning the %s: %.400s", code, what, out)
	}
	raw, err := ctr.File(report).Contents(ctx)
	if err != nil {
		return nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: trivy wrote no report for the %s: %v", what, err)
	}
	findings, err := buildlane.TrivyFindings([]byte(raw))
	if err != nil {
		return nil, buildlane.CouldNotRun, "could not run: " + err.Error()
	}
	return findings, buildlane.Clean, ""
}

// atomVerdict is one atom's answer in the verify stage, from the lane's code.
func atomVerdict(atom string, code int, reason string) checks.Verdict {
	s := checks.StateFor(code)
	return checks.Verdict{Atom: atom, Stage: stageVerify, Lane: "image", State: int(s), Result: s.String(), Reason: atom + ": " + strings.TrimSpace(reason)}
}

// absent is an atom with no surface in this tree, in the shape every atom
// announces one: `<atom>: ABSENT - <why>`.
func absent(atom, why string) checks.Verdict {
	return checks.Verdict{Atom: atom, Stage: stageVerify, Lane: "image", State: 0, Result: "absent", Reason: atom + ": ABSENT - " + why}
}
