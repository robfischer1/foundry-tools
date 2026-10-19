// The publish lane's record, driven through the real lane on the paper engine
// (cast_record_test.go's reason: the lane is what is under test, not the recorder).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/dagger"
)

// postedRecord is the part of a run record these tests read.
type postedRecord struct {
	Stage     string
	State     int
	Lanes     []string
	Unreached []string
	Atoms     []struct {
		Atom, Group, Result, Reason string
		State                       int
		StartedAt                   string `json:"started_at"`
		FinishedAt                  string `json:"finished_at"`
	}
}

// door is a record door: it keeps what was posted.
type door struct {
	*httptest.Server
	mu   sync.Mutex
	body []string
}

func newDoor(t *testing.T) *door {
	t.Helper()
	d := &door{}
	d.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		d.body = append(d.body, string(b))
		d.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(d.Close)
	return d
}

// posted decodes the one record the door was handed.
func (d *door) posted(t *testing.T) (rec postedRecord) {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.body) != 1 {
		t.Fatalf("the door was handed %d records", len(d.body))
	}
	raw, ok := strings.CutPrefix(d.body[0], "ourea-run-record/1 ")
	if !ok {
		t.Fatalf("not a run record: %.80s", d.body[0])
	}
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func (d *door) none(t *testing.T) {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.body) != 0 {
		t.Fatalf("a record was posted: %.200s", d.body[0])
	}
}

// recorded runs Publish with a record token against door d.
func recorded(t *testing.T, m *FoundryTools, d *door, token, npmToken *dagger.Secret, registry string, dryRun bool) {
	t.Helper()
	m.Repo = d.URL + "/rob/x.git"
	if err := m.Publish(context.Background(), token, publishURL, checkURL, "publisher", buildIndex, npmToken, registry, dryRun, nil, false, dag.SetSecret("record-token", "rt")); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func atomsNamed(t *testing.T, names []string, want ...string) {
	t.Helper()
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("rows %v, want %v", names, want)
	}
}

func names(rec postedRecord) []string {
	var out []string
	for _, a := range rec.Atoms {
		out = append(out, a.Atom)
	}
	return out
}

func TestDEF_B6_publish_record_aPythonPublishRecordsTheExecutorsRows(t *testing.T) {
	d := newDoor(t)
	m := publishOn(t, publishSdist, publishWheel)
	indexAnswers("", "404")
	recorded(t, m, d, dag.SetSecret("pypi-token", publishPass), nil, npmHosted, false)
	rec := d.posted(t)
	atomsNamed(t, names(rec), "python:artifacts", "publish:publish")
	if rec.Stage != "publish" || rec.State != 0 || len(rec.Lanes) != 1 || rec.Lanes[0] != "python" || len(rec.Unreached) != 0 {
		t.Errorf("%+v", rec)
	}
	if a := rec.Atoms[0]; a.Group != "language" || a.Result != "pass" || !strings.Contains(a.Reason, publishSdist) || a.StartedAt == "" || a.FinishedAt == "" {
		t.Errorf("%+v", a)
	}
	if a := rec.Atoms[1]; a.Group != "capability" || a.Result != "pass" || !strings.Contains(a.Reason, "2 uploaded") {
		t.Errorf("%+v", a)
	}
}

func TestDEF_B6_publish_record_aReleasedVersionRecordsNothingToDo(t *testing.T) {
	d := newDoor(t)
	m := publishOn(t, publishSdist, publishWheel)
	indexAnswers(`<a href="../../packages/stellar-core/1.43.0/`+publishWheel+`#sha256=aa">`+publishWheel+`</a>`+
		`<a href="../../packages/stellar-core/1.43.0/`+publishSdist+`#sha256=bb">`+publishSdist+`</a>`, "200")
	recorded(t, m, d, dag.SetSecret("pypi-token", publishPass), nil, npmHosted, false)
	rec := d.posted(t)
	if rec.State != 0 || !strings.Contains(rec.Atoms[1].Reason, "nothing to do") {
		t.Errorf("%+v", rec)
	}
}

func TestDEF_B6_publish_record_aBuildThatFailsLeavesTheUploadUnreached(t *testing.T) {
	d := newDoor(t)
	m := publishOn(t, publishSdist)
	engine.exitCode(buildNeedle, 1)
	engine.stderr(buildNeedle, "Failed to build `/src`")
	recorded(t, m, d, dag.SetSecret("pypi-token", publishPass), nil, npmHosted, false)
	rec := d.posted(t)
	atomsNamed(t, names(rec), "python:artifacts")
	if rec.State != buildlane.Findings || rec.Atoms[0].Result != "findings" || !strings.Contains(rec.Atoms[0].Reason, "findings in uv build") ||
		len(rec.Unreached) != 1 || rec.Unreached[0] != "publish:publish" {
		t.Errorf("%+v", rec)
	}
}

func TestDEF_B6_publish_record_aBuildThatWritesNothingIsTheBuildRowsFinding(t *testing.T) {
	d := newDoor(t)
	m := publishOn(t, ".gitignore")
	recorded(t, m, d, dag.SetSecret("pypi-token", publishPass), nil, npmHosted, false)
	rec := d.posted(t)
	atomsNamed(t, names(rec), "python:artifacts")
	if rec.Atoms[0].State != buildlane.Findings || !strings.Contains(rec.Atoms[0].Reason, "no wheel or sdist") {
		t.Errorf("%+v", rec)
	}
}

func TestDEF_B6_publish_record_aBuildThatNeverRanIsCouldNotRun(t *testing.T) {
	d := newDoor(t)
	m := publishOn(t, publishSdist)
	engine.fail(buildNeedle, "engine down")
	recorded(t, m, d, dag.SetSecret("pypi-token", publishPass), nil, npmHosted, false)
	rec := d.posted(t)
	if rec.State != buildlane.CouldNotRun || rec.Atoms[0].Atom != "python:artifacts" || !strings.Contains(rec.Atoms[0].Reason, "uv build never ran") {
		t.Errorf("%+v", rec)
	}
}

// A lane that stops before the tree has said which half it is files the one row it
// has, under the lane's own group.
func TestDEF_B6_publish_record_aLaneStoppedBeforeTheTreeSaysWhichHalfRecordsOnlyThePublishRow(t *testing.T) {
	d := newDoor(t)
	m := publishOn(t, publishSdist)
	recorded(t, m, d, nil, nil, npmHosted, false) // no token: could not run
	rec := d.posted(t)
	atomsNamed(t, names(rec), "publish:publish")
	if rec.State != buildlane.CouldNotRun || len(rec.Lanes) != 1 || rec.Lanes[0] != "publish" || !strings.Contains(rec.Atoms[0].Reason, "--token is required") {
		t.Errorf("%+v", rec)
	}
}

// WITHOUT THE FLAG THE LANE IS WHAT IT WAS: nothing is posted and nothing is said
// about a record.
func TestDEF_B6_publish_record_withoutATokenNothingIsPostedOrPrinted(t *testing.T) {
	d := newDoor(t)
	var said bytes.Buffer
	old := recordPostOut
	recordPostOut = &said
	t.Cleanup(func() { recordPostOut = old })
	m := publishOn(t, publishSdist, publishWheel)
	m.Repo = d.URL + "/rob/x.git"
	indexAnswers("", "404")
	publishWith(t, m, dag.SetSecret("pypi-token", publishPass), buildIndex, false)
	d.none(t)
	if said.Len() != 0 {
		t.Errorf("the lane said %q", said.String())
	}
}

func TestDEF_B6_publish_record_anNpmPublishRecordsTsPackAndPublish(t *testing.T) {
	d := newDoor(t)
	m := npmOn(t, stellarCoreTS, nil)
	registryAnswers("Not Found", "404")
	recorded(t, m, d, nil, dag.SetSecret("npm-token", npmPass), npmHosted, false)
	rec := d.posted(t)
	atomsNamed(t, names(rec), "ts:pack", "publish:publish")
	if rec.State != 0 || rec.Lanes[0] != "ts" || rec.Atoms[0].Group != "language" || rec.Atoms[0].Result != "pass" ||
		rec.Atoms[1].Group != "capability" || !strings.Contains(rec.Atoms[1].Reason, "@forge/stellar-core-ts@0.6.0") {
		t.Errorf("%+v", rec)
	}
}

func TestDEF_B6_publish_record_anNpmTreeTheLaneCannotPublishStopsAtTsPack(t *testing.T) {
	d := newDoor(t)
	m := npmOn(t, `{"name": "x", "version": "1", "private": true}`, nil)
	recorded(t, m, d, nil, dag.SetSecret("npm-token", npmPass), npmHosted, false)
	rec := d.posted(t)
	atomsNamed(t, names(rec), "ts:pack")
	if rec.State != buildlane.Findings || len(rec.Unreached) != 1 || rec.Unreached[0] != "publish:publish" {
		t.Errorf("%+v", rec)
	}
}

func TestDEF_B6_publish_record_anInstallThatFailsStopsAtTsPack(t *testing.T) {
	d := newDoor(t)
	m := npmOn(t, stellarCoreTS, nil)
	registryAnswers("Not Found", "404")
	engine.exitCode(npmInstallNeedle, 1)
	engine.stderr(npmInstallNeedle, "error: lockfile had changes, but lockfile is frozen")
	recorded(t, m, d, nil, dag.SetSecret("npm-token", npmPass), npmHosted, false)
	rec := d.posted(t)
	atomsNamed(t, names(rec), "ts:pack")
	if !strings.Contains(rec.Atoms[0].Reason, "findings in bun install --frozen-lockfile") {
		t.Errorf("%+v", rec)
	}
}

func TestDEF_B6_publish_record_anInstallThatNeverRanStopsAtTsPack(t *testing.T) {
	d := newDoor(t)
	m := npmOn(t, stellarCoreTS, nil)
	registryAnswers("Not Found", "404")
	engine.fail(npmInstallNeedle, "engine down")
	recorded(t, m, d, nil, dag.SetSecret("npm-token", npmPass), npmHosted, false)
	rec := d.posted(t)
	if rec.State != buildlane.CouldNotRun || !strings.Contains(rec.Atoms[0].Reason, "bun install never ran") {
		t.Errorf("%+v", rec)
	}
}

// A member whose build fails does not strand the others: ts:pack carries the build
// failure and publish:publish still has its row.
func TestDEF_B6_publish_record_aMemberWhoseBuildFailsIsTsPacksFindingAndTheRestStillPublish(t *testing.T) {
	d := newDoor(t)
	m := npmTreeOn(t, theiaTree())
	packumentAnswers("@forge/aglaia", `{"versions": {"0.19.0": {}}}`, "200")
	packumentAnswers("@forge/chrome", "Not Found", "404")
	packumentAnswers("@theia/theme-gijmo", "Not Found", "404")
	engine.exitCode(`"--filter=@forge/chrome"`, 2)
	engine.stderr(`"--filter=@forge/chrome"`, "error TS2307: Cannot find module '@forge/aglaia'")
	recorded(t, m, d, nil, dag.SetSecret("npm-token", npmPass), npmHosted, false)
	rec := d.posted(t)
	atomsNamed(t, names(rec), "ts:pack", "publish:publish")
	if rec.State != buildlane.Findings || rec.Atoms[0].Result != "findings" || !strings.Contains(rec.Atoms[0].Reason, "the build of @forge/chrome@0.7.0") ||
		!strings.Contains(rec.Atoms[1].Reason, "published @theia/theme-gijmo@0.2.1") {
		t.Errorf("%+v", rec)
	}
}

// Builds that fail only on the network are could-not-run, and a refused upload is
// publish:publish's, not ts:pack's.
func TestDEF_B6_publish_record_aBuildThatFailedOnTheNetworkIsCouldNotRunAndAnUploadIsNotABuild(t *testing.T) {
	d := newDoor(t)
	m := npmTreeOn(t, theiaTree())
	packumentAnswers("@forge/aglaia", `{"versions": {"0.19.0": {}}}`, "200")
	packumentAnswers("@forge/chrome", "Not Found", "404")
	packumentAnswers("@theia/theme-gijmo", `{"versions": {"0.2.1": {}}}`, "200")
	engine.exitCode(`"--filter=@forge/chrome"`, 2)
	engine.stderr(`"--filter=@forge/chrome"`, "error: connection refused")
	recorded(t, m, d, nil, dag.SetSecret("npm-token", npmPass), npmHosted, false)
	rec := d.posted(t)
	if rec.State != buildlane.CouldNotRun || rec.Atoms[0].State != buildlane.CouldNotRun {
		t.Errorf("%+v", rec)
	}

	d2 := newDoor(t)
	m = npmOn(t, stellarCoreTS, nil)
	registryAnswers("Not Found", "404")
	engine.exitCode(npmPublishNeedle, 1)
	engine.stderr(npmPublishNeedle, "error: publish failed: 403 Forbidden")
	recorded(t, m, d2, nil, dag.SetSecret("npm-token", npmPass), npmHosted, false)
	rec = d2.posted(t)
	if rec.Atoms[0].Atom != "ts:pack" || rec.Atoms[0].Result != "pass" || rec.Atoms[1].Result != "findings" {
		t.Errorf("%+v", rec)
	}
}

func TestDEF_B6_publish_record_aRegistryThatIsNotAURLRecordsOnlyThePublishRow(t *testing.T) {
	d := newDoor(t)
	m := npmOn(t, stellarCoreTS, nil)
	recorded(t, m, d, nil, dag.SetSecret("npm-token", npmPass), "no-host", false)
	rec := d.posted(t)
	atomsNamed(t, names(rec), "publish:publish")
}

func TestDEF_B6_publish_record_aBuildDirectoryThatCannotBeReadIsTheBuildRowsCouldNotRun(t *testing.T) {
	d := newDoor(t)
	m := publishOn(t, publishSdist)
	engine.failLeaf(buildNeedle, "entries", "the engine lost the directory")
	recorded(t, m, d, dag.SetSecret("pypi-token", publishPass), nil, npmHosted, false)
	rec := d.posted(t)
	if rec.State != buildlane.CouldNotRun || rec.Atoms[0].Atom != "python:artifacts" || !strings.Contains(rec.Atoms[0].Reason, "what uv build wrote could not be read") {
		t.Errorf("%+v", rec)
	}
}
