// Package buildlane holds the build lane's decisions as pure functions: what a
// push changed, where its image goes, what the registry credential says, how
// a failed step reads, and what hades answered the permit. build.go at the
// module root is left with the chain and nothing to decide.
//
// Ported from foundry-stocks ci/lib/build/build.sh and permit.py, rule for
// rule; each function names the line it replaces.
package buildlane

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The door's three verdicts for a lane.
const (
	Clean       = 0
	Findings    = 1
	CouldNotRun = 2
)

// inert is the set of paths whose change alone builds nothing — records, agent
// and editor furniture, docs — so a README edit does not publish, permit and
// roll the star (build.sh BUILD_INERT).
//
// THE HOOK FILES ARE FURNITURE TOO. stages.just (the file the hooks' `just
// check` / `just gate` import), hooks/pre-commit and hooks/pre-push (what
// core.hooksPath names) and cliff.toml (git-cliff's config) reach no image:
// no Dockerfile COPYs them and no go:embed reads them, fleet-wide. justfile was
// inert and stages.just, split out of it in F15, was not — so a fleet re-lay of
// that one file would have rebuilt and rolled every star it touched. Exact
// paths, not hooks/: foundry-stocks keeps real source under hooks/.
var inert = regexp.MustCompile(`^(star\.toml|\.copier-answers\.yml|\.copier-answers\.speckit\.yml|justfile|stages\.just|hooks/pre-commit|hooks/pre-push|cliff\.toml|\.pre-commit-config\.yaml|ruff\.toml|\.secrets\.baseline|LICENSE|\.gitignore|\.gitattributes|\.editorconfig|\.python-version|\.furnaceignore)$|^(\.furnace|\.claude|\.specify|\.forgejo|\.github|\.agents|\.cerberus|docs|rules)/|\.md$|\.tfvars$|\.melt$`)

// NonInert answers the paths in `git diff --name-only` output that are not
// inert, in the order git listed them.
func NonInert(changed string) []string {
	var out []string
	for _, p := range strings.Split(changed, "\n") {
		if p = strings.TrimSpace(p); p != "" && !inert.MatchString(p) {
			out = append(out, p)
		}
	}
	return out
}

var composeImage = regexp.MustCompile(`(forgejo|registry)\.notusmi\.com/rob/[A-Za-z0-9._/-]+(:[A-Za-z0-9._-]+)?`)

// ComposeFiles are the compose spellings DeclaredImage reads, in order.
var ComposeFiles = []string{"compose.yaml", "compose.yml", "docker-compose.yml"}

// DeclaredImage answers the image a star's compose file names — the first
// fleet image reference in it — or registry.notusmi.com/rob/<star>:latest when
// it names none. The answer always carries a tag.
func DeclaredImage(compose, star string) string {
	img := composeImage.FindString(compose)
	if img == "" {
		img = "registry.notusmi.com/rob/" + star
	}
	if !strings.Contains(path.Base(img), ":") {
		img += ":latest"
	}
	return img
}

// PushRepo answers where a declared image is pushed: the path after the fleet
// host, on the lane's registry, without its tag.
func PushRepo(registry, image string) string {
	p := image
	if _, after, ok := strings.Cut(image, ".notusmi.com/"); ok {
		p = after
	}
	if i := strings.LastIndex(p, ":"); i > strings.LastIndex(p, "/") {
		p = p[:i]
	}
	return registry + "/" + p
}

// GPin is the tag a tip is published under: g and the commit's first twelve.
func GPin(pushRepo, sha string) (string, error) {
	if len(sha) < 12 {
		return "", fmt.Errorf("the commit %q is too short for a g-pin", sha)
	}
	return pushRepo + ":g" + sha[:12], nil
}

// StampTag is the tag Flux's image automation reads (Scheduler Redistribution
// Part II, D7): `{unix-ts}-{sha7}`, the timestamp the commit's COMMITTER time.
// The ImagePolicy orders numerically on the first field, so the field has to
// order the way main does — and the committer time of a door landing is the
// moment it landed. The build's own clock was the other candidate and is wrong:
// the door re-asks a could-not-run tip for hours (couldNotRunCooldown), and a
// rerun of an older landing would carry a newer stamp and roll the star back.
// committed is `git log -1 --format=%ct` verbatim, newline and all.
func StampTag(pushRepo, sha, committed string) (string, error) {
	if len(sha) < 7 {
		return "", fmt.Errorf("the commit %q is too short for a stamp tag", sha)
	}
	ts := strings.TrimSpace(committed)
	if n, err := strconv.ParseInt(ts, 10, 64); err != nil || n <= 0 {
		return "", fmt.Errorf("the commit time %q is not a unix timestamp", ts)
	}
	return pushRepo + ":" + ts + "-" + sha[:7], nil
}

// BuildArgs reads .forgejo/build-args.env — KEY=VALUE per line, blank lines
// and # comments skipped — into entries, verbatim.
func BuildArgs(contents string) []string {
	var out []string
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// RegistryLogin reads a docker config JSON's username and password for host,
// from username/password or from the base64 auth pair. The error never
// carries the credential.
func RegistryLogin(configJSON, host string) (user, password string, err error) {
	var cfg struct {
		Auths map[string]struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Auth     string `json:"auth"`
		} `json:"auths"`
	}
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return "", "", fmt.Errorf("the registry credential is not a docker config JSON")
	}
	a, ok := cfg.Auths[host]
	if !ok {
		return "", "", fmt.Errorf("the registry credential names no entry for %s", host)
	}
	u, p := a.Username, a.Password
	if u == "" && a.Auth != "" {
		dec, err := base64.StdEncoding.DecodeString(a.Auth)
		if err != nil {
			return "", "", fmt.Errorf("the registry credential's auth for %s is not base64", host)
		}
		u, p, _ = strings.Cut(string(dec), ":")
	}
	if u == "" || p == "" {
		return "", "", fmt.Errorf("the registry credential names no username and password for %s", host)
	}
	return u, p, nil
}

var builderStage = regexp.MustCompile(`(?im)^FROM .* AS builder\s*$`)

// HasBuilderStage answers whether a Dockerfile names a `builder` stage, whose
// dependencies the SBOM then covers too.
func HasBuilderStage(dockerfile string) bool { return builderStage.MatchString(dockerfile) }

var digest = regexp.MustCompile(`sha256:[0-9a-f]{64}`)

// DigestOf answers the first sha256 digest in s, or "".
func DigestOf(s string) string { return digest.FindString(s) }

// MergeSBOM folds the builder stage's CycloneDX components into the image's:
// one per purl, or per name@version where a component has no purl, the image's
// copy kept, sorted by that key (build.sh's jq group_by). It answers the merged
// document and the three component counts.
func MergeSBOM(image, builder []byte) (merged []byte, imageN, builderN, mergedN int, err error) {
	var img map[string]any
	if err := json.Unmarshal(image, &img); err != nil {
		return nil, 0, 0, 0, fmt.Errorf("the image SBOM is not JSON: %w", err)
	}
	var bld map[string]any
	if err := json.Unmarshal(builder, &bld); err != nil {
		return nil, 0, 0, 0, fmt.Errorf("the builder SBOM is not JSON: %w", err)
	}
	ic, bc := components(img), components(bld)
	type keyed struct {
		key string
		c   any
	}
	seen := map[string]bool{}
	var all []keyed
	for _, c := range append(append([]any{}, ic...), bc...) {
		k := componentKey(c)
		if seen[k] {
			continue
		}
		seen[k] = true
		all = append(all, keyed{k, c})
	}
	slices.SortStableFunc(all, func(a, b keyed) int { return strings.Compare(a.key, b.key) })
	out := make([]any, len(all))
	for i, k := range all {
		out[i] = k.c
	}
	img["components"] = out
	merged, err = json.Marshal(img)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	return merged, len(ic), len(bc), len(out), nil
}

func components(doc map[string]any) []any {
	c, _ := doc["components"].([]any)
	return c
}

func componentKey(c any) string {
	m, _ := c.(map[string]any)
	if p, ok := m["purl"].(string); ok {
		return p
	}
	name, ok := m["name"].(string)
	if !ok {
		name = "?"
	}
	version, ok := m["version"].(string)
	if !ok {
		version = "?"
	}
	return name + "@" + version
}

// fault is a failure the lane did not get to look past — the network or the
// registry — so running again can change the answer (build.sh fault_re). The
// timeout and name-resolution phrasings are uv's (publish.sh FAULT_RE): the
// publish lane settles its build and its uploads through this too.
//
// `response status code 5\d\d` IS ORAS'S PHRASING AND IT IS LOAD-BEARING.
// The literals beside it — "502 Bad Gateway" and friends — are the SPACED
// form, and oras writes a COLON: "response status code 502: Bad Gateway".
// One character, and the whole line missed. MEASURED 2026-09-16 on anvil
// (cast-anvil-93f3819-b9kw5): zot was restarting and rebuilding its 221-repo
// metadata DB, port 5000 was not listening yet, `oras push` to staging got a
// 502, and Failed filed a transient registry outage as FINDINGS — telling the
// operator "running again changes nothing" about the one failure where
// running again is the entire fix. Every lane that pushes to a registry
// (build, cast, publish, bundle) reads this, so a routine zot restart could
// red any landing in the fleet as a fault of the tree.
//
// THE REGISTRY'S OWN 500 IS THE REGISTRY'S, AND SO IS THE ENGINE'S FAILURE
// TO LOAD WHAT IT PULLED. MEASURED 2026-09-18 10:49Z, seaweedfs (zot's S3
// backend) down for ~2 minutes after dev01 rebooted: glaucus's tip build
// settled FINDINGS on `unexpected status from POST request to
// https://registry…/blobs/uploads/…: 500 Internal Server Error` (zot: "dial
// tcp …:8333: connection refused" to seaweedfs), and harmonia's pull build
// settled FINDINGS on `failed to load container from converted ID: … failed
// to content hash dockerfile copy: exit code: 1` — the engine could not read
// the base layers it had pulled through the same registry. Both said
// "running again changes nothing" about the one failure where running again
// is the entire fix, and the door never re-asked. The phrases below name
// them — phrases, not bare status numbers: `500 Internal Server Error` is
// the spaced form buildkit's pusher writes, and `failed to load container
// from converted ID` is the engine's own wording and never a verdict on a
// tree.
//
// THREE ALTERNATIVES LEFT ON 2026-09-26, BECAUSE A 404 IS NOT A NETWORK
// FAULT. `unexpected status from [A-Z]+ request to` matched buildkit's prefix
// WHATEVER THE CODE, and the comment above said so as if it were the point.
// It is not: a 4xx from a registry is the registry answering correctly about
// a thing that is not there, and calling that transient spends the whole
// re-ask ladder on a condition no re-ask can change. MEASURED on
// foundry/base-images 92eb0db0 — five build settles, BYTE-IDENTICAL output
// each time, four bases rebuilt per settle, 5 of the ladder's 9 rungs gone,
// and only the landing stopped it (ourea#9793's cap is a window, not a wall).
//
// AND THE PREFIX EARNED NOTHING IT DID NOT ALREADY HAVE. Every 5xx phrasing
// is named separately and appears in the SAME string buildkit writes, so the
// prefix is redundant for the case it was added for and load-bearing only for
// the case it gets wrong. Measured both ways over a 17-case corpus: 10 of 10
// transient strings still match without it — including all three incidents
// this comment cites — and 6 of 7 permanent ones stop matching.
//
// `failed to resolve source metadata` went for the same reason and it is the
// clearer instance: it is a WRAPPER. buildkit writes `failed to resolve
// source metadata for <ref>: <cause>`, so a transient cause is already in the
// string and matches on its own merits (`…: dial tcp: connection refused`
// still reads as a fault), while `…: not found` no longer reads as one.
//
// A THIRD REMOVAL WAS ATTEMPTED AND WITHDRAWN, which is the more useful
// record. `unexpected media type … : not found` looks like the same defect —
// it names a digest, and a digest that is absent stays absent. The test below
// refused it, and the test is right: its case is a blob zot's GC REAPED, and
// the build that produced that blob re-pushes it on the next run. Transient.
// The string cannot tell a reaped blob from one that never existed, so it
// stays a fault, and the ambiguity is the finding rather than the phrasing.
//
// WHICH IS ALSO WHERE `failed to load container from converted ID` sits, and
// why it is untouched: measured transient on 2026-09-18 (seaweedfs down two
// minutes) and permanent on base-images 92eb0db0. Nothing in either text
// separates them, and that coordinate's Job record has since been reaped so
// the distinguishing lines are gone.
//
// SO TWO OF THESE PHRASES ARE GENUINELY BOTH THINGS, and no regexp fixes
// that. The discriminator the system already holds is not a phrase: the five
// base-images settles were BYTE-IDENTICAL, which a ladder comparing
// consecutive could-not-run outputs can see and a classifier reading one
// output never can. That belongs in ourea's ladder, not here.
var fault = regexp.MustCompile(`(?i)connection refused|connection reset|connection timed out|operation timed out|i/o timeout|no such host|temporary failure in name resolution|dns error|server misbehaving|TLS handshake timeout|response status code 5\d\d|500 Internal Server Error|502 Bad Gateway|503 Service Unavailable|504 Gateway|failed to load container from converted ID|unexpected EOF|too many requests|429 Too Many Requests|toomanyrequests|context deadline exceeded|failed to do request|unexpected media type [^[:space:]]+ for sha256:[0-9a-f]{64}: not found`)

// contended is a SHARED TOOLCHAIN CACHE being written by two lanes at once.
// It is neither a network fault nor a tool refusing its arguments, so it gets
// its own pattern rather than being folded into fault — "failed on a network
// fault" would be a false sentence about a healthy network, and a verdict that
// lies about the cause is the thing this file exists to stop.
//
// MEASURED 2026-09-16 on tongs (cast-tongs-560a9f9-wrkxj), while gavel's cast
// was resolving crates in the same seconds:
//
//	error: failed to unpack package `sha2 v0.10.9`
//	Caused by: failed to open `/usr/local/cargo/registry/src/
//	           index.crates.io-1949cf8c6b5b557f/sha2-0.10.9/.cargo-ok`
//	Caused by: File exists (os error 17)
//
// WHY IT HAPPENS AT ALL: checks.CachesFor(ImageRust) mounts
// /usr/local/cargo/registry as a shared Dagger cache volume, but cargo's
// cross-process lock is $CARGO_HOME/.package-cache — /usr/local/cargo, which
// is NOT mounted. The directory is shared and the lock guarding it is not, so
// two cargos each hold a lock the other cannot see and both unpack the same
// crate. The cache fix is foundry-tools#9663; this is the classification half,
// and it is worth having on its own because ANY contended cache outlives any
// one fix to the mount.
//
// The next run reads a cache that is already correct, so this is retryable in
// the strongest sense: the thing that failed has since completed.
var contended = regexp.MustCompile(`(?i)failed to unpack package|failed to open [^\n]*\.cargo-ok`)

// Failed answers the verdict of a step that failed with output: could-not-run
// on a network fault or a contended toolchain cache, findings on anything else.
func Failed(step, output string) (int, string) {
	if hit := fault.FindString(output); hit != "" {
		return CouldNotRun, fmt.Sprintf("could not run: %s failed on a network fault (%s) — the lane did not get to look; run it again", step, hit)
	}
	if hit := contended.FindString(output); hit != "" {
		return CouldNotRun, fmt.Sprintf("could not run: %s lost a race for a shared toolchain cache (%s) — another lane was writing it; the next run reads it warm, so run it again", step, strings.TrimSpace(hit))
	}
	return Findings, fmt.Sprintf("findings in %s — read its log above; running again changes nothing", step)
}

// refused is a CLI rejecting the arguments it was called with, in cobra's
// wording, which cosign and syft share. The tool exits before it looks at
// anything, so the fault is in the lane's call, not the image. The pattern is
// anchored at the start of a line, allowing cosign's two prefixes, so that
// BuildKit's "dockerfile parse error on line 2: unknown flag: --x", a finding
// in the tree, never matches it.
var refused = regexp.MustCompile(`(?m)^(?:Error: |error during command execution: )?(?:unknown (?:shorthand )?flag: |unknown command "|flag needs an argument: |(?:requires|accepts) .*arg\(s\)|required flag\(s\) ).*$`)

// ToolFailed gives the verdict for a tool exec that failed with output. It is
// could-not-run when the tool refused the lane's own arguments, and otherwise
// whatever Failed says. Measured 2026-09-14 on athena a446514: cosign v2.5.3
// answered the lane's sign with "Error: unknown flag: --use-signing-config",
// and Failed filed that as findings in sign, a verdict about an image no tool
// had looked at.
func ToolFailed(step, output string) (int, string) {
	if hit := refused.FindString(output); hit != "" {
		return CouldNotRun, fmt.Sprintf("could not run: %s refused the lane's own arguments (%s) — the tool never looked; the lane's call is wrong, not the image", step, strings.TrimSpace(hit))
	}
	return Failed(step, output)
}

// ParseCall reads hadescall's output: "HTTP <status>" and then the body.
func ParseCall(out string) (status int, body string, err error) {
	head, body, _ := strings.Cut(out, "\n")
	code, ok := strings.CutPrefix(strings.TrimSpace(head), "HTTP ")
	if !ok {
		return 0, "", fmt.Errorf("hadescall answered no status line: %.200q", out)
	}
	status, err = strconv.Atoi(code)
	if err != nil {
		return 0, "", fmt.Errorf("hadescall answered a status that is not a number: %.200q", head)
	}
	return status, body, nil
}

// noArtifactAt matches mold's refusal when nothing is stamped for the head it
// looked at. The text is hephaestus internal/mold/mold.go: "mold <name>: no CI
// artifact at the tip <sha> or the <n> commit(s) behind it". The sha is RAW —
// mold resolves tags by a g-pin but reports the commit — so the matcher this
// replaced (`at g([0-9a-f]{12})`) could never fire, and every superseded
// permit settled Findings, blaming the build for a race it did not lose.
var noArtifactAt = regexp.MustCompile(`no CI artifact at the tip ([0-9a-f]{7,40})`)

// sameCommit answers whether two shas name the same commit: one is a prefix of
// the other, so a full sha and an abbreviation of it still match. Both sides
// are full shas today — the door's --sha and mold's tip — but a bare equality
// would call a commit superseded by itself the day either side shortens.
func sameCommit(a, b string) bool {
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// Permit folds hades's answer to forge_mold into a verdict and a reason
// (permit.py classify). built is the commit this build published: when mold's
// refusal names a different head, the tip moved past this build and the
// permit is superseded — could-not-run, not a finding about this build.
func Permit(status int, raw, built string) (int, string) {
	switch {
	case status == 403 && strings.Contains(raw, "unidentifiable caller"):
		return CouldNotRun, "hades did not DERIVE a principal from the peer SVID, so the PDP was never consulted — check the deployed hades revision, not the policy"
	case status == 403:
		return Findings, "hades identified the caller and the POLICY refused it: the lane SVID is not granted forge_mold — check policy/authz_grants in foundry-dies"
	case status == 401:
		return CouldNotRun, "hades rejected the caller at the door (401): the client certificate was not presented or did not verify against the SPIRE bundle"
	case status != 200:
		return CouldNotRun, fmt.Sprintf("hades returned HTTP %d — failing closed", status)
	}
	var answer struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(raw), &answer); err != nil {
		return CouldNotRun, "hades returned a 200 with a body that is not a tool answer — failing closed"
	}
	text := ""
	if len(answer.Content) > 0 {
		text = answer.Content[0].Text
	}
	if answer.IsError {
		if m := noArtifactAt.FindStringSubmatch(text); m != nil && !sameCommit(built, m[1]) {
			return CouldNotRun, fmt.Sprintf("SUPERSEDED — the tip moved to %.12s before this build's permit ran; mold stamps the current head only, so nothing was stamped for %.12s and the newer tip's own build permits it", m[1], built)
		}
		return Findings, strings.TrimSpace("PERMIT REFUSED by hephaestus — the CI build did not verify; the image is NOT stamped and must not be promoted. " + truncate(text, 300))
	}
	var body struct {
		NoOp      bool   `json:"no_op"`
		Digest    string `json:"digest"`
		PushedRef string `json:"pushed_ref"`
	}
	_ = json.Unmarshal([]byte(text), &body)
	if body.NoOp {
		return Clean, "WARNING permit was a no-op: mold found the record's content-etag tag already resolved and stamped nothing"
	}
	return Clean, fmt.Sprintf("verified and stamped (digest=%s, ref=%s)", or(body.Digest, "?"), or(body.PushedRef, "?"))
}

func truncate(s string, n int) string { return s[:min(len(s), n)] }

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
