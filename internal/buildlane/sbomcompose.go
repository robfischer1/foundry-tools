package buildlane

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// THE COMPOSED SBOM (CA master-plan F14, "F14's SBOM contract"). A star's SBOM
// carries its OWN components and a link to its base image's document, rather
// than the base's inventory copied in again.
//
// MEASURED ON THE LIVE REGISTRY, 2026-09-17: rob/iris:stable carried 730
// packages, 696 of them (95%) byte-identical to its base's, whose own SBOM was
// already a referrer in the same registry (3.4 MB, 725 packages); mnemosyne
// and terpsichore 94% each. Every star re-stored its base's inventory on every
// build, and the fleet's SBOM referrers came to 2.7 GB against 2.4 GB for the
// 44 images they described. The duplicated bulk is the base's pypi and cargo
// contents, not its deb list, so no cataloger filter separates it — only a
// subtraction against the base's document does, which is what ComposeSBOM is.
//
// THE READER LANDED FIRST (foundry-stocks#204): forge-portfolio follows the
// link, unions the two documents, and refuses a document that declares itself
// first-party-only with nowhere to find the rest. The shapes below are that
// reader's, exactly — change one side and the other refuses the record.

// SBOMLinkType is the CycloneDX externalReferences type that names another
// document (1.4 added it; the fleet emits 1.6).
const SBOMLinkType = "bom"

// SBOMAggregateFirstParty is the compositions aggregate a first-party document
// declares: its own components are here, the rest is in the linked document.
const SBOMAggregateFirstParty = "incomplete_first_party_only"

// sbomLocatorScheme is the locator form the reader follows. The spec's own is
// urn:cdx:<serialNumber>/<version>, which names a document in a registry of
// documents the fleet does not have; an OCI blob address is resolvable by
// anything that can reach zot, and is its own integrity.
const sbomLocatorScheme = "oci://"

// BaseLink is where the base image's SBOM lives: the repository (host
// qualified, as oras and the reader take it) and the document's own digest.
type BaseLink struct {
	Repo string
	Blob string
	Size int64
}

// Locator is the link as the reader parses it: oci://<repo>@<blob>.
func (l BaseLink) Locator() string { return sbomLocatorScheme + l.Repo + "@" + l.Blob }

// ComposeStats is what composing did, for the lane's log.
type ComposeStats struct {
	// Own is the star document's component count as syft wrote it.
	Own int
	// Files is how many of those were file entries, dropped.
	Files int
	// Shared is how many of the rest the base's document also carries, dropped.
	Shared int
	// Kept is what the composed document carries.
	Kept int
	// Linked is whether a base document was named.
	Linked bool
}

func (s ComposeStats) String() string {
	if !s.Linked {
		return fmt.Sprintf("SBOM: %d components kept of %d (%d file entries dropped); no base document to link, so the SBOM is self-contained", s.Kept, s.Own, s.Files)
	}
	return fmt.Sprintf("SBOM: %d components kept of %d (%d file entries dropped, %d shared with the base and linked instead)", s.Kept, s.Own, s.Files, s.Shared)
}

// ComposeSBOM makes the star's document first-party.
//
// FILE ENTRIES GO FIRST, whatever the base says. Measured across 12 stars,
// syft's per-file SHA-1/SHA-256 components were 7.5 MB of 13.3 MB — 56% of the
// fleet's SBOM volume — and the only reader takes components carrying a purl,
// so nothing has ever read one. The scan is asked not to write them
// (SYFT_FILE_METADATA_SELECTION=none); dropping them here as well means the
// contract holds even when a scan was run without that setting.
//
// THEN THE BASE'S COMPONENTS, when a base document is given: every component
// the base also carries, by MergeSBOM's key, is dropped and the base document
// is linked in its place — an externalReferences entry of type bom whose
// locator and SHA-256 both name the base document's digest, and a compositions
// entry declaring first-party scope. A document composed with NO base keeps
// every component and declares nothing: it is the self-contained shape every
// image published before this change carries, and the reader takes it as is.
//
// Duplicates inside the star's own document collapse to one per key, as
// MergeSBOM already did for the builder merge: syft catalogs a crate once per
// binary that embeds it, and 1,028 cargo entries on iris were 514 packages.
func ComposeSBOM(own, base []byte, link *BaseLink) ([]byte, ComposeStats, error) {
	var doc map[string]any
	if err := json.Unmarshal(own, &doc); err != nil {
		return nil, ComposeStats{}, fmt.Errorf("the image SBOM is not JSON: %w", err)
	}
	st := ComposeStats{Own: len(components(doc))}

	var shared map[string]bool
	if link != nil {
		var baseDoc map[string]any
		if err := json.Unmarshal(base, &baseDoc); err != nil {
			return nil, ComposeStats{}, fmt.Errorf("the base SBOM is not JSON: %w", err)
		}
		shared = map[string]bool{}
		for _, c := range components(baseDoc) {
			shared[componentKey(c)] = true
		}
		st.Linked = true
	}

	var kept []any
	seen := map[string]bool{}
	for _, c := range components(doc) {
		if isFileComponent(c) {
			st.Files++
			continue
		}
		k := componentKey(c)
		if shared[k] {
			st.Shared++
			continue
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		kept = append(kept, c)
	}
	doc["components"] = kept
	st.Kept = len(kept)

	if link != nil {
		refs, _ := doc["externalReferences"].([]any)
		refs = append(refs, map[string]any{
			"type": SBOMLinkType,
			"url":  link.Locator(),
			"hashes": []map[string]string{{
				"alg":     "SHA-256",
				"content": strings.TrimPrefix(link.Blob, "sha256:"),
			}},
		})
		doc["externalReferences"] = refs
		doc["compositions"] = []map[string]any{{"aggregate": SBOMAggregateFirstParty}}
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, ComposeStats{}, err
	}
	return out, st, nil
}

func isFileComponent(c any) bool {
	m, _ := c.(map[string]any)
	t, _ := m["type"].(string)
	return t == "file"
}

// fromLine is one Dockerfile FROM: any flags, the image, an optional stage
// name. Case-insensitive, as Dockerfile instructions are.
var fromLine = regexp.MustCompile(`(?im)^\s*FROM\s+(?:--[^\s]+\s+)*([^\s]+)(?:\s+AS\s+([^\s]+))?\s*$`)

// RuntimeBase reads the image the runtime stage is built on: the LAST FROM in
// the Dockerfile, followed through stage names, with the digest it pins.
//
// The last FROM is the final stage, and the final stage is the runtime — that
// is what a multi-stage Dockerfile means. A final stage built FROM an earlier
// stage's name resolves to that stage's own image; a FROM that names a build
// argument (`FROM ${BASE}`) or a stage that cannot be found answers nothing,
// because a base the lane cannot name is a base it cannot link.
//
// An unpinned base (no @sha256) answers its reference and no digest: the link
// needs the digest, and the reference alone is only worth a label.
func RuntimeBase(dockerfile string) (ref, digest string, ok bool) {
	matches := fromLine.FindAllStringSubmatch(dockerfile, -1)
	if len(matches) == 0 {
		return "", "", false
	}
	stages := map[string]string{}
	for _, m := range matches {
		if m[2] != "" {
			stages[strings.ToLower(m[2])] = m[1]
		}
	}
	image := matches[len(matches)-1][1]
	// Follow stage names, bounded by the stage count so a Dockerfile naming a
	// stage after itself cannot loop.
	for range matches {
		next, isStage := stages[strings.ToLower(image)]
		if !isStage {
			break
		}
		image = next
	}
	if strings.Contains(image, "$") {
		return "", "", false
	}
	if _, isStage := stages[strings.ToLower(image)]; isStage {
		return "", "", false
	}
	// scratch is Docker's reserved empty image, not a reference to anything:
	// an image built on it has no base to label, link or subtract.
	if strings.EqualFold(image, "scratch") {
		return "", "", false
	}
	return image, DigestOf(image), true
}

// RepoOf strips a reference to its repository: registry/ns/name, without the
// tag or the digest. A registry port keeps its colon, which is why the tag is
// cut only in the last path element. Written with Cut and Split rather than
// index comparisons: a reference carries one '@' at most, and a boundary on
// an index no reference can reach is a mutant no test can kill.
func RepoOf(ref string) string {
	repo, _, _ := strings.Cut(ref, "@")
	dir, last := path.Split(repo)
	name, _, _ := strings.Cut(last, ":")
	return dir + name
}

// discovery is what `oras discover --format json` answers.
type discovery struct {
	Referrers []struct {
		Digest       string            `json:"digest"`
		ArtifactType string            `json:"artifactType"`
		Annotations  map[string]string `json:"annotations"`
	} `json:"referrers"`
}

// NewestReferrer picks the referrer of artifactType to follow: the one created
// last, by its org.opencontainers.image.created annotation — the lane stamps
// one on every SBOM it attaches — and, among those without one, the last
// listed. None is not an error: a base published before its lane attached
// SBOMs has none, and the star's document is then self-contained.
func NewestReferrer(raw []byte, artifactType string) (digest string, ok bool, err error) {
	var d discovery
	if err := json.Unmarshal(raw, &d); err != nil {
		return "", false, fmt.Errorf("oras discover's answer is not JSON: %w", err)
	}
	created := ""
	for _, r := range d.Referrers {
		if r.ArtifactType != artifactType || DigestOf(r.Digest) != r.Digest {
			continue
		}
		at := r.Annotations["org.opencontainers.image.created"]
		if !ok || at >= created {
			digest, ok, created = r.Digest, true, at
		}
	}
	return digest, ok, nil
}
