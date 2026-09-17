package buildlane

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	baseDigest = "sha256:9a21aefeb6af6265024a4aa614b66607a3eb65810d8739e0c8a410af51b74459"
	bomDigest  = "sha256:a84162e704746cffbf575f3051e34d498ddfaf29cc8ea88edf8e244081d8c80b"
)

func bom(t *testing.T, comps ...map[string]any) []byte {
	t.Helper()
	doc := map[string]any{"bomFormat": "CycloneDX", "specVersion": "1.6", "components": comps}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func pkg(name, version string) map[string]any {
	return map[string]any{"type": "library", "name": name, "version": version, "purl": "pkg:pypi/" + name + "@" + version}
}

func file(path string) map[string]any {
	return map[string]any{"type": "file", "name": path, "hashes": []map[string]string{{"alg": "SHA-256", "content": strings.Repeat("0", 64)}}}
}

func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("composed document is not JSON: %v", err)
	}
	return doc
}

func names(doc map[string]any) []string {
	var out []string
	for _, c := range components(doc) {
		m := c.(map[string]any)
		out = append(out, m["name"].(string))
	}
	return out
}

func TestComposeSBOMSubtractsTheBaseAndLinksIt(t *testing.T) {
	own := bom(t, pkg("anyio", "4.14.2"), pkg("uv", "0.9.0"), pkg("openssl", "3.0"), file("/usr/share/base-files/motd"), file("/etc/profile"))
	base := bom(t, pkg("uv", "0.9.0"), pkg("openssl", "3.0"), pkg("libc", "2.41"))
	link := &BaseLink{Repo: "registry.notusmi.com/rob/stellar_core", Blob: bomDigest, Size: 3414}

	out, st, err := ComposeSBOM(own, base, link)
	if err != nil {
		t.Fatalf("ComposeSBOM: %v", err)
	}
	want := ComposeStats{Own: 5, Files: 2, Shared: 2, Kept: 1, Linked: true}
	if st != want {
		t.Errorf("stats = %+v, want %+v", st, want)
	}
	doc := decode(t, out)
	if got := names(doc); len(got) != 1 || got[0] != "anyio" {
		t.Errorf("components = %v, want the star's own anyio alone", got)
	}
	refs, _ := doc["externalReferences"].([]any)
	if len(refs) != 1 {
		t.Fatalf("externalReferences = %v, want the one bom link", refs)
	}
	ref := refs[0].(map[string]any)
	if ref["type"] != SBOMLinkType || ref["url"] != "oci://registry.notusmi.com/rob/stellar_core@"+bomDigest {
		t.Errorf("link = %v, want type bom at the oci locator", ref)
	}
	hashes := ref["hashes"].([]any)
	h := hashes[0].(map[string]any)
	if h["alg"] != "SHA-256" || h["content"] != strings.TrimPrefix(bomDigest, "sha256:") {
		t.Errorf("hashes = %v, want SHA-256 of the linked document's digest, without its prefix", hashes)
	}
	comps, _ := doc["compositions"].([]any)
	if len(comps) != 1 || comps[0].(map[string]any)["aggregate"] != SBOMAggregateFirstParty {
		t.Errorf("compositions = %v, want aggregate %q", comps, SBOMAggregateFirstParty)
	}
	if !strings.Contains(st.String(), "1 components kept of 5") || !strings.Contains(st.String(), "2 shared with the base") {
		t.Errorf("stats line = %q", st.String())
	}
}

func TestComposeSBOMWithoutABaseIsSelfContained(t *testing.T) {
	// Every image published before the producer flipped carries this shape,
	// and a base with no SBOM of its own leaves the star's the same way: all
	// its components, no link, no declaration — the reader takes it as is.
	own := bom(t, pkg("anyio", "4.14.2"), pkg("uv", "0.9.0"), file("/etc/profile"))
	out, st, err := ComposeSBOM(own, nil, nil)
	if err != nil {
		t.Fatalf("ComposeSBOM: %v", err)
	}
	if want := (ComposeStats{Own: 3, Files: 1, Kept: 2}); st != want {
		t.Errorf("stats = %+v, want %+v", st, want)
	}
	doc := decode(t, out)
	if got := names(doc); len(got) != 2 {
		t.Errorf("components = %v, want both packages kept", got)
	}
	if _, has := doc["externalReferences"]; has {
		t.Errorf("a self-contained document names a link: %v", doc["externalReferences"])
	}
	if _, has := doc["compositions"]; has {
		t.Errorf("a self-contained document declares a composition: %v", doc["compositions"])
	}
	if !strings.Contains(st.String(), "self-contained") {
		t.Errorf("stats line = %q, want it to say so", st.String())
	}
}

func TestComposeSBOMCollapsesDuplicatesAndKeepsOtherReferences(t *testing.T) {
	// syft catalogs a crate once per binary that embeds it: 1,028 cargo
	// entries on iris were 514 packages. And a document's existing external
	// references (vcs, say) are kept beside the link, not replaced by it.
	own := map[string]any{
		"bomFormat": "CycloneDX", "specVersion": "1.6",
		"components":         []map[string]any{pkg("serde", "1.0"), pkg("serde", "1.0"), pkg("anyio", "4.14.2")},
		"externalReferences": []map[string]any{{"type": "vcs", "url": "https://forgejo.notusmi.com/rob/iris"}},
	}
	raw, err := json.Marshal(own)
	if err != nil {
		t.Fatal(err)
	}
	out, st, err := ComposeSBOM(raw, bom(t, pkg("libc", "2.41")), &BaseLink{Repo: "r/b", Blob: bomDigest})
	if err != nil {
		t.Fatalf("ComposeSBOM: %v", err)
	}
	if st.Kept != 2 || st.Shared != 0 {
		t.Errorf("stats = %+v, want 2 kept (serde once) and nothing shared", st)
	}
	doc := decode(t, out)
	refs, _ := doc["externalReferences"].([]any)
	if len(refs) != 2 || refs[0].(map[string]any)["type"] != "vcs" || refs[1].(map[string]any)["type"] != SBOMLinkType {
		t.Errorf("externalReferences = %v, want the vcs reference kept and the bom link appended", refs)
	}
}

func TestComposeSBOMRefusesWhatIsNotADocument(t *testing.T) {
	if _, _, err := ComposeSBOM([]byte("{"), nil, nil); err == nil || !strings.Contains(err.Error(), "image SBOM") {
		t.Errorf("a broken image document was accepted: %v", err)
	}
	if _, _, err := ComposeSBOM(bom(t), []byte("{"), &BaseLink{Repo: "r/b", Blob: bomDigest}); err == nil || !strings.Contains(err.Error(), "base SBOM") {
		t.Errorf("a broken base document was accepted: %v", err)
	}
}

func TestBaseLinkLocatorIsTheReadersForm(t *testing.T) {
	l := BaseLink{Repo: "registry.notusmi.com/foundry/base-images/python", Blob: bomDigest}
	if got, want := l.Locator(), "oci://registry.notusmi.com/foundry/base-images/python@"+bomDigest; got != want {
		t.Errorf("Locator = %q, want %q", got, want)
	}
}

func TestRuntimeBase(t *testing.T) {
	pinned := "registry.notusmi.com/rob/stellar_core:python-runtime@" + baseDigest
	cases := []struct {
		name, dockerfile, ref, digest string
		ok                            bool
	}{
		{"one stage, pinned", "FROM " + pinned + "\nCOPY app app\n", pinned, baseDigest, true},
		{"the last FROM is the runtime", "FROM golang:1.26 AS builder\nRUN go build\nFROM " + pinned + "\nCOPY --from=builder /out /app\n", pinned, baseDigest, true},
		{"a platform flag before the image", "FROM --platform=linux/amd64 " + pinned + " AS runtime\n", pinned, baseDigest, true},
		{"lower case and indented", "  from " + pinned + " as final\n", pinned, baseDigest, true},
		{"a final stage built from a named stage", "FROM " + pinned + " AS base\nRUN apt-get update\nFROM base\nCOPY app app\n", pinned, baseDigest, true},
		{"stage names are case-insensitive", "FROM " + pinned + " AS Base\nFROM base\n", pinned, baseDigest, true},
		{"unpinned answers the reference and no digest", "FROM registry.notusmi.com/rob/go-base-image:stable\n", "registry.notusmi.com/rob/go-base-image:stable", "", true},
		{"a build argument is not a base the lane can name", "ARG BASE\nFROM ${BASE}\n", "", "", false},
		{"scratch is no base at all", "FROM golang:1.26 AS builder\nFROM scratch\nCOPY --from=builder /x /x\n", "", "", false},
		{"nor is SCRATCH", "FROM SCRATCH\n", "", "", false},
		{"a stage that names itself", "FROM loop AS loop\n", "", "", false},
		{"no FROM at all", "RUN true\n", "", "", false},
		{"COPY --from is not a FROM", "FROM " + pinned + "\nCOPY --from=builder /x /x\n", pinned, baseDigest, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ref, digest, ok := RuntimeBase(c.dockerfile)
			if ok != c.ok || ref != c.ref || digest != c.digest {
				t.Errorf("RuntimeBase = (%q, %q, %v), want (%q, %q, %v)", ref, digest, ok, c.ref, c.digest, c.ok)
			}
		})
	}
}

func TestRepoOf(t *testing.T) {
	cases := map[string]string{
		"registry.notusmi.com/rob/stellar_core:python-runtime@" + baseDigest: "registry.notusmi.com/rob/stellar_core",
		"registry.notusmi.com/rob/go-base-image:stable":                      "registry.notusmi.com/rob/go-base-image",
		"registry.notusmi.com/rob/athena@" + baseDigest:                      "registry.notusmi.com/rob/athena",
		"host:5000/rob/athena:stable":                                        "host:5000/rob/athena",
		"registry.notusmi.com/rob/athena":                                    "registry.notusmi.com/rob/athena",
	}
	for in, want := range cases {
		if got := RepoOf(in); got != want {
			t.Errorf("RepoOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewestReferrer(t *testing.T) {
	const cdx = "application/vnd.cyclonedx+json"
	older := "sha256:" + strings.Repeat("1", 64)
	newer := "sha256:" + strings.Repeat("2", 64)
	undated := "sha256:" + strings.Repeat("3", 64)
	answer := func(refs ...map[string]any) []byte {
		raw, err := json.Marshal(map[string]any{"referrers": refs})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	ref := func(digest, typ, created string) map[string]any {
		r := map[string]any{"digest": digest, "artifactType": typ}
		if created != "" {
			r["annotations"] = map[string]string{"org.opencontainers.image.created": created}
		}
		return r
	}
	cases := []struct {
		name string
		raw  []byte
		want string
		ok   bool
	}{
		{"the newest by created wins, whatever the order", answer(ref(newer, cdx, "2026-09-17T00:00:00Z"), ref(older, cdx, "2026-09-16T00:00:00Z")), newer, true},
		{"listed the other way round", answer(ref(older, cdx, "2026-09-16T00:00:00Z"), ref(newer, cdx, "2026-09-17T00:00:00Z")), newer, true},
		{"a dated one beats an undated one", answer(ref(undated, cdx, ""), ref(older, cdx, "2026-09-16T00:00:00Z")), older, true},
		{"among undated, the last listed", answer(ref(older, cdx, ""), ref(undated, cdx, "")), undated, true},
		{"other artifact types are not SBOMs", answer(ref(newer, "application/vnd.dev.sigstore.bundle.v0.3+json", "2026-09-17T00:00:00Z")), "", false},
		{"a referrer whose digest is not one is skipped", answer(ref("sha256:short", cdx, "2026-09-17T00:00:00Z"), ref(older, cdx, "2026-09-16T00:00:00Z")), older, true},
		{"none is not an error", answer(), "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok, err := NewestReferrer(c.raw, cdx)
			if err != nil {
				t.Fatalf("NewestReferrer: %v", err)
			}
			if ok != c.ok || got != c.want {
				t.Errorf("NewestReferrer = (%q, %v), want (%q, %v)", got, ok, c.want, c.ok)
			}
		})
	}
	if _, _, err := NewestReferrer([]byte("not json"), cdx); err == nil {
		t.Error("a non-JSON answer was accepted")
	}
}
