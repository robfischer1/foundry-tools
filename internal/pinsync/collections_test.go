package pinsync

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const routerosOld = galaxyArtifacts + "community-routeros-3.22.0.tar.gz"
const routerosNew = galaxyArtifacts + "community-routeros-3.23.0.tar.gz"

// collectionsFile is the real file's shape: a header of comments that mention
// name and version, then entries of name, version, url and sha256.
func collectionsFile(routerosVersion, routerosURL, routerosSum string) string {
	return `---
# THE ANSIBLE COLLECTIONS: a bump moves name: version: url: sha256:.
collections:
  - name: community.routeros
    version: "` + routerosVersion + `"
    url: ` + routerosURL + `
    sha256: ` + routerosSum + `
  - name: vyos.vyos
    version: "6.0.0"
    url: ` + galaxyArtifacts + `vyos-vyos-6.0.0.tar.gz
    sha256: ` + sum("vyos") + `
`
}

func collectionsEnv(t *testing.T, body string, web *fakeWeb, write bool) (*Env, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pytools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, CollectionsFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	log := &bytes.Buffer{}
	return &Env{Root: root, HTTP: &http.Client{Transport: web}, GitHubAPI: "https://api.test", Write: write, Log: log}, log
}

// Renovate's customManager moves the version alone; the url and the sum of the
// entry that moved follow it, held to galaxy's record, and the neighbour that
// did not move is untouched and never fetched.
func TestACollectionVersionMovesItsUrlAndSum(t *testing.T) {
	record := galaxyVersions + "community/routeros/versions/3.23.0/"
	web := &fakeWeb{bodies: map[string]string{
		routerosNew: "routeros-3.23.0 tarball",
		record:      `{"artifact":{"sha256":"` + sum("routeros-3.23.0 tarball") + `"}}`,
	}}
	e, _ := collectionsEnv(t, collectionsFile("3.23.0", routerosOld, sum("routeros-3.22.0 tarball")), web, true)
	got, err := e.syncCollections(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := collectionsFile("3.23.0", routerosNew, sum("routeros-3.23.0 tarball")); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	for _, u := range web.asked {
		if strings.Contains(u, "vyos") {
			t.Errorf("an entry in step was fetched: %s", u)
		}
	}
}

// The neighbouring cases: a file in step reads nothing and writes nothing, a
// dry run names the file without fetching, and a tree with no such file has
// nothing to follow.
func TestACollectionsFileInStepFetchesNothing(t *testing.T) {
	web := &fakeWeb{}
	e, _ := collectionsEnv(t, collectionsFile("3.22.0", routerosOld, sum("routeros-3.22.0 tarball")), web, true)
	if got, err := e.syncCollections(t.Context()); got != "" || err != nil || len(web.asked) != 0 {
		t.Errorf("in step: %q %v %v", got, err, web.asked)
	}

	web = &fakeWeb{}
	e, _ = collectionsEnv(t, collectionsFile("3.23.0", routerosOld, sum("old")), web, false)
	got, err := e.syncCollections(t.Context())
	if err != nil || !strings.Contains(got, routerosNew) || !strings.Contains(got, sum("old")) || len(web.asked) != 0 {
		t.Errorf("dry run: %v %v\n%s", err, web.asked, got)
	}

	e = &Env{Root: t.TempDir(), HTTP: &http.Client{Transport: &fakeWeb{}}, Log: &bytes.Buffer{}}
	if got, err := e.syncCollections(t.Context()); got != "" || err != nil {
		t.Errorf("no file: %q %v", got, err)
	}
}

// A sum galaxy does not agree with, a tarball that will not come, and an entry
// that does not read are errors, and nothing is returned to write.
func TestACollectionThatCannotBeVerifiedWritesNothing(t *testing.T) {
	record := galaxyVersions + "community/routeros/versions/3.23.0/"
	for name, tc := range map[string]struct {
		web  map[string]string
		body string
		want string
	}{
		"galaxy disagrees": {map[string]string{routerosNew: "t", record: `{"artifact":{"sha256":"` + sum("other") + `"}}`},
			collectionsFile("3.23.0", routerosOld, sum("o")), "galaxy's record states"},
		"no tarball": {map[string]string{}, collectionsFile("3.23.0", routerosOld, sum("o")), "Not Found"},
		"no sum":     {map[string]string{}, strings.Replace(collectionsFile("3.22.0", routerosOld, sum("o")), "    sha256: "+sum("o")+"\n", "", 1), "no version, then url, then sha256"},
	} {
		e, _ := collectionsEnv(t, tc.body, &fakeWeb{bodies: tc.web}, true)
		got, err := e.syncCollections(t.Context())
		if err == nil || got != "" || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %q %v, want an error naming %q", name, got, err, tc.want)
		}
	}
	// galaxy's record unreadable leaves the measured sum standing, logged
	e, log := collectionsEnv(t, collectionsFile("3.23.0", routerosOld, sum("o")), &fakeWeb{bodies: map[string]string{routerosNew: "t"}}, true)
	got, err := e.syncCollections(t.Context())
	if err != nil || !strings.Contains(got, sum("t")) || !strings.Contains(log.String(), "could not read galaxy's record") {
		t.Errorf("%v %s", err, log)
	}
}

// Through Run: a collection version Renovate moved is followed in the same
// pass as every other pin, and the pass after it finds the tree in step.
func TestRunFollowsACollectionBumpAndThenLeavesItAlone(t *testing.T) {
	fixturePins(t)
	files := fixtureFiles()
	files[CollectionsFile] = collectionsFile("3.23.0", routerosOld, sum("routeros-3.22.0 tarball"))
	root := writeTree(t, files)
	web := &fakeWeb{bodies: map[string]string{
		routerosNew: "routeros-3.23.0 tarball",
		galaxyVersions + "community/routeros/versions/3.23.0/": `{"artifact":{"sha256":"` + sum("routeros-3.23.0 tarball") + `"}}`,
	}}
	env, log := newEnv(root, web, &lockRun{}, true)
	res, err := env.Run(t.Context())
	if err != nil {
		t.Fatalf("%v; log:\n%s", err, log)
	}
	if strings.Join(res.Changed, " ") != CollectionsFile {
		t.Errorf("changed %v, want only %s", res.Changed, CollectionsFile)
	}
	if got, want := read(t, root, CollectionsFile), collectionsFile("3.23.0", routerosNew, sum("routeros-3.23.0 tarball")); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	again, _ := newEnv(root, &fakeWeb{}, &lockRun{}, true)
	if res, err = again.Run(t.Context()); err != nil || len(res.Changed) != 0 {
		t.Errorf("a second pass changed %v (%v)", res.Changed, err)
	}
}

// THE LANDED FILE IS IN STEP, and a version bump that did not bring its url
// along is red here rather than a tarball the installer cannot match: the
// customManager in renovate.json5 moves the version alone, pinsync is meant to
// follow, and this holds the result whichever of them ran.
func TestTheLandedCollectionsFileNamesTheArtifactOfEachVersion(t *testing.T) {
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, CollectionsFile)); err != nil {
		t.Fatal(err)
	}
	web := &fakeWeb{}
	e := &Env{Root: root, HTTP: &http.Client{Transport: web}, Write: false, Log: &bytes.Buffer{}}
	got, err := e.syncCollections(t.Context())
	if err != nil || got != "" || len(web.asked) != 0 {
		t.Errorf("%s is not in step with its versions (run `go run ./pinsync`): %v %v\n%s", CollectionsFile, err, web.asked, got)
	}
}

// The rest of the ways it stops: a path that is not a readable file, a galaxy
// record that states no sum (the measured one stands, logged), and an entry
// that does not read, through Run.
func TestCollectionsEdgesStopOrLogAndNeverGuess(t *testing.T) {
	e := &Env{Root: t.TempDir(), HTTP: &http.Client{Transport: &fakeWeb{}}, Log: &bytes.Buffer{}}
	if err := os.MkdirAll(filepath.Join(e.Root, CollectionsFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := e.syncCollections(t.Context()); err == nil || got != "" {
		t.Errorf("a directory where the file should be is an error: %q %v", got, err)
	}

	web := &fakeWeb{bodies: map[string]string{routerosNew: "t", galaxyVersions + "community/routeros/versions/3.23.0/": `{"artifact":{}}`}}
	e2, log := collectionsEnv(t, collectionsFile("3.23.0", routerosOld, sum("o")), web, true)
	got, err := e2.syncCollections(t.Context())
	if err != nil || !strings.Contains(got, sum("t")) || !strings.Contains(log.String(), "states no sha256") {
		t.Errorf("%v %s", err, log)
	}

	fixturePins(t)
	files := fixtureFiles()
	files[CollectionsFile] = "collections:\n  - name: community.routeros\n    version: \"3.23.0\"\n"
	env, _ := newEnv(writeTree(t, files), &fakeWeb{}, &lockRun{}, true)
	if _, err := env.Run(t.Context()); err == nil || !strings.Contains(err.Error(), CollectionsFile) {
		t.Errorf("Run with an entry that does not read: %v", err)
	}
}
