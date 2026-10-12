// Package bundlelane holds the bundle lane's decisions as pure functions: which
// die a landing publishes, whether the fleet's two tiers agree, what the built
// artifacts must carry, and what the registry answered. bundle.go at the module
// root is left with the chain.
//
// Ported from foundry-dies ci/bundle.sh, rule for rule; each function names the
// step it replaces.
package bundlelane

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// The two dies and where they are published.
const (
	PolicyDie    = "foundry.notusmi.com/policy/ouranos-bundle"
	FleetDie     = "foundry.notusmi.com/data/fleet-bundle"
	RegistryHost = "foundry.notusmi.com"
	RegistryUser = "rob"
	// BundleMediaType is the artifact type every die is pushed under.
	BundleMediaType = "application/vnd.openpolicyagent.bundle.layer.v1+tar+gzip"
	// LayerMediaType is the one layer's media type.
	LayerMediaType = "application/vnd.oci.image.layer.v1.tar+gzip"
)

var (
	// policyIgnore is build.yml's paths-ignore: schema/ is the slag-schema
	// die's payload on its own channel, and a schema-only change must not
	// re-move :stable on policy/ouranos-bundle.
	policyIgnore = regexp.MustCompile(`(^|/)[^/]*\.md$|^\.forgejo/|^schema/`)
	// fleetMatch is fleet-bundle.yml's paths without its own workflow file:
	// the recipe that replaced that file is this package, and a change here
	// lands in foundry-tools, not in the diff this reads. Republishing the
	// roster on an unrelated policy commit would make its revision say
	// "changed" when nothing did.
	fleetMatch = regexp.MustCompile(`^fleet/|^policy/\.manifest$|^policy/admission/stubs\.rego$`)
)

// Changed reads `git diff --name-only` output into paths, blank lines dropped.
func Changed(out string) []string {
	var paths []string
	for _, p := range strings.Split(out, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// Publishes answers which dies a landing publishes, from the paths it changed
// since the previous tip. No paths means the previous tip could not be read,
// and cannot-tell publishes both (bundle.sh PUBLISH_POLICY / PUBLISH_FLEET).
// THE GATES DO NOT MOVE WITH THIS: both dies are built and gated on every
// landing, and this decides only what is pushed.
func Publishes(changed []string) (policy, fleet bool) {
	if len(changed) == 0 {
		return true, true
	}
	for _, p := range changed {
		if !policyIgnore.MatchString(p) {
			policy = true
		}
		if fleetMatch.MatchString(p) {
			fleet = true
		}
	}
	return policy, fleet
}

// Pin is a die's immutable tag for a commit: g and the commit's first seven.
func Pin(sha string) (string, error) {
	if len(sha) < 7 {
		return "", fmt.Errorf("the commit %q is too short for a pin", sha)
	}
	return "g" + sha[:7], nil
}

// FleetPin is the fleet die's immutable tag: Pin's g<dies7> and then
// -f<flux7>, the foundry/flux commit its facts were read from. The die is a
// function of BOTH commits (data.fleet.flux comes from flux, the roster from
// foundry-dies), so a flux landing at an unchanged foundry-dies commit is a
// new pin, and a pin that stands is a die that is current. Readers: nyx
// reads :signed and the image.revision, never the pin's shape; the registry
// keeps the pin as an opaque tag (-signed appended for the twin).
func FleetPin(sha, fluxSHA string) (string, error) {
	pin, err := Pin(sha)
	if err != nil {
		return "", err
	}
	if len(fluxSHA) < 7 {
		return "", fmt.Errorf("the flux commit %q is too short for a pin", fluxSHA)
	}
	return pin + "-f" + fluxSHA[:7], nil
}

// FleetRevision is the fleet die's bundle revision (opa build --revision and
// the image.revision annotation, which nyx's die-lag compares): the dies
// commit and the flux commit, so a flux-only republish reads as a new
// revision instead of the same one.
func FleetRevision(sha, fluxSHA string) string {
	if len(fluxSHA) < 7 {
		return sha
	}
	return sha + "-f" + fluxSHA[:7]
}

// Orphans answers the stars with a slag shard (fleet/stars/<star>/slag.json)
// that fleet/data.json's map does not carry, sorted, and the map's size. A
// shard with no map entry is a star the fleet demonstrably has and the roster
// is missing, and every seam pointing at it would be denied for the wrong
// reason (bundle.sh fleet_tier_gate).
func Orphans(shards []string, dataJSON string) (orphans []string, mapped int, err error) {
	var d struct {
		Map map[string]json.RawMessage `json:"map"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &d); err != nil {
		return nil, 0, fmt.Errorf("fleet/data.json is not JSON: %w", err)
	}
	for _, s := range shards {
		star := path.Base(path.Dir(s))
		if _, ok := d.Map[star]; !ok {
			orphans = append(orphans, star)
		}
	}
	sort.Strings(orphans)
	return orphans, len(d.Map), nil
}

// Signed answers whether a `tar tzf` listing carries .signatures.json at the
// bundle root — the signature OPA verifies. Its absence is a build failure,
// not something a consumer discovers by being served an empty policy.
func Signed(listing string) bool {
	for _, line := range strings.Split(listing, "\n") {
		if l := strings.TrimSpace(line); l == ".signatures.json" || l == "/.signatures.json" {
			return true
		}
	}
	return false
}

// FleetManifest is the manifest the staged roster carries. The bundle root
// must CONTAIN fleet/, not BE it: a stage rooted at the fleet dir publishes
// data.map instead of data.fleet.map, which composes cleanly and denies
// nothing.
const FleetManifest = "{\n \"roots\": [\"fleet\"],\n \"rego_version\": 1\n}\n"

// RootsAreFleet answers whether a bundle's .manifest declares exactly
// ["fleet"]. A signed build that dropped or rewrote the hand-written manifest
// would publish roots overlapping the policy bundle's, which OPA refuses to
// activate, silently losing whichever bundle lost the race.
func RootsAreFleet(manifest string) bool {
	var m struct {
		Roots []string `json:"roots"`
	}
	if err := json.Unmarshal([]byte(manifest), &m); err != nil {
		return false
	}
	return len(m.Roots) == 1 && m.Roots[0] == "fleet"
}

// Roster grades the built roster's data.fleet AS OPA EVALUATES IT (opa eval
// -b <die> data, which is {"fleet": …}): map, topics and stars all non-empty,
// and no star row carrying a charter. Evaluated, not read out of the tarball's
// data.json, because once fleet/derive.rego ships data.fleet.stars is a rule
// and the tarball's data.json has no stars at all. An empty data.fleet makes
// admission's seam and topic guards evaluate to nothing, and a charter in the
// bundle means slag.json — the full declaration — leaked into the PDP
// (bundle.sh fleet_roster_gate). problem is "" when the roster is sound.
func Roster(dataJSON string) (mapped, stars, topics int, problem string, err error) {
	var d struct {
		Fleet struct {
			Map    json.RawMessage `json:"map"`
			Topics json.RawMessage `json:"topics"`
			Stars  json.RawMessage `json:"stars"`
			// Repos is the catalog rule (F15): one row per key of
			// declared ∪ lean. Absent when the rule is gone or renamed.
			Repos json.RawMessage `json:"repos"`
			// Declared and Lean are the documents fleet/derive.rego derives
			// stars from: one star per key of their union.
			Declared map[string]json.RawMessage `json:"declared"`
			Lean     map[string]json.RawMessage `json:"lean"`
			// Flux is what makes a repo a star (F16): data.fleet.stars holds
			// the repos with a flux entry.
			Flux map[string]json.RawMessage `json:"flux"`
		} `json:"fleet"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &d); err != nil {
		return 0, 0, 0, "", fmt.Errorf("the built roster's data.json is not JSON: %w", err)
	}
	mapped, topics = size(d.Fleet.Map), size(d.Fleet.Topics)
	rows := members(d.Fleet.Stars)
	stars = len(rows)
	if mapped == 0 || topics == 0 || stars == 0 {
		return mapped, stars, topics, "the built bundle has an empty data.fleet — admission's seam and topic guards would evaluate to nothing, so cross-star checks stop denying instead of failing", nil
	}
	// A declared entry without a kind makes its derived row undefined, and the
	// star drops out of data.fleet.stars with nothing failing. When the roster
	// carries the documents the rule derives from, every repo (key of their
	// union) that flux backs must be a star, and nothing else may be (F16:
	// stars are the deployed repos, not the catalog).
	repoKeys := map[string]struct{}{}
	for k := range d.Fleet.Declared {
		repoKeys[k] = struct{}{}
	}
	for k := range d.Fleet.Lean {
		repoKeys[k] = struct{}{}
	}
	want := map[string]struct{}{}
	for k := range repoKeys {
		// A roster that carries no flux document predates the injection: its
		// stars are the whole union, as before F16.
		if _, deployed := d.Fleet.Flux[k]; deployed || d.Fleet.Flux == nil {
			want[k] = struct{}{}
		}
	}
	// TRANSITION (F16): the lane lands before foundry-dies narrows
	// data.fleet.stars, so a roster built by the OLD derive.rego carries every
	// repo as a star. Both counts pass until foundry-dies F16 lands; then the
	// stars == len(repoKeys) arm below is deleted.
	if len(repoKeys) > 0 && stars != len(want) && stars != len(repoKeys) {
		return mapped, stars, topics, fmt.Sprintf("the built roster derives %d stars from %d flux-backed repos (of %d declared/lean keys) — a declared entry without a kind drops its star silently, and a repo flux does not deploy is not a star", stars, len(want), len(repoKeys)), nil
	}
	// A lean row that states an MCP interface or an address with a port is a
	// deployed star by its own account. If flux backs no entry for it, or no
	// star row came out, the narrowing would drop a live route silently
	// (blade-runner's Service named no namespace, and flux said nothing for it).
	var routed []string
	for k, raw := range d.Fleet.Lean {
		var row struct {
			Address   string `json:"address"`
			Interface struct {
				Sync []string `json:"sync"`
			} `json:"interface"`
		}
		if json.Unmarshal(raw, &row) != nil {
			continue
		}
		mcp := false
		for _, i := range row.Interface.Sync {
			mcp = mcp || i == "mcp"
		}
		if !mcp && !strings.Contains(row.Address, ":") {
			continue
		}
		_, backed := d.Fleet.Flux[k]
		if !backed || !hasKey(d.Fleet.Stars, k) {
			routed = append(routed, k)
		}
	}
	if len(routed) > 0 {
		sort.Strings(routed)
		return mapped, stars, topics, fmt.Sprintf("the built roster routes %s (a lean row states mcp or an address with a port) but flux has no entry or data.fleet.stars no row for it — the narrowed stars would drop a live route", strings.Join(routed, ", ")), nil
	}
	// data.fleet.repos is the same union, one catalog row per key, and the
	// rule is on foundry-dies main, so it is held to the count unconditionally
	// whenever the union is non-empty. OPA emits "repos":{} when every row is
	// undefined, and omits the key when the rule is gone or renamed: both are
	// a count of 0, so neither passes silently. Stars is not held to it.
	if len(repoKeys) > 0 {
		if repos := size(d.Fleet.Repos); repos != len(repoKeys) {
			return mapped, stars, topics, fmt.Sprintf("the built roster derives %d repos from %d declared/lean keys — a declared entry missing a required field drops its repo silently, and a missing repos rule derives none", repos, len(repoKeys)), nil
		}
	}
	for _, row := range rows {
		var fields map[string]json.RawMessage
		if json.Unmarshal(row, &fields) == nil {
			if _, leaked := fields["charter"]; leaked {
				return mapped, stars, topics, "slag.json leaked into the bundle — the lean/full split is broken", nil
			}
		}
	}
	return mapped, stars, topics, "", nil
}

// hasKey is whether an object (or the array form's named rows) holds key.
func hasKey(raw json.RawMessage, key string) bool {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil {
		_, ok := obj[key]
		return ok
	}
	var arr []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &arr) == nil {
		for _, a := range arr {
			if a.Name == key {
				return true
			}
		}
	}
	return false
}

// size is jq's length over an object or an array; anything else is 0.
func size(raw json.RawMessage) int {
	return len(members(raw))
}

// members is jq's .[] over an object's values or an array's elements.
func members(raw json.RawMessage) []json.RawMessage {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) == nil {
		out := make([]json.RawMessage, 0, len(obj))
		for _, v := range obj {
			out = append(out, v)
		}
		return out
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		return arr
	}
	return nil
}

// PlantedSeams is the composition gate's probe: two seams admission must deny
// only because data.fleet resolved. A count of roster keys cannot tell a live
// rule from a dead one; a planted violation can.
const PlantedSeams = `{"name":"gate-probe","seams":[{"to":"definitely-not-a-real-star","topic":null},{"to":"ananke","topic":"not-a-registered-topic"}]}`

// RosterDenials counts the denials that name the fleet roster.
func RosterDenials(denies []string) int {
	n := 0
	for _, d := range denies {
		if strings.Contains(d, "fleet roster") {
			n++
		}
	}
	return n
}

var sha256sumLine = regexp.MustCompile(`^([0-9a-f]{64})\s`)

// FileDigest reads `sha256sum <file>` into the digest a registry would name
// the file's bytes by.
func FileDigest(out string) (string, error) {
	m := sha256sumLine.FindStringSubmatch(strings.TrimSpace(out) + " ")
	if m == nil {
		return "", fmt.Errorf("sha256sum answered no digest: %.120q", out)
	}
	return "sha256:" + m[1], nil
}

// PublishedLayer reads the first layer's digest off an OCI manifest, or "".
func PublishedLayer(manifest string) string {
	var m struct {
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
	}
	if json.Unmarshal([]byte(manifest), &m) != nil || len(m.Layers) == 0 {
		return ""
	}
	return m.Layers[0].Digest
}

// DockerConfig is the docker config JSON that logs user in to host with token,
// what oras's --registry-config and cosign's DOCKER_CONFIG read. It carries the
// credential and is only ever handed to the engine as a secret.
func DockerConfig(host, user, token string) string {
	auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + token))
	b, _ := json.Marshal(map[string]any{"auths": map[string]any{host: map[string]string{"auth": auth}}})
	return string(b)
}

// DecodeKey reads a base64-wrapped PEM key, the shape OPA_BUNDLE_SIGNING_KEY
// and COSIGN_PRIVATE_KEY arrive in. The error never carries the key.
func DecodeKey(name, encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", fmt.Errorf("%s is not base64", name)
	}
	if block, _ := pem.Decode(raw); block == nil {
		return "", fmt.Errorf("%s decodes to something that is not a PEM key", name)
	}
	return string(raw), nil
}

// EphemeralKey mints a throwaway P-256 key as SEC1 PEM. A dry run with no
// signing key builds and gates the signed twins with it, because the
// alternative is skipping three gates and calling that verified.
func EphemeralKey() (string, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})), nil
}
