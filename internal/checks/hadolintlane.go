package checks

import (
	"path"
	"strings"
)

// The hadolint atom's JUDGEMENTS, as pure functions over strings — which
// files are Dockerfiles, what the fleet's ruleset is, and whether the binary
// on disk is the pinned one. The atom itself (atoms_fleet.go) only fetches,
// mounts and points.

// HadolintVersion / HadolintMirror / HadolintURL fetch the Dockerfile linter
// the fleet:hadolint atom runs. Same Nexus-then-upstream shape as opa and
// compose, and the same refusal: a Dockerfile that was never linted is not a
// Dockerfile that passed.
//
// THE MIRROR IS A LITERAL, NOT A JOIN, for the reason OpengrepMirror is: a
// `+` in a const block is a declaration no coverage profile can mark, and the
// mutation lane reads it as NOT COVERED forever (foundry-tools#35, twice).
// TestHadolintPinsAgree holds the version and both URLs in step instead.
//
// THE ASSET NAME IS LOWERCASE. v2.15.1 publishes `hadolint-linux-x86_64`
// (measured against the release's own asset list and checksums.sha256,
// 2026-09-13: sha256 c7187db9…, 55 661 096 bytes); GitHub also answers the
// older `hadolint-Linux-x86_64` spelling by redirect, and a URL that works by
// accident is a URL that stops working on somebody else's schedule. The
// mirror serves the same bytes — measured, same digest.
//
// THE PIN IS THE ONE THE FLEET'S HOOKS RUN. Seven repositories laid the
// hadolint-docker pre-commit hook at ghcr.io/hadolint/hadolint:v2.15.1 on
// 2026-09-12/13, so the lint that runs on a laptop and the lint that gates a
// pull are the same program at the same version — a finding here is a
// finding there.
const (
	HadolintVersion = "2.15.1"
	HadolintMirror  = "https://nexus.notusmi.com/repository/github-raw/hadolint/hadolint/releases/download/v2.15.1/hadolint-linux-x86_64"
	HadolintURL     = "https://github.com/hadolint/hadolint/releases/download/v2.15.1/hadolint-linux-x86_64"
)

// HadolintConfigPath is where the atom writes the fleet's ruleset inside the
// lane container, and the `--config` it hands hadolint. OUTSIDE /src,
// deliberately: hadolint reads `.hadolint.yaml` from the working directory
// when no --config is given, and an explicit --config REPLACES that lookup
// rather than merging with it (measured 2026-09-13: a cwd config ignoring
// DL3008 silenced the rule; the same tree under `--config <other file>` fired
// it). That replacement is the whole mechanism by which the ruleset below is
// the fleet's and not the repository's.
const HadolintConfigPath = "/etc/hadolint/fleet.yaml"

// HadolintConfig is THE FLEET'S RULESET, and no atom reads the repository's.
// Rob, 2026-09-11: a repo has no say in anything that runs; the fleet decides
// the atoms AND their rulesets. Every line below is hadolint's own default
// except three decisions, each measured against every Dockerfile the fleet
// tracks (47 files: 39 image stars' root Dockerfiles, the six bases, infra's
// tf-runner, fleet-crew's exec image — 2026-09-13):
//
//   - failure-threshold: warning. An error or a warning is a finding; info
//     and style are reported and do not fail. hadolint's own default is info,
//     which would make DL3066 ("non-numeric user-id", 12 stars — the template
//     pours `USER app`) and DL3059 ("consecutive RUNs") gate reds over
//     advice. They stay visible in the tool's output; they do not block.
//
//   - DL3008 ignored — `apt-get install foo` without `=version`. The fleet
//     does not pin distro packages by version: it pins the base image by
//     digest and the OUTPUT by digest, and a Debian point release moves the
//     only version the mirror serves, so a hand-pinned `foo=1.2.3-1` is a
//     build that breaks on the next sync for no reproducibility the digest
//     pin does not already give. A Renovate-managed pin is worse, not better:
//     Renovate's deb datasource reads Packages.gz only, and Debian ships
//     bookworm-security and bookworm-updates as .xz only (measured
//     2026-09-13, at the mirror and at deb.debian.org). A managed pin could
//     never see the security pocket — ca-certificates would sit on main's
//     20230311 bundle while security carries 20250419. The unlock is
//     upstream (xz in the deb datasource), not here.
//
//     DL3018 (`apk add foo` without `=version`) was ignored beside it until
//     its only population, infra's host-support/tf-runner, retired
//     (infra#490, 2026-09-13). It is hadolint's default again: an Alpine
//     image that enters the fleet meets the rule.
//
//   - trustedRegistries: the fleet's two hosts. images.go states the
//     invariant — "the host is the fleet's docker mirror (docker.notusmi.com),
//     never docker.io directly" — and DL3026 is hadolint saying it about a
//     FROM line. Measured: five real violations today (three stars still
//     FROM the sunset forgejo.notusmi.com host; two direct docker.io pulls)
//     and ONE false positive, `FROM ${PYTHON_BASE}` in python-base-image,
//     which hadolint cannot resolve through the ARG even when its default is
//     a trusted host — that line carries a reasoned pragma.
//
// RETURNED TO THE DEFAULT. DL3064 ("potentially sensitive data in ARG or
// ENV") was demoted to info while its only population was Go module
// coordinates: the heuristic is a variable-NAME substring match, and
// `GOPRIVATE` contains PRIVATE (26 of 26 Go stars measured). The sweep of
// 2026-09-13 spelled every one as GONOPROXY + GONOSUMDB with the same hosts
// (`go env` identical before and after, star by star; in hephaestus and
// tartarus it also closed a latent trap, since a GONOSUMDB set beside
// GOPRIVATE replaces the default GOPRIVATE would give it). No Dockerfile on
// main trips the rule now (75 measured), so a name that reads as a secret is
// a warning again; fleet:detect-secrets still reads every tracked file for
// the values themselves.
//
// WHAT IS DELIBERATELY NOT HERE. DL3025 (JSON notation for CMD, ENTRYPOINT
// and HEALTHCHECK) stays a warning: eleven stars carry a template-poured
// HEALTHCHECK in shell form with a redundant `|| exit 1`, and every one is a
// one-line fix that makes the Dockerfile strictly better — the same rule on
// CMD/ENTRYPOINT is what keeps SIGTERM reaching PID 1 rather than a wrapping
// `sh`. Inline `# hadolint ignore=` pragmas are HONOURED (no
// --disable-ignore-pragma): a suppression with a reason beside it is the
// fleet's sanctioned shape for a line the rule gets wrong, exactly as `noqa`
// is under stop-justifications.
//
// The repositories' own `.hadolint.yaml` mirrors serve the LOCAL hook and
// nothing else: the gate never reads them.
const HadolintConfig = `# The fleet's hadolint ruleset. Written by foundry-tools into the lane
# container; the repository's own .hadolint.yaml is not read. The reasons
# for every line are in internal/checks/hadolintlane.go.
failure-threshold: warning
ignored:
  - DL3008
trustedRegistries:
  - docker.notusmi.com
  - registry.notusmi.com
`

// HadolintTrustedRegistries is the FROM allowlist the config above carries,
// as data, so a test can hold the YAML and the list in step.
var HadolintTrustedRegistries = []string{"docker.notusmi.com", "registry.notusmi.com"}

// DockerfilePopulation answers the Dockerfiles among the gate population —
// the files hadolint is asked to read.
//
// THE PREDICATE IS THE ONE DEFINITION. It is applied in Go over the whole
// committable tree rather than expressed a second time as a set of globs
// handed to the engine, so there is one spelling of "what is a Dockerfile"
// and a table test reads it.
//
// WHAT COUNTS: a basename of `Dockerfile` or `Containerfile`; either with a
// dotted suffix (`Dockerfile.arm64`, `Containerfile.dev`); or a dotted
// prefix on either, in any case (`build.Dockerfile`, `serving.dockerfile`).
// These are the spellings Docker, Podman and every editor's file association
// recognise. Hyphenated names (`Dockerfile-old`) are NOT Dockerfiles here:
// nothing documents that spelling, and the two the fleet tracks sit in a
// directory called trash.
//
// The input is the gate population, so `vendor/` and `node_modules/` are
// already gone (GateExclude) — which matters, because four Go stars vendor
// go.opentelemetry.io's dependencies.Dockerfile and one vendors sha1cd's
// Dockerfile.arm64, and none of those is a Dockerfile this repository ships.
func DockerfilePopulation(files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if IsDockerfile(f) {
			out = append(out, f)
		}
	}
	return out
}

// IsDockerfile reports whether one path is a Dockerfile by name. path.Base
// already reads `./Dockerfile` as `Dockerfile`; a bare `.dockerfile` (a hidden
// file with no stem in front of the dot) is refused by the length clause.
func IsDockerfile(p string) bool {
	base := path.Base(p)
	for _, stem := range []string{"Dockerfile", "Containerfile"} {
		if base == stem || strings.HasPrefix(base, stem+".") {
			return true
		}
		lower := strings.ToLower(base)
		if strings.HasSuffix(lower, "."+strings.ToLower(stem)) && len(lower) > len(stem)+1 {
			return true
		}
	}
	return false
}

// HadolintVersionOK reads `hadolint --version` and answers whether the binary
// IS the pin.
//
// THE PIN IS PART OF THE QUESTION, as with opa: a rule set is a property of
// the binary, and hadolint adds, renames and re-grades rules between minors
// (DL3064 does not exist before 2.12). "This tree passes hadolint" is a
// statement about 2.15.1's rules, and the probe proves the binary on PATH is
// that one. The output is one line, `Haskell Dockerfile Linter 2.15.1`, and
// the match is on the whole trimmed line so 2.15.1-rc1 is not 2.15.1.
func HadolintVersionOK(versionOutput, want string) bool {
	for _, line := range strings.Split(versionOutput, "\n") {
		if strings.TrimSpace(line) == "Haskell Dockerfile Linter "+want {
			return true
		}
	}
	return false
}
