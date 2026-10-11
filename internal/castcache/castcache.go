// Package castcache names the cast's Rust target archive in SeaweedFS: the
// bucket, the role, the object key and the credential files. It imports the
// standard library only, so the Dagger module can use the names without
// compiling the S3 client (internal/casttarget, which does the work).
//
// WHY AN ARCHIVE. The cast lane builds into an engine-local cache volume
// (checks.ReleaseCacheFor). It is warm only while the engine pod that holds it
// lives and has not pruned it: MEASURED 2026-10-11, a cerberus cast compiled
// 234 crates with 0 Fresh (~580 CPU-s) and bellows ~480 CPU-s on every
// production cast. The archive is one object per repo, toolchain and target
// triple, overwritten by each successful cast, so storage is bounded and the
// object outlives every engine.
package castcache

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

const (
	// Bucket is the cast's bucket in SeaweedFS; the CastCache role is scoped to it.
	Bucket = "cast-cache"
	// RoleARN is the SeaweedFS STS role the cast's SPIFFE id may assume (flux
	// objects/seaweedfs-s3.yaml pins oidc:sub to ca-cast).
	RoleARN = "arn:aws:iam::role/CastCache"
	// Audience is the JWT-SVID audience SeaweedFS' spire provider requires.
	Audience = "seaweedfs"
	// DefaultEndpoint is SeaweedFS' S3 and STS port, in cluster.
	DefaultEndpoint = "http://seaweedfs.objects.svc.cluster.local:8333"
	// Region is signed into every request; SeaweedFS does not check it.
	Region = "us-east-1"

	// SecretDir holds the three dagger secrets in the archive container, one
	// file each: a secret is a mounted file, never an env var or an argument.
	SecretDir     = "/run/castcache"
	AccessKeyFile = SecretDir + "/access-key-id"
	SecretKeyFile = SecretDir + "/secret-access-key"
	TokenFile     = SecretDir + "/session-token"

	// CredsOut is where the credential fetch leaves its answer, for the module
	// to read as a file and hand on as secrets. Never stdout: dagger shows an
	// exec's output in its progress log.
	CredsOut = "/tmp/castcache-creds.json"

	// MinFiles is the smallest target worth shipping. A save that found fewer
	// ran against a volume the build never wrote (an engine-cached build exec
	// does not touch it) and must not overwrite a good archive with nothing.
	MinFiles = 20
)

// Triple is the Rust target triple of the platform the helper runs on, which
// is the platform the lane's cargo builds for.
func Triple(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "x86_64-unknown-linux-gnu", nil
	case "arm64":
		return "aarch64-unknown-linux-gnu", nil
	}
	return "", fmt.Errorf("no Rust target triple is known for GOARCH=%s", goarch)
}

// ToolchainKey names a toolchain image for the archive key: its repository's
// last segment, tag and the first twelve hex of its digest. A new toolchain is
// a new key, so an archive never carries one compiler's artifacts to another.
//
//	docker.io/library/rust:1.97.0-bookworm@sha256:8fa55b2f3ddf… -> rust-1.97.0-bookworm-8fa55b2f3ddf
func ToolchainKey(image string) string {
	ref, digest, _ := strings.Cut(image, "@")
	ref = ref[strings.LastIndex(ref, "/")+1:]
	ref = strings.ReplaceAll(ref, ":", "-")
	if hex, ok := strings.CutPrefix(digest, "sha256:"); ok && len(hex) >= 12 {
		ref += "-" + hex[:12]
	}
	return segment(ref)
}

// Key is the object key of one archive: <star>/<toolchain>/<triple>.tar.gz.
// The star is the repo URL's last path element, as the cast lane names it.
func Key(repo, toolchain, triple string) (string, error) {
	star := segment(path.Base(strings.TrimSuffix(strings.TrimSuffix(repo, "/"), ".git")))
	if repo == "" || star == "" || star == "." || star == ".." {
		return "", errors.New("no repository to key the archive by")
	}
	if toolchain == "" || triple == "" {
		return "", errors.New("no toolchain or target triple to key the archive by")
	}
	return star + "/" + segment(toolchain) + "/" + segment(triple) + ".tar.gz", nil
}

// segment keeps a key element to characters that need no escaping anywhere
// between here and the S3 canonical request.
func segment(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '_'
	}, s)
}

// URI is the object's address, for the log.
func URI(key string) string { return "s3://" + Bucket + "/" + key }

// Creds are the temporary credentials STS issued. They are held in memory and
// in mounted secret files, and are never part of an error or a log line.
type Creds struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token"`
}
