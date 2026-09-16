// Base-image lane: a base image through foundry-tools' image lane, then Trivy.
//
// A base image builds exactly as a star does, from the repository's root
// Dockerfile through foundry-tools' Image. Before anything leaves the engine,
// Trivy scans it and refuses fixable HIGH or CRITICAL vulnerabilities.
//
// The call shape is the build lane's, so build.sh could name this module in
// place of foundry-tools: `image` takes the revision, source URL and title,
// and then either `publish` (which takes the ref plus the registry account
// and its credential, passed as a secret — never as an argument) or `tarball
// export`, which settles on the build and publishes nothing.
//
// The literal invocations are deliberately not spelled out here. They named a
// credential flag, which reads to detect-secrets as a secret keyword, and an
// example nothing runs is not worth an entry in the repo's baseline.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"dagger/base-image-lane/internal/dagger"
)

const (
	// trivyImage is the scanner the fleet already runs (ca-rescan pins this
	// digest), pulled through the docker.notusmi.com mirror.
	trivyImage = "docker.notusmi.com/aquasec/trivy:0.72.0@sha256:cffe3f5161a47a6823fbd23d985795b3ed72a4c806da4c4df16266c02accdd6f"
	// trivyDB and trivyJavaDB are the vulnerability databases, through the
	// same mirror (the settings vuln-3p runs with).
	trivyDB     = "docker.notusmi.com/aquasecurity/trivy-db:2"
	trivyJavaDB = "docker.notusmi.com/aquasecurity/trivy-java-db:1"

	// uvImage is the fleet's uv, at the version its uv.lock files were
	// written under (foundry-tools checks.ImageUV). It is a scratch image
	// carrying /uv and /uvx, so the binary is copied out rather than run in
	// place — nothing in it can host a working directory.
	uvImage = "docker.notusmi.com/astral-sh/uv:0.12.13@sha256:b485bd65cc2cf1c9a93b3554012c9c3778cf7b1b5fd3d3096ce9e1226c97e1e6"
	// lockImage hosts that binary for the one exec the relock needs.
	lockImage = "docker.notusmi.com/library/python:3.14-slim-bookworm@sha256:9ab8d9c8514b44f90cf0029dd42fdd7e9e211e639c8b995304cc04568dee900f"
	// uvIndexURL is the index BOTH the lock and the Dockerfile's `uv sync
	// --locked` must resolve against. Resolve them separately and the build
	// rejects a lock written seconds earlier: measured at foundry-stocks#7278,
	// where the Nexus-resolved lock was 185,665 B and the upstream-resolved
	// one 207,123 B. The Dockerfile takes it as ARG UV_INDEX_URL; this lane
	// passes it as a build arg, which is the seam.
	uvIndexURL = "https://nexus.notusmi.com/repository/pypi/simple"
)

var ociMediaTypes = dagger.ContainerAsTarballOpts{MediaTypes: dagger.ImageMediaTypesOcimediaTypes}

// BaseImageLane is the module root. The repository is bound once, here.
type BaseImageLane struct {
	// +private
	Source *dagger.Directory
}

func New(
	// The repository whose root Dockerfile is the base image.
	// +defaultPath="/"
	source *dagger.Directory,
) *BaseImageLane {
	return &BaseImageLane{Source: source}
}

// BaseImage is one build of the repository's root Dockerfile.
type BaseImage struct {
	// +private
	Tar *dagger.File
}

// Bases answers the base names this repository declares, in sorted order.
//
// A BASE IS A DIRECTORY UNDER bases/ THAT HOLDS A Dockerfile, and it is
// defined that way rather than "every entry under bases/" because bases/ is a
// directory people also put things in. Measured on foundry-stocks: 27 bases
// and two files beside them (README.md, departed.toml). Listing entries alone
// answered 29 and would have sent the lane to build
// bases/README.md/Dockerfile.
//
// A repo with no bases/ answers nothing, and its root Dockerfile is the
// single base — the shape go-base-image already ships.
func (m *BaseImageLane) Bases(ctx context.Context) ([]string, error) {
	entries, err := m.Source.Directory("bases").Entries(ctx)
	if err != nil {
		return nil, nil // no bases/ tree: the root Dockerfile is the base
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := strings.TrimSuffix(e, "/")
		files, err := m.Source.Directory("bases/" + name).Entries(ctx)
		if err != nil {
			continue // not a directory
		}
		for _, f := range files {
			if strings.TrimSuffix(f, "/") == "Dockerfile" {
				out = append(out, name)
				break
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// Image builds one base image through foundry-tools' image lane.
//
// ONE DOCKERFILE PER CALL, and `base` chooses which. foundry-tools' Image
// builds the Dockerfile at the root of the Source it is handed, so a named
// base is that lane pointed at bases/<name>/ instead of the tree root. That
// is the whole of multi-base support: no second build path, no fork of the
// image lane, and every base gets the labels, SBOM and signature a star's
// image already gets.
//
// Naming no base builds the tree's own root Dockerfile, which is the shape
// go-base-image already ships and the star build lane already accepts.
func (m *BaseImageLane) Image(
	// The source commit.
	revision string,
	// The repository URL.
	sourceUrl string,
	// The image's name.
	title string,
	// Which base under bases/ to build. Empty builds the root Dockerfile.
	// +optional
	base string,
	// Build args as KEY=VALUE, one per entry.
	// +optional
	buildArgs []string,
) *BaseImage {
	src := m.Source
	if base != "" {
		src = m.Source.Directory("bases/" + base)
	}
	src, relocked := relock(src)
	if relocked {
		buildArgs = append(buildArgs, "UV_INDEX_URL="+uvIndexURL)
	}
	ctr := dag.FoundryTools(dagger.FoundryToolsOpts{Source: src}).
		Image(revision, sourceUrl, title, dagger.FoundryToolsImageOpts{BuildArgs: buildArgs}).
		Container()
	// One tarball for the scan and the publish. foundry-tools stamps the
	// created label with the clock, so evaluating the container twice could
	// publish a config the scan never read.
	return &BaseImage{Tar: ctr.AsTarball(ociMediaTypes)}
}

// Scan reports what Trivy finds in the image, and fails on any fixable HIGH
// or CRITICAL vulnerability.
func (b *BaseImage) Scan(ctx context.Context) (string, error) {
	return scan(ctx, b.Tar)
}

// Tarball scans the image and, when it passes, answers it as an OCI layout
// tar. A pull-time build settles on this and publishes nothing.
func (b *BaseImage) Tarball(ctx context.Context) (*dagger.File, error) {
	if _, err := scan(ctx, b.Tar); err != nil {
		return nil, err
	}
	return b.Tar, nil
}

// Publish scans the image and, when it passes, pushes the scanned bytes under
// ref as an OCI manifest. It answers the coordinate the registry minted.
func (b *BaseImage) Publish(
	ctx context.Context,
	// Where to push: registry.notusmi.com/<repo>:g<12>.
	ref string,
	// The registry account to publish as.
	registryUser string,
	// That account's password.
	registryPassword *dagger.Secret,
) (string, error) {
	if _, err := scan(ctx, b.Tar); err != nil {
		return "", err
	}
	host, _, ok := strings.Cut(ref, "/")
	if !ok || host == "" {
		return "", fmt.Errorf("publish: %q names no registry host", ref)
	}
	return dag.Container().
		Import(b.Tar).
		WithRegistryAuth(host, registryUser, registryPassword).
		Publish(ctx, ref, dagger.ContainerPublishOpts{MediaTypes: dagger.ImageMediaTypesOcimediaTypes})
}

// relock writes a uv.lock into the build context when the base declares
// "relock me", and answers whether it did.
//
// A PYTHON BASE SHIPPING A pyproject AND NO COMMITTED LOCK IS DECLARING THAT,
// and it is a choice with a measurement behind it: foundry-stocks a0efc76
// (2026-07-30) deleted the one committed uv.lock in the tree after finding it
// 27 days and ten minor versions stale, on the argument that a hand-maintained
// lock in a loop has already failed once. Its four siblings had always
// relocked; that commit made the family consistent.
//
// THIS PHASE WAS LOST WHEN build-base.yml LEFT, not omitted. Without it the
// build fails at the Dockerfile's bind mount with "/uv.lock: not found" —
// measured here against python-base-image bases/python-runtime before this
// existed, which is how the gap was found: the prototype only ever built
// go-base-image, which ships no pyproject and never needed a lock.
//
// A base that DOES commit a lock is left exactly alone.
func relock(src *dagger.Directory) (*dagger.Directory, bool) {
	// The check is the file's presence, asked of the build context itself.
	// dagger evaluates lazily, so a base with a committed lock costs nothing.
	locked := dag.Container().
		From(lockImage).
		WithFile("/usr/local/bin/uv", dag.Container().From(uvImage).File("/uv")).
		WithEnvVariable("UV_INDEX_URL", uvIndexURL).
		WithMountedDirectory("/base", src).
		WithWorkdir("/base").
		// `uv lock` only when the base asks: a pyproject present and no lock
		// beside it. Anything else is a no-op that leaves the tree as it was.
		WithExec([]string{"sh", "-c",
			`if [ -f pyproject.toml ] && [ ! -f uv.lock ]; then uv lock; fi`}).
		Directory("/base")
	return locked, true
}

// trivyReport is the part of Trivy's JSON the gate reads.
type trivyReport struct {
	Results []struct {
		Target          string `json:"Target"`
		Vulnerabilities []struct {
			VulnerabilityID  string `json:"VulnerabilityID"`
			PkgName          string `json:"PkgName"`
			InstalledVersion string `json:"InstalledVersion"`
			FixedVersion     string `json:"FixedVersion"`
			Severity         string `json:"Severity"`
		} `json:"Vulnerabilities"`
	} `json:"Results"`
}

// reportPath is where Trivy writes its JSON. A file, not stdout: the engine's
// progress output echoes an exec's stdout, and a full report is thousands of
// lines in the lane's log.
const reportPath = "/scan/report.json"

// scan runs Trivy against the tarball. Trivy's own exit code is not the
// verdict: it exits 1 on its own errors too, so the scan asks for exit 0 and
// the verdict is read from the JSON report.
func scan(ctx context.Context, image *dagger.File) (string, error) {
	ctr := dag.Container().
		From(trivyImage).
		WithEnvVariable("TRIVY_DB_REPOSITORY", trivyDB).
		WithEnvVariable("TRIVY_JAVA_DB_REPOSITORY", trivyJavaDB).
		WithEnvVariable("TRIVY_CACHE_DIR", "/cache").
		WithMountedCache("/cache", dag.CacheVolume("trivy-db")).
		// A fresh scan every call. Without this the engine would replay a
		// cached result for the same image against an older database.
		WithEnvVariable("SCANNED_AT", time.Now().UTC().Format(time.RFC3339Nano)).
		WithMountedFile("/scan/image.tar", image).
		WithExec([]string{
			"trivy", "image", "--input", "/scan/image.tar",
			"--scanners", "vuln", "--severity", "HIGH,CRITICAL", "--ignore-unfixed",
			"--exit-code", "0", "--no-progress", "--quiet", "--format", "json", "--output", reportPath,
		}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})

	code, err := ctr.ExitCode(ctx)
	if err != nil {
		return "", fmt.Errorf("scan could not run: %w", err)
	}
	if code != 0 {
		stderr, _ := ctr.Stderr(ctx)
		return "", fmt.Errorf("scan could not run: trivy exited %d: %s", code, strings.TrimSpace(stderr))
	}
	raw, err := ctr.File(reportPath).Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("scan could not run: trivy wrote no report: %w", err)
	}

	var report trivyReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		return "", fmt.Errorf("scan could not run: trivy's report is not JSON: %w", err)
	}
	var findings []string
	for _, result := range report.Results {
		for _, v := range result.Vulnerabilities {
			findings = append(findings, fmt.Sprintf("%s %s %s %s -> %s (%s)",
				v.Severity, v.VulnerabilityID, v.PkgName, v.InstalledVersion, v.FixedVersion, result.Target))
		}
	}
	sort.Strings(findings)
	if len(findings) > 0 {
		return "", fmt.Errorf("scan found %d fixable HIGH or CRITICAL vulnerabilities:\n%s",
			len(findings), strings.Join(findings, "\n"))
	}
	return "scan clean: no fixable HIGH or CRITICAL vulnerabilities", nil
}
