package main

import (
	"context"
	"fmt"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE BUILD LANE'S IMAGE SCAN. This was the Verify stage (CA master-plan F14)
// until 2026-09-23, when the standalone stage was deleted under F18: NOTHING
// CALLED IT. Build never did — build.go's `(*buildLane).verify` constructs a
// verifyLane and calls `scan` directly, reaching past the stage — and the door
// dispatches no verify lane (zero `verify` entries in ourea-config's lane
// tables). The exported `Verify` function's only consumer was dagger.gen.go's
// own dispatch table. Its doc said it stayed "for the door's own run of the
// chain (F16)"; F16 landed without ever wiring it.
//
// WHAT SURVIVED IS THE MEASUREMENT, which was always the point and is
// unchanged: the image's fixable HIGH and CRITICAL findings less the ones its
// base already carries, gating the publish. `buildLane.run` refuses to push
// anything the scan did not pass, so the registry never holds it.
//
// `absent` AND `stageVerify` WENT WITH THE STAGE. A first pass at this landing
// kept them on the belief that ~50 files called `absent`. They do not: that
// was a word-match grep hitting the string "ABSENT" in other atoms' log lines,
// and `absent` had exactly ZERO call sites outside its own definition. The
// atoms announce an absent surface with checks.AbsentVerdict /
// checks.AbsentProblem, in the package that owns the vocabulary. staticcheck
// caught it at the push gate (U1000, both symbols) — the gate was right and
// the measurement behind the first pass was not.

type verifyLane struct {
	m                    *FoundryTools
	indexURL, sourceBase string
	// stamp keys the registry reads, which must not answer from an older run:
	// a base attaches its SBOM once, after its own publish, and a listing
	// cached from before that would keep saying none.
	stamp string
}

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

// announces one: `<atom>: ABSENT - <why>`.
