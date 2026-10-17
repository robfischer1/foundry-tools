package releasekit

import (
	"context"
	"errors"
	"fmt"

	"dagger/foundry-tools/internal/checks"
)

// NotAStarError is StarName's answer when the repository is not a star: an
// ABSENCE to a release atom, where a read fault (the dies unreadable) is a
// could-not-run. errors.As tells them apart.
type NotAStarError struct{ why string }

func notAStar(why string) error        { return &NotAStarError{why: why} }
func (e *NotAStarError) Error() string { return e.why }

// IsNotAStar reports whether err is StarName's "not a star".
func IsNotAStar(err error) bool {
	var ns *NotAStarError
	return errors.As(err, &ns)
}

// StarName is the star this repository is — the key the record and the release
// convention share. The answers file says it first; a repository with none is
// named BY ITS RECORD, the one in foundry-dies whose meta.repo is this clone's
// custody key. hephaestus is the case (CA F17, Rob 2026-09-19): not
// copier-templated, no .copier-answers.yml, and a catalog entry at
// fleet/repos/hephaestus.json saying repo rob/hephaestus — the door already
// routes its lanes by that record, so the release build reads the same name.
// The clone URL is the door's (--repo) or the hook's (--origin); a run given
// neither, and no answers file, is not a star: an error here is "not a star",
// never "could not read".
//
// AN ANSWERS FILE WITH NO service_name FALLS BACK TO THE RECORD TOO. Only the
// go, rust and python templates ask for one; the frontend template asks for
// repo_name, which is not the star (demeter's is demeter-mcp, its catalog entry
// fleet/repos/demeter.json). Measured on calliope #46, 2026-09-19: the push's
// ts:release stood down ABSENT as "not a star image" and the build lane's
// Release() failed on the same sentence — a star with a record, read as none.
//
// origin is the clone URL: the door's, else the hook's.
func StarName(ctx context.Context, t Tree, origin string) (string, error) {
	lacks := "no .copier-answers.yml"
	if answers, present := t.Present(ctx, ".copier-answers.yml"); present {
		if star := checks.ServiceName(answers); star != "" {
			return star, nil
		}
		lacks = "no service_name in .copier-answers.yml"
	}
	key := checks.RepoKey(origin)
	if key == "" {
		return "", notAStar(lacks + " and no clone URL to find a record by, so the repository names no star")
	}
	star, _, err := RecordOf(ctx, t, key)
	if err != nil {
		return "", err
	}
	if star == "" {
		return "", notAStar(fmt.Sprintf("%s, and no record in foundry-dies names repo %s, so the repository names no star", lacks, key))
	}
	return star, nil
}

// RecordOf is the record whose repo is key: its name and its JSON, or "" for
// both when no entry names key. The entries are read at their one home
// (fleet/repos/<name>.json, which every fleet repo has whether or not flux
// deploys it); a tree that cannot be listed is an error, not "no star".
func RecordOf(ctx context.Context, t Tree, key string) (name, body string, err error) {
	paths, err := t.RecordPaths(ctx)
	if err != nil {
		return "", "", fmt.Errorf("the fleet's records could not be listed: %w", err)
	}
	for _, p := range paths {
		slag, err := t.Record(ctx, p)
		if err != nil {
			return "", "", fmt.Errorf("record %s could not be read: %w", p, err)
		}
		if checks.RecordRepo(slag) == key {
			return checks.StarOfRecordPath(p), slag, nil
		}
	}
	return "", "", nil
}

// Origin is the clone URL a run names its repository by: the door's (--repo)
// when it names a custody key, else the hook's (--origin).
func Origin(repo, origin string) string {
	if checks.RepoKey(repo) != "" {
		return repo
	}
	return origin
}
