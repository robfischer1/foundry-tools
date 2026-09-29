package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// BASE IMAGES THROUGH THE IMAGE LANE. A repository whose tree has no root
// Dockerfile and one or more bases/<lang>/Dockerfile is a repository of base
// images (foundry/base-images), and the build lane builds each of them the way
// it builds a star: through Image, against the whole tree, with the same
// labels, g-pin, signature and SBOM. Rob, 2026-09-14: "stand up a thin dagger
// module that runs base images through the image lane and runs trivy at the
// end". Three things differ, and they are the whole of this file:
//
//  1. TRIVY GATES EVERY BASE before anything leaves the engine: a fixable
//     HIGH or CRITICAL vulnerability is a finding, on a pull as on a tip. A
//     base is what every star FROMs, so its CVEs are every star's.
//  2. A BASE IS PUSHED UNDER ITS REPOSITORY'S OWN PATH:
//     registry.notusmi.com/foundry/base-images/<lang>.
//  3. THE LANE MOVES :stable ITSELF. A star's :stable is its permit's output
//     (hephaestus' mold stamps it against the star's record), and mold is one
//     record to one image. A base is not a star — it ships no service, holds
//     no identity and deploys nothing — so the lane that built, scanned,
//     signed and verified it moves :stable to that digest. The tag keeps the
//     fleet's meaning (Rob, 2026-09-14: ":stable for the moving tag, to match
//     convention"): the last build that passed everything.
//
// Each base stands or builds on its own change set since its own :stable, so
// a Rust Dockerfile edit rebuilds rust and nothing else, and the shared
// stellar-boot source rebuilds them all.

// bases answers the base images the tree declares, sorted, or none when the
// tree has a root Dockerfile (a star, built as one).
func (l *buildLane) bases(ctx context.Context) ([]string, error) {
	root, err := l.m.Source.Glob(ctx, "Dockerfile")
	if err != nil {
		return nil, err
	}
	if len(root) > 0 {
		return nil, nil
	}
	found, err := l.m.Source.Glob(ctx, buildlane.BasesDir+"/*/Dockerfile")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, f := range found {
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(f, buildlane.BasesDir+"/"), "/Dockerfile"))
	}
	sort.Strings(out)
	return out, nil
}

// buildArgs reads the tree's .forgejo/build-args.env and adds the runner's
// python index, which a RUN sees only when it crosses the seam by name.
func (l *buildLane) buildArgs(ctx context.Context) ([]string, error) {
	var args []string
	if env, ok, err := fileIn(ctx, l.m.Source, ".forgejo/build-args.env"); err != nil {
		return nil, fmt.Errorf("could not read .forgejo/build-args.env: %v", err)
	} else if ok {
		args = buildlane.BuildArgs(env)
		say("build args: %d from .forgejo/build-args.env", len(args))
	}
	if l.indexURL != "" {
		args = append(args, "UV_INDEX_URL="+l.indexURL)
	}
	return args, nil
}

// runBases builds every base and settles on the worst of them: a base that
// could not run outranks one with findings, which outranks a clean one.
func (l *buildLane) runBases(ctx context.Context, star string, bases []string) (int, string) {
	l.say("%d base image(s) under %s/: %s", len(bases), buildlane.BasesDir, strings.Join(bases, ", "))
	args, err := l.buildArgs(ctx)
	if err != nil {
		return l.stop("build:detect", buildlane.CouldNotRun, err.Error())
	}
	// A FANOUT, NOT THE STAR SEQUENCE. Marked here so Unreached stays empty:
	// the bases that ran are the bases there were.
	l.fanout = true
	l.seal("build:detect", buildlane.Clean, fmt.Sprintf("%d base image(s) under %s/", len(bases), buildlane.BasesDir))
	worst := buildlane.Clean
	var lines []string
	for _, b := range bases {
		code, why := l.base(ctx, star, b, args)
		l.say("%s: %s", b, why)
		// ONE ATOM PER BASE, AND THE SEQUENCE DOES NOT STOP. Unlike the star
		// path, a base that fails does not spare the others — they are
		// independent images and the run grades all of them — so every base
		// seals its own verdict and the lane settles on the worst.
		l.seal(baseAtom(b), code, why)
		lines = append(lines, b+": "+why)
		worst = max(worst, code)
	}
	return worst, strings.Join(lines, "\n")
}

// base builds, scans and — on a tip — publishes, signs and promotes one base.
func (l *buildLane) base(ctx context.Context, star, base string, args []string) (int, string) {
	pushRepo := buildlane.BasePushRepo(l.registry, l.m.Repo, base)
	needed, why, code := l.detectWhere(ctx, pushRepo, func(changed string) string { return buildlane.BaseChanges(base, changed) })
	if code != buildlane.Clean {
		return code, why
	}
	if !needed {
		return buildlane.Clean, "stood down: " + why
	}
	src, code, why := l.relock(ctx, base)
	if code != buildlane.Clean {
		return code, why
	}
	bound := *l.m
	bound.Source = src
	img, err := bound.Image(ctx, l.m.Sha, l.sourceBase+"/"+star, star+"/"+base, args, buildlane.BaseDockerfile(base))
	if err != nil {
		return buildlane.CouldNotRun, err.Error()
	}
	if _, err := img.Container().Sync(ctx); err != nil {
		return buildlane.Failed("the image build", err.Error())
	}
	if code, why := l.scan(ctx, img); code != buildlane.Clean {
		return code, why
	}
	if !l.tip {
		return buildlane.Clean, fmt.Sprintf("clean: built and scanned %s at %.12s — nothing published; the landing does that", base, l.m.Sha)
	}
	name := star + "-" + base
	ref, code, why := l.publish(ctx, img, pushRepo, name)
	if code != buildlane.Clean {
		return code, why
	}
	if code, why := l.sign(ctx, img, ref, name); code != buildlane.Clean {
		return code, why
	}
	if code, why := l.stable(ctx, img, pushRepo, ref, name); code != buildlane.Clean {
		return code, why
	}
	return buildlane.Clean, fmt.Sprintf("clean: published, scanned and signed %s; :stable moved to it", ref)
}

// trivyReport is where the scan writes its JSON — a file, not stdout, because
// the engine echoes an exec's stdout into the lane's log.
const trivyReport = "/scan/report.json"

// scan runs trivy against the image as built. Trivy's own exit code is not the
// verdict — it exits non-zero on its own failures too — so the scan asks for
// exit 0 on findings and reads the verdict from the report.
func (l *buildLane) scan(ctx context.Context, img *Image) (int, string) {
	ctr := dag.Container().From(checks.ImageTrivy).
		WithEnvVariable("TRIVY_DB_REPOSITORY", checks.TrivyDBRepo).
		WithEnvVariable("TRIVY_JAVA_DB_REPOSITORY", checks.TrivyJavaDBRepo).
		WithEnvVariable("TRIVY_CACHE_DIR", "/cache").
		WithMountedCache("/cache", dag.CacheVolume("trivy-db")).
		// A fresh scan every run: without it the engine would answer a cached
		// report for the same image against an older database.
		WithEnvVariable("BUILD_RUN", l.stamp).
		WithMountedFile("/scan/image.tar", img.Tarball()).
		WithExec([]string{"image", "--input", "/scan/image.tar",
			"--scanners", "vuln", "--severity", "HIGH,CRITICAL", "--ignore-unfixed",
			"--exit-code", "0", "--no-progress", "--quiet", "--format", "json", "--output", trivyReport,
		}, entrypointAnyExit)
	out, code, err := output(ctx, ctr)
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: the scan did not run: %v", err)
	}
	if code != 0 {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: trivy exited %d: %.400s", code, out)
	}
	raw, err := ctr.File(trivyReport).Contents(ctx)
	if err != nil {
		return buildlane.CouldNotRun, fmt.Sprintf("could not run: trivy wrote no report: %v", err)
	}
	findings, err := buildlane.TrivyFindings([]byte(raw))
	if err != nil {
		return buildlane.CouldNotRun, "could not run: " + err.Error()
	}
	if len(findings) > 0 {
		return buildlane.Findings, fmt.Sprintf("findings in the scan: %d fixable HIGH or CRITICAL vulnerabilities:\n%s",
			len(findings), strings.Join(findings, "\n"))
	}
	say("scan clean: no fixable HIGH or CRITICAL vulnerabilities")
	return buildlane.Clean, ""
}

// stable moves <pushRepo>:stable to the image just published and signed, and
// holds the registry to it: the tag must name the digest the g-pin minted. The
// image's config is stamped once (Image.Created), so a second push of the same
// image is the same manifest, and a different digest is a different image.
func (l *buildLane) stable(ctx context.Context, img *Image, pushRepo, ref, name string) (int, string) {
	moved, code, why := l.push(ctx, img, pushRepo, pushRepo+":stable", name)
	if code != buildlane.Clean {
		return code, why
	}
	if moved != ref {
		return buildlane.Findings, fmt.Sprintf("findings in :stable: the push minted %s, not the signed %s", moved, ref)
	}
	say("moved %s:stable to %s", pushRepo, ref)
	return buildlane.Clean, ""
}

// relock answers the tree a base builds from: the tree as fetched, or — for a
// python base that ships a pyproject.toml and no uv.lock — the tree with a lock
// written into that base's directory.
//
// A PYTHON BASE WITH NO COMMITTED LOCK IS DECLARING "relock me", and that is a
// measured choice: foundry-stocks a0efc76 deleted the one committed base lock
// after finding it 27 days and ten minor versions stale. ca-bases carried the
// phase; the lane that replaces it must, or the Dockerfile's
// `uv sync --locked` fails at its bind mount ("uv.lock: not found", measured
// on the prototype before it carried this).
//
// THE LOCK AND THE SYNC MUST SEE ONE INDEX. The lock resolves against the
// lane's --index-url, the same index the build passes the Dockerfile as
// UV_INDEX_URL; resolved apart, the build rejects a lock written seconds
// earlier (foundry-stocks#7278).
func (l *buildLane) relock(ctx context.Context, base string) (*dagger.Directory, int, string) {
	dir := buildlane.BasesDir + "/" + base
	if _, ok, err := fileIn(ctx, l.m.Source, dir+"/pyproject.toml"); err != nil || !ok {
		return l.m.Source, buildlane.Clean, ""
	}
	if _, locked, err := fileIn(ctx, l.m.Source, dir+"/uv.lock"); err != nil || locked {
		return l.m.Source, buildlane.Clean, ""
	}
	lock := dag.Container().From(checks.ImagePython).
		WithFile("/usr/local/bin/uv", dag.Container().From(checks.ImageUV).File("/uv")).
		WithEnvVariable("UV_INDEX_URL", l.indexURL).
		// Not a lane container (laneBase), so the trust variable is named here:
		// the engine's CA reaches this container's system store like any other,
		// and uv reads it only with native TLS.
		WithEnvVariable("UV_NATIVE_TLS", "1").
		WithMountedDirectory("/base", l.m.Source.Directory(dir)).
		WithWorkdir("/base").
		WithExec([]string{"uv", "lock"}, anyExit)
	out, code, err := output(ctx, lock)
	if err != nil {
		return nil, buildlane.CouldNotRun, fmt.Sprintf("could not run: the relock did not run: %v", err)
	}
	if code != 0 {
		c, why := buildlane.Failed("the relock", out)
		return nil, c, why
	}
	say("%s: no committed uv.lock — relocked against %s", base, l.indexURL)
	return l.m.Source.WithDirectory(dir, lock.Directory("/base")), buildlane.Clean, ""
}
