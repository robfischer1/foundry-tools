// Package fleetrenovateconfig is the atom fleet:renovate-config: a root config.js that is Renovate's
// global config passes the validator. One file holds the atom, and both runners execute it
// (lld.md FN-085).
package fleetrenovateconfig

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. The validator is the deployed Renovate's own, so it runs in
// the Renovate image (the toolchain), where nothing is installed or fetched.
var Def = atomkit.Def{
	ID: "fleet:renovate-config", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "A root config.js that is a Renovate global config passes renovate-config-validator --strict, run from the Renovate image the fleet deploys.",
	Surface:   "root:config.js",
	Toolchain: "renovate",
	Check:     check,
}

const id = "fleet:renovate-config"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// configFile is the self-hosted Renovate global config's name.
const configFile = "config.js"

// check validates a root config.js that is Renovate's global config.
//
// THE VALIDATOR IS THE DEPLOYED RENOVATE'S, not whatever npm or PATH holds: it
// runs from checks.ImageRenovate, the ref flux's CronJob runs (the image is node
// and renovate in one digest, so nothing is installed or fetched here). The
// version is probed and held to checks.RenovateTag, so a tag that moved under
// the digest cannot grade with another Renovate's option table.
//
// THE SURFACE IS DECIDED IN GO from the file: a root config.js that exports a
// config naming a `platform` is Renovate's; any other config.js (a bundler's,
// a framework's) is not this atom's subject and reads ABSENT, not red.
//
// THE EXIT CODE IS THE VERDICT: 0 valid, 1 invalid (--strict makes a
// deprecated or migrated option an error as well).
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	roots, err := t.Entries(".")
	if err != nil {
		return checks.CannotEnumerate(row, err)
	}
	if !checks.HasEntry(roots, configFile) {
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - this tree carries no root "+configFile+", so it holds no Renovate global config")
	}
	body, err := t.Read(configFile)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %s could not be read: %v", id, configFile, err))
	}
	if !strings.Contains(body, "module.exports") || !strings.Contains(body, "platform") {
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - the root "+configFile+" is not a Renovate global config (no module.exports naming a platform)")
	}
	out, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "renovate", Args: []string{"--version"}})
	if code < 0 {
		return atoms.NeverRan(row, nil, code, out)
	}
	if code != 0 || out != checks.RenovateTag {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the image answers renovate %q (exit %d), not the deployed %s, so its validator is not the deployed one", id, out, code, checks.RenovateTag))
	}
	out, code = in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "renovate-config-validator", Args: []string{"--strict", configFile}, Both: true})
	if code < 0 {
		return atoms.NeverRan(row, nil, code, out)
	}
	return checks.VerdictOf(row, code, out)
}
