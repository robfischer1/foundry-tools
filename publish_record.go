package main

import (
	"context"
	"strings"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/dagger"
)

// THE PUBLISH LANE'S RECORD, AS PHASES — cast's shape (cast_record.go), for the
// third procedural lane.
//
// WHY IT POSTS AT ALL. The executor's publish lane posts a run record, and the
// lists that say which lanes owe one (daedalus RECORD_LANES, the door's
// record_lanes, ops:record-lanes) are per lane, not per repo: once `publish` is
// on them, every publish run owes a record, Dagger's included, and one that
// posts none settles no_record/could-not-run. Erebus: 300 publish settles in a
// week, none with atom rows.
//
// THE ROWS ARE THE EXECUTOR'S, by name, where the step is the same step:
//
//	python:artifacts  (group language)    uv build and the artifacts it wrote
//	ts:pack           (group language)    the package selection, the install and the builds
//	publish:publish   (group capability)  the index/registry probes and the uploads
//
// WHERE THEY CANNOT MATCH. The executor builds every package first and uploads
// after; Dagger builds each npm package only when the registry lacks it, right
// before its upload, so a ts:pack failure here is a build that failed in the
// middle of publishing, and publish:publish still has a row for the other
// members. Dagger's rows carry the lane's reasons, not the executor's atoms'
// logs (publishSay prints to the pod log only), and each row's span is the time
// since the row before it. The record explains the verdict; it does not decide
// it (settle() is unchanged).

// publishGroup is the record's lane group, until the tree says python or ts.
const publishGroup = "publish"

// The two groups the executor files its publish rows under.
const (
	publishBuildGroup  = "language"
	publishUploadGroup = "capability"
)

// publishPhases is the lane's rows in the order it takes them for a tree of the
// given kind ("python", "ts"; "" before the tree has said).
func publishPhases(kind string) []string {
	if kind == "" {
		return []string{"publish:publish"}
	}
	return []string{artifactAtom(kind), "publish:publish"}
}

// artifactAtom is the executor atom whose step builds what the lane uploads.
func artifactAtom(kind string) string {
	if kind == "ts" {
		return "ts:pack"
	}
	return "python:artifacts"
}

// begin says which half the tree took, so the record names its build row.
func (l *publishLane) begin(kind string) {
	l.kind = kind
	l.order = publishPhases(kind)
}

// sealArtifacts files the build row.
func (l *publishLane) sealArtifacts(code int, reason string) {
	l.packSealed = true
	l.sealIn(publishBuildGroup, artifactAtom(l.kind), code, reason)
}

// stopArtifacts files the build row as the step that ended the run, so the upload
// reads as never reached, and hands the verdict back for the call site to return.
func (l *publishLane) stopArtifacts(code int, reason string) (int, string) {
	l.halted = true
	l.sealArtifacts(code, reason)
	return code, reason
}

// packFailed notes a package whose build failed in the middle of its publish
// (inBuild); an upload that failed is publish:publish's.
func (l *publishLane) packFailed(inBuild bool, code int, reason string) {
	if !inBuild {
		return
	}
	l.packFailures = append(l.packFailures, reason)
	l.packFindings = l.packFindings || code == buildlane.Findings
}

// post files the rows the run did not file as it went and posts the record, when
// the call declared a token. Without one nothing is built and nothing is printed:
// the lane is what it was.
func (l *publishLane) post(ctx context.Context, token *dagger.Secret, code int, reason string) {
	if token == nil {
		return
	}
	if !l.halted {
		if l.kind == "ts" && !l.packSealed {
			l.sealPack()
		}
		l.sealIn(publishUploadGroup, "publish:publish", code, reason)
	}
	rec := l.record("publish", code)
	rec.Lanes = []string{publishGroup}
	if l.kind != "" {
		rec.Lanes = []string{l.kind}
	}
	// THE MARSHAL ERROR IS DROPPED, as cast's is: StageResult is strings, ints and
	// slices, so json.Marshal cannot fail on it.
	body, _ := rec.Record()
	postRecord(ctx, l.m.Repo, token, body)
}

// sealPack files ts:pack for a run whose builds all held, or whose builds failed
// in the middle of publishing.
func (l *publishLane) sealPack() {
	if len(l.packFailures) == 0 {
		l.sealArtifacts(buildlane.Clean, "every package the registry lacked built")
		return
	}
	code := buildlane.CouldNotRun
	if l.packFindings {
		code = buildlane.Findings
	}
	l.sealArtifacts(code, strings.Join(l.packFailures, "; "))
}
