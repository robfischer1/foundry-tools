// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

func init() {
	register("fleet:renovate-config", fleetRenovateConfig)
}

// renovateConfigFile is the self-hosted Renovate global config's name.
const renovateConfigFile = "config.js"

// A root config.js that is Renovate's global config passes the validator.
//
// THE VALIDATOR IS THE DEPLOYED RENOVATE'S, not whatever npm or PATH holds: it
// runs from checks.ImageRenovate, the ref flux's CronJob runs (the image is node
// and renovate in one digest, so nothing is installed or fetched here). The
// version is probed and held to checks.RenovateTag, so a tag that moved
// under the digest cannot grade with another Renovate's option table.
//
// THE SURFACE IS DECIDED IN GO from the file: a root config.js that exports a
// config naming a `platform` is Renovate's; any other config.js (a bundler's,
// a framework's) is not this atom's subject and reads ABSENT, not red.
//
// THE EXIT CODE IS THE VERDICT: 0 valid, 1 invalid (--strict makes a
// deprecated or migrated option an error as well).
func fleetRenovateConfig(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("fleet:renovate-config")

	roots, err := r.src.Entries(ctx)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(roots, renovateConfigFile) {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - this tree carries no root "+renovateConfigFile+", so it holds no Renovate global config")
	}
	body, err := r.src.File(renovateConfigFile).Contents(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - %s could not be read: %v", a.ID, renovateConfigFile, err))
	}
	if !strings.Contains(body, "module.exports") || !strings.Contains(body, "platform") {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - the root "+renovateConfigFile+" is not a Renovate global config (no module.exports naming a platform)")
	}

	base := r.lane(checks.ImageRenovate)
	out, code, err := output(ctx, base.WithExec([]string{"renovate", "--version"}, anyExit))
	if err != nil {
		return neverRan(a, err)
	}
	if code != 0 || out != checks.RenovateTag {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the image answers renovate %q (exit %d), not the deployed %s, so its validator is not the deployed one", a.ID, out, code, checks.RenovateTag))
	}
	return verdict(ctx, a, base.WithExec([]string{"renovate-config-validator", "--strict", renovateConfigFile}, anyExit))
}
