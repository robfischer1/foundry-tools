package buildlane

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTheSBOMLayerIsReadAsTheRegistryStoredIt(t *testing.T) {
	blob := "sha256:" + strings.Repeat("b", 64)
	good := `{"schemaVersion":2,"artifactType":"application/vnd.cyclonedx+json","layers":[{"mediaType":"application/vnd.cyclonedx+json","digest":"` + blob + `","size":4812337}]}`
	digest, size, err := SBOMLayer(good)
	if err != nil || digest != blob || size != 4812337 {
		t.Fatalf("SBOMLayer = %q, %d, %v; want %q, 4812337", digest, size, err, blob)
	}
	if _, size, err := SBOMLayer(strings.Replace(good, "4812337", "1", 1)); err != nil || size != 1 {
		t.Errorf("a one-byte SBOM is still an SBOM: %d, %v", size, err)
	}

	for name, tc := range map[string]struct{ manifest, want string }{
		"not JSON":           {"<html>502</html>", "not JSON"},
		"no layers":          {`{"layers":[]}`, "carries 0 layers"},
		"two layers":         {`{"layers":[{"mediaType":"application/vnd.cyclonedx+json","digest":"` + blob + `","size":1},{"mediaType":"application/vnd.cyclonedx+json","digest":"` + blob + `","size":1}]}`, "carries 2 layers"},
		"an empty digest":    {`{"layers":[{"mediaType":"application/vnd.cyclonedx+json","digest":"","size":10}]}`, "names no digest and size"},
		"a digest with more": {`{"layers":[{"mediaType":"application/vnd.cyclonedx+json","digest":"x` + blob + `","size":10}]}`, "names no digest and size"},
		"a zero size":        {`{"layers":[{"mediaType":"application/vnd.cyclonedx+json","digest":"` + blob + `","size":0}]}`, "names no digest and size"},
		"a negative size":    {`{"layers":[{"mediaType":"application/vnd.cyclonedx+json","digest":"` + blob + `","size":-3}]}`, "names no digest and size"},
		"another media type": {`{"layers":[{"mediaType":"application/octet-stream","digest":"` + blob + `","size":10}]}`, "is application/octet-stream, not application/vnd.cyclonedx+json"},
	} {
		if _, _, err := SBOMLayer(tc.manifest); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: SBOMLayer must refuse with %q: %v", name, tc.want, err)
		}
	}
}

func TestThePointerPredicateNamesTheReferrerTheBlobAndItsSize(t *testing.T) {
	manifest := "sha256:" + strings.Repeat("a", 64)
	blob := "sha256:" + strings.Repeat("b", 64)
	var got struct {
		SBOM map[string]any `json:"sbom"`
	}
	if err := json.Unmarshal([]byte(SBOMRefPredicate(manifest, blob, 4812337)), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"artifactManifest": manifest,
		"blob":             blob,
		"size":             float64(4812337),
		"mediaType":        "application/vnd.cyclonedx+json",
		"format":           "CycloneDX",
		"generator":        "syft",
	}
	if len(got.SBOM) != len(want) {
		t.Fatalf("the predicate carries %d fields, want %d: %v", len(got.SBOM), len(want), got.SBOM)
	}
	for k, v := range want {
		if got.SBOM[k] != v {
			t.Errorf("sbom.%s = %v, want %v", k, got.SBOM[k], v)
		}
	}
	if SBOMRefType != "https://notusmi.com/attestation/sbom-ref/v1" {
		t.Errorf("the predicate type is the reader's contract and moved: %s", SBOMRefType)
	}
}
