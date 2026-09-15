package buildlane

import (
	"encoding/json"
	"fmt"
)

// THE SBOM RIDES AS A REFERRER; THE ATTESTATION POINTS AT IT (Rob and Zuse7,
// 2026-09-15). zot copies every cosign attestation's payload into its repository
// metadata, and a whole CycloneDX SBOM per image grew that database to 4.6 GB,
// rewritten on every manifest pull — the memory spike that OOM-killed the
// registry. A plain OCI referrer is listed and never inlined (zot's isSignature
// matches notation, the two cosign artifact types and .sig tags only), so the
// SBOM rides as one, and the signed attestation carries only where it is.
const (
	// SBOMMediaType is the referrer's artifact type and its one layer's media type.
	SBOMMediaType = "application/vnd.cyclonedx+json"
	// SBOMRefType is the pointer attestation's predicate type.
	SBOMRefType = "https://notusmi.com/attestation/sbom-ref/v1"
)

// SBOMLayer reads the SBOM referrer's one layer off its manifest as the registry
// stored it. The pointer names these bytes, never a hash of the local file: what
// a reader fetches is what the registry holds.
func SBOMLayer(manifest string) (digest string, size int64, err error) {
	var m struct {
		Layers []struct {
			MediaType string `json:"mediaType"`
			Digest    string `json:"digest"`
			Size      int64  `json:"size"`
		} `json:"layers"`
	}
	if err := json.Unmarshal([]byte(manifest), &m); err != nil {
		return "", 0, fmt.Errorf("the SBOM referrer's manifest is not JSON: %v", err)
	}
	if len(m.Layers) != 1 {
		return "", 0, fmt.Errorf("the SBOM referrer's manifest carries %d layers, not one", len(m.Layers))
	}
	layer := m.Layers[0]
	if layer.Digest == "" || DigestOf(layer.Digest) != layer.Digest || layer.Size <= 0 {
		return "", 0, fmt.Errorf("the SBOM referrer's layer names no digest and size (%q, %d)", layer.Digest, layer.Size)
	}
	if layer.MediaType != SBOMMediaType {
		return "", 0, fmt.Errorf("the SBOM referrer's layer is %s, not %s", layer.MediaType, SBOMMediaType)
	}
	return layer.Digest, layer.Size, nil
}

// SBOMRefPredicate is the pointer attestation's predicate: the referrer's
// manifest, the blob it stores and that blob's size. The blob digest is the
// SBOM's integrity — the registry is content-addressed — so the signature over
// the pointer covers the SBOM it names.
func SBOMRefPredicate(artifactManifest, blob string, size int64) string {
	b, _ := json.Marshal(map[string]any{"sbom": map[string]any{
		"artifactManifest": artifactManifest,
		"blob":             blob,
		"size":             size,
		"mediaType":        SBOMMediaType,
		"format":           "CycloneDX",
		"generator":        "syft",
	}})
	return string(b)
}
