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
	"regexp"
	"sort"
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
var inert = regexp.MustCompile(`^(star\.toml|\.copier-answers\.yml|\.copier-answers\.speckit\.yml|justfile|\.pre-commit-config\.yaml|ruff\.toml|\.secrets\.baseline|LICENSE|\.gitignore|\.gitattributes|\.editorconfig|\.python-version|\.furnaceignore)$|^(\.furnace|\.claude|\.specify|\.forgejo|\.github|\.agents|\.cerberus|docs|rules)/|\.md$|\.tfvars$|\.melt$`)

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
	if !strings.Contains(img[strings.LastIndex(img, "/")+1:], ":") {
		img += ":latest"
	}
	return img
}

// PushRepo answers where a declared image is pushed: the path after the fleet
// host, on the lane's registry, without its tag.
func PushRepo(registry, image string) string {
	path := image
	if _, after, ok := strings.Cut(image, ".notusmi.com/"); ok {
		path = after
	}
	if i := strings.LastIndex(path, ":"); i > strings.LastIndex(path, "/") {
		path = path[:i]
	}
	return registry + "/" + path
}

// GPin is the tag a tip is published under: g and the commit's first twelve.
func GPin(pushRepo, sha string) (string, error) {
	if len(sha) < 12 {
		return "", fmt.Errorf("the commit %q is too short for a g-pin", sha)
	}
	return pushRepo + ":g" + sha[:12], nil
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
	sort.SliceStable(all, func(i, j int) bool { return all[i].key < all[j].key })
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
// registry — so running again can change the answer (build.sh fault_re).
var fault = regexp.MustCompile(`(?i)connection refused|connection reset|i/o timeout|no such host|server misbehaving|TLS handshake timeout|502 Bad Gateway|503 Service Unavailable|504 Gateway|unexpected EOF|too many requests|429 Too Many Requests|toomanyrequests|context deadline exceeded|failed to do request|failed to resolve source metadata|unexpected media type [^[:space:]]+ for sha256:[0-9a-f]{64}: not found`)

// Failed answers the verdict of a step that failed with output: could-not-run
// on a network fault, findings on anything else.
func Failed(step, output string) (int, string) {
	if hit := fault.FindString(output); hit != "" {
		return CouldNotRun, fmt.Sprintf("could not run: %s failed on a network fault (%s) — the lane did not get to look; run it again", step, hit)
	}
	return Findings, fmt.Sprintf("findings in %s — read its log above; running again changes nothing", step)
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

var noArtifactAt = regexp.MustCompile(`no CI artifact at g([0-9a-f]{12})`)

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
		if m := noArtifactAt.FindStringSubmatch(text); m != nil && built != "" && !strings.HasPrefix(built, m[1]) {
			return CouldNotRun, fmt.Sprintf("SUPERSEDED — the tip moved to %s before this build's permit ran; mold stamps the current head only, so nothing was stamped for %.12s and the newer tip's own build permits it", m[1], built)
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
