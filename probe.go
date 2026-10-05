package main

import (
	"context"

	"dagger/foundry-tools/internal/dagger"
)

// AN ABSENT FILE IS AN ANSWER, NOT AN ERROR. Asking the engine for
// Directory.file on a path that is not a regular file fails that span. The
// atoms read the failure as "absent" and carry on, but the engine has already
// logged the span as ERROR, and the lane transcript and Loki count it as one
// ("Directory.file ERROR ! path .git is a directory, not a file", "! stat
// .copier-answers.yml: no such file or directory"). Measured 2026-10-05 over
// 60 lane pods: every gate, orbit, build and mutation run carried at least one
// such line, ~170 an hour at detected_level=error, and none of them was a
// failure.
//
// So the question is asked the way it is meant: Exists with the type it needs,
// which answers false for an absent path or a directory and opens no failing
// span. The file is read only when it is there. present=false with a nil error
// is "not a regular file at that path". A non-nil error is the engine's own
// failure, and the caller treats it the same way it treated a failed read
// before.

// fileIfPresent reads path off dir when it is a regular file there.
func fileIfPresent(ctx context.Context, dir *dagger.Directory, path string) (body string, present bool, err error) {
	ok, err := dir.Exists(ctx, path, dagger.DirectoryExistsOpts{ExpectedType: dagger.ExistsTypeRegularType})
	if err != nil || !ok {
		return "", false, err
	}
	body, err = dir.File(path).Contents(ctx)
	return body, err == nil, err
}

// ctrFileIfPresent is fileIfPresent for a path inside a container's filesystem.
func ctrFileIfPresent(ctx context.Context, ctr *dagger.Container, path string) (body string, present bool, err error) {
	ok, err := ctr.Exists(ctx, path, dagger.ContainerExistsOpts{ExpectedType: dagger.ExistsTypeRegularType})
	if err != nil || !ok {
		return "", false, err
	}
	body, err = ctr.File(path).Contents(ctx)
	return body, err == nil, err
}
