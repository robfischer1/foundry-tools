package checks

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// fleet:renovate-lockstep's judgement (Later #119).
//
// THE PIN LIVES IN TWO REPOSITORIES. foundry/flux deploys Renovate (the
// renovate/renovate.yaml CronJob, two containers on one ref) and this repo
// pins the same image as ImageRenovate, the validator fleet:renovate-config
// runs config.js through. A validator older than the deployed image rejects
// options the image accepts, so ImageRenovate must be what flux runs.
//
// A RENOVATE RULE CANNOT DO THIS. packageRules group inside one repository's
// run and Renovate opens one pull per repository, so "bump both in one update"
// has nowhere to be expressed across flux and this repo (the same limit
// renovate-config's dagger note records). Nothing else moved the pin either:
// ImageRenovate is a bare ref, not a `<Name>Version` marker. So this is the
// guard, and it is the dagger-lockstep shape: flux leads, this repo follows.
//
//   - in a tree that holds flux's renovate.yaml, every Renovate ref in it must
//     be one ref (tag AND digest) - a pull that moves one container alone is a
//     finding;
//   - in a tree that holds ImageRenovate, it must EQUAL the ref flux's main
//     runs, read from the door at gate time. Ahead of flux is as red as behind:
//     land flux first, then re-pin here.
//
// Tests never dial anything: the judgement takes the file bodies and a Door,
// and the tests hand it an httptest server.

// RenovateFluxRepo and RenovateFluxSource are where the deployed Renovate is
// pinned.
const (
	RenovateFluxRepo   = "foundry/flux"
	RenovateFluxSource = "renovate/renovate.yaml"
	// RenovateImagesSource is the file that declares ImageRenovate.
	RenovateImagesSource = "internal/checks/images.go"
)

// RenovateLockstepFiles is every file the lockstep reads.
var RenovateLockstepFiles = []string{RenovateFluxSource, RenovateImagesSource}

const renovateRefPattern = `ghcr\.io/renovatebot/renovate:[^\s"'@]+@sha256:[0-9a-f]{64}`

var (
	renovateRefRe   = regexp.MustCompile(renovateRefPattern)
	renovateConstRe = regexp.MustCompile(`(?m)^\s*ImageRenovate\s*=\s*"(` + renovateRefPattern + `)"`)
)

// RenovateLockstep judges files (path -> contents, only the paths the tree
// holds), reading flux's main through door when the tree pins the image but
// does not hold flux's source.
func RenovateLockstep(ctx context.Context, files map[string]string, door Door) (int, string) {
	const id = "fleet:renovate-lockstep"
	var findings []string

	fluxBody, hasFlux := files[RenovateFluxSource]
	var fluxRefs []string
	if hasFlux {
		fluxRefs = renovateRefRe.FindAllString(fluxBody, -1)
	}

	var pinned string
	if body, ok := files[RenovateImagesSource]; ok {
		if m := renovateConstRe.FindStringSubmatch(body); m != nil {
			pinned = m[1]
		}
	}

	if !hasFlux && pinned == "" {
		return 0, id + ": ABSENT - this tree neither deploys nor pins the Renovate image, so there is nothing to hold in lockstep"
	}

	agree := 0
	runs, from := "", ""
	if hasFlux {
		if len(fluxRefs) == 0 {
			findings = append(findings, fmt.Sprintf("%s: names no ghcr.io/renovatebot/renovate:<tag>@<digest> this atom can read, so the lockstep compared nothing there. The pin's spelling moved; move checks.renovateRefPattern with it.", RenovateFluxSource))
		} else {
			runs, from = fluxRefs[0], RenovateFluxSource+" (this tree)"
			for _, r := range fluxRefs {
				if r != runs {
					findings = append(findings, fmt.Sprintf("%s: Renovate runs %s in one container and %s in another. One CronJob, one ref - they move in one pull.", RenovateFluxSource, runs, r))
				} else {
					agree++
				}
			}
		}
	}

	if pinned != "" {
		if !hasFlux {
			at := door.URL(RenovateFluxRepo, RenovateFluxSource)
			status, body, err := door.Get(ctx, RenovateFluxRepo, RenovateFluxSource)
			switch {
			case err != nil:
				return 2, fmt.Sprintf("%s: CANNOT RUN - the door is unreachable (%s): %v. A Renovate ref that could not be read is not one that agrees.", id, at, err)
			case status != http.StatusOK:
				return 2, fmt.Sprintf("%s: CANNOT RUN - the door answered HTTP %d for %s. A Renovate ref that could not be read is not one that agrees.", id, status, at)
			}
			ref := renovateRefRe.FindString(string(body))
			if ref == "" {
				return 2, fmt.Sprintf("%s: CANNOT RUN - %s answered no ghcr.io/renovatebot/renovate:<tag>@<digest> pin, so the deployed Renovate is unknown.", id, at)
			}
			runs, from = ref, RenovateFluxRepo+" main "+RenovateFluxSource
		}
		if runs != "" {
			if pinned != runs {
				findings = append(findings, fmt.Sprintf("%s: ImageRenovate %s, but Renovate runs %s (%s). The validator follows the deployed image: land flux first, then re-pin ImageRenovate and RenovateTag here.", RenovateImagesSource, pinned, runs, from))
			} else {
				agree++
			}
		}
	}

	if len(findings) > 0 {
		sort.Strings(findings)
		return 1, strings.Join(append([]string{fmt.Sprintf("%s: %d pin(s) out of lockstep", id, len(findings))}, findings...), "\n")
	}
	return 0, fmt.Sprintf("%s: %d pin(s) agree with the Renovate %s (%s)", id, agree, runs, from)
}
