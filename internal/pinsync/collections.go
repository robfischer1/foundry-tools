package pinsync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// CollectionsFile is the ansible collections the tools container installs. Its
// `version` is what Renovate moves (the customManager in renovate.json5, which
// reads the name and version of each entry); the `url` and `sha256` under it
// name the artifact of THAT version and are not Renovate's to move, because the
// built-in ansible-galaxy manager skips an entry that carries keys beyond
// name/version/source/type (its `default:` case marks the dep "unsupported"),
// and no datasource states a galaxy tarball's sum.
const CollectionsFile = "pytools/ansible-collections.yml"

// galaxyArtifacts is where galaxy serves a collection's tarball:
// <namespace>-<name>-<version>.tar.gz under it.
const galaxyArtifacts = "https://galaxy.ansible.com/api/v3/plugin/ansible/content/published/collections/artifacts/"

// galaxyVersions is galaxy's own record of a version, whose artifact states the
// tarball's sha256: <namespace>/<name>/versions/<version>/ under it.
const galaxyVersions = "https://galaxy.ansible.com/api/v3/plugin/ansible/content/published/collections/index/"

var (
	collectionName    = regexp.MustCompile(`(?m)^([ \t]*-[ \t]*name:[ \t]*)(\S+)[ \t]*$`)
	collectionVersion = regexp.MustCompile(`(?m)^[ \t]+version:[ \t]*["']?([^"'\s]+)["']?[ \t]*$`)
	collectionURL     = regexp.MustCompile(`(?m)^([ \t]+url:[ \t]*)(\S+)[ \t]*$`)
	collectionSum     = regexp.MustCompile(`(?m)^([ \t]+sha256:[ \t]*)([0-9a-f]{64})[ \t]*$`)
)

// artifactURL is the tarball address of a collection version.
func artifactURL(name, version string) string {
	return galaxyArtifacts + strings.Replace(name, ".", "-", 1) + "-" + version + ".tar.gz"
}

// syncCollections moves the url and sha256 of each entry whose url is not the
// address of its version, and answers the file's new text ("" when it is in
// step, or when the tree has no such file). The sum is measured from the
// tarball and held to galaxy's own record of the version. A dry run answers the
// text with the url moved and the sums as they were, fetching nothing: the
// file is named as changed.
//
// An entry that does not read (no version, url or sha256) is an error: the
// file is the record the installer holds the artifact to, and a guess is
// worse than a stop.
func (e *Env) syncCollections(ctx context.Context) (string, error) {
	raw, err := os.ReadFile(filepath.Join(e.Root, filepath.FromSlash(CollectionsFile)))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	text := string(raw)
	// one block per entry: from its `- name:` line to the next entry's
	idx := collectionName.FindAllStringSubmatchIndex(text, -1)
	var out strings.Builder
	last := 0
	for i, m := range idx {
		end := len(text)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		block := text[m[0]:end]
		name := text[m[4]:m[5]]
		moved, err := e.syncCollection(ctx, name, block)
		if err != nil {
			return "", fmt.Errorf("%s %s: %w", CollectionsFile, name, err)
		}
		out.WriteString(text[last:m[0]])
		out.WriteString(moved)
		last = end
	}
	out.WriteString(text[last:])
	if out.String() == text {
		return "", nil
	}
	return out.String(), nil
}

// syncCollection answers one entry's block with its url and sha256 moved to
// the version it names. The url is the record of the version the sum was taken
// for: a url that already is the version's address keeps its sum and fetches
// nothing.
func (e *Env) syncCollection(ctx context.Context, name, block string) (string, error) {
	v := collectionVersion.FindStringSubmatch(block)
	u := collectionURL.FindStringSubmatchIndex(block)
	s := collectionSum.FindStringSubmatchIndex(block)
	if v == nil || u == nil || s == nil || u[5] > s[4] {
		return "", fmt.Errorf("the entry has no version, then url, then sha256 to keep in step")
	}
	want := artifactURL(name, v[1])
	if block[u[4]:u[5]] == want {
		return block, nil
	}
	sum := block[s[4]:s[5]] // a dry run fetches nothing: the url alone marks the file
	if e.Write {
		var err error
		if sum, err = e.collectionSum(ctx, name, v[1], want); err != nil {
			return "", err
		}
		fmt.Fprintf(e.Log, "pinsync: %s %s sha256 %s\n", name, want, sum)
	} else {
		fmt.Fprintf(e.Log, "pinsync: %s moved to %s and needs its sha256 fetched\n", name, want)
	}
	return block[:u[4]] + want + block[u[5]:s[4]] + sum + block[s[5]:], nil
}

// collectionSum measures the tarball's sha256 and holds it to galaxy's record
// of the version. A record that cannot be read is logged and leaves the
// measured sum standing, as sumOf does for a publisher who states none; one
// that disagrees is an error and nothing is written.
func (e *Env) collectionSum(ctx context.Context, name, version, artifact string) (string, error) {
	measured, err := e.sumOf(ctx, artifact)
	if err != nil {
		return "", err
	}
	ns, coll, _ := strings.Cut(name, ".")
	record := galaxyVersions + ns + "/" + coll + "/versions/" + version + "/"
	b, err := e.read(ctx, record, false, 1<<20)
	if err != nil {
		fmt.Fprintf(e.Log, "pinsync: %s: could not read galaxy's record (%v); its sum goes unchecked\n", name, err)
		return measured, nil
	}
	var doc struct {
		Artifact struct {
			SHA256 string `json:"sha256"`
		} `json:"artifact"`
	}
	if json.Unmarshal(b, &doc) != nil || doc.Artifact.SHA256 == "" {
		fmt.Fprintf(e.Log, "pinsync: %s: galaxy's record states no sha256; its sum goes unchecked\n", name)
		return measured, nil
	}
	if doc.Artifact.SHA256 != measured {
		return "", fmt.Errorf("%s measures %s but galaxy's record states %s", artifact, measured, doc.Artifact.SHA256)
	}
	return measured, nil
}
