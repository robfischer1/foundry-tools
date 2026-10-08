// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"

	"dagger/foundry-tools/internal/checks"
)

func init() {
	register("fleet:ourea-config-retired-keys", fleetOureaConfigRetiredKeys)
}

const (
	// oureaConfigPath is the ConfigMap that overrides ourea's defaults in the
	// cluster. It is also the only file in the fleet this atom has an opinion
	// about.
	//
	// IT MOVED ONCE AND THIS ATOM KEPT PASSING. The flux tree was split by
	// namespace on 2026-10-01 and the ConfigMap went from infrastructure/ to
	// prime/; infrastructure/ stayed, without it, so "no such file in that
	// directory" read as ABSENT and every flux pull since was graded by
	// nothing — including the one that added and the one that removed
	// cast_on_build. The directory is now prime/, which only the fleet's flux
	// tree has, and a prime/ without the ConfigMap is no longer absence.
	oureaConfigPath = checks.OureaConfigPath

	// oureaConfigKey is the ConfigMap data key holding the door's TOML.
	oureaConfigKey = checks.OureaConfigKey

	// OureaRepo / OureaRef / OureaRetiredKeys locate the list this atom grades
	// against. It is generated from ourea's own `retiredKeys` map and pinned to
	// it by a test in that repository (internal/config's
	// TestTheRetiredKeysArtifactIsTheMap), so the list cannot drift from the
	// code that stopped reading the keys.
	//
	// A MOVING REF, for the reason DiesRepo gives at length: this grades a flux
	// pull against what the door CURRENTLY ignores, and a key retired after a
	// pinned sha would be graded by nothing — the silent skip again, wearing a
	// version number.
	OureaRepo        = "https://git.notusmi.com/rob/ourea.git"
	OureaRef         = checks.OureaRef
	OureaRetiredKeys = checks.OureaRetiredKeysFile
	oureaConfigDir   = checks.OureaConfigDir
	oureaConfigInDir = checks.OureaConfigFile
)

// The ourea ConfigMap names no key the door has stopped reading.
//
// WHAT THIS REPLACES, because the obvious fix was considered and refused.
// ourea's own boot warns about a retired key and carries on; the comment above
// its map used to say "flip this to a refusal once the ConfigMap is clean", and
// the ConfigMap IS clean. The flip was reachable and it was still wrong.
// MEASURED across every retirement so far, the CODE lands ahead of the
// ConfigMap edit:
//
//	record keys (3)    ourea c262dd31 -> infra 4479d295a   15 minutes
//	gate_job_image     ourea dda71d36 -> infra cdc395cfb   ~3 days
//	recipe_job_script  ourea 11beede8 -> infra cdc395cfb   ~12 days
//
// ourea is the fleet's git remote as well as its CI runner, so a boot refusal
// makes each of those windows a constellation-wide outage waiting on any ourea
// image roll — and that door rolls constantly. Cleaning the ConfigMap would
// make the flip safe TODAY and not durably: the hazard returns on the next
// retirement, permanently. Rob's call, 2026-09-29: buy the same guarantee at PR
// time, where the fix is a deleted line and nothing is down.
//
// THE WARNING STAYS, and that is not a compromise. cdc395cfb's own subject is
// "two keys the door already told us it stopped reading" — the boot warning is
// what DROVE that cleanup. This atom is a second reader of the same fact, not a
// replacement for the first.
//
// EXIT 1, NOT 2. A ConfigMap naming a retired key is a finding about the tree:
// the line is there, it is inert, and deleting it is the whole fix. Nothing
// could not be checked.
func fleetOureaConfigRetiredKeys(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("fleet:ourea-config-retired-keys")

	entries, err := r.src.Entries(ctx)
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(entries, oureaConfigDir) {
		return checks.VerdictOf(a, 0, string(a.ID)+
			": ABSENT - this tree carries no "+oureaConfigDir+"/, so it is not the fleet's flux tree and holds no ourea ConfigMap")
	}
	infra, err := r.src.Directory(oureaConfigDir).Entries(ctx)
	if err != nil {
		// The directory is there and would not list. Guessing the ConfigMap is
		// absent is how a check reports a pass over a file it never opened.
		return checks.VerdictOf(a, 2, fmt.Sprintf(
			"%s: CANNOT RUN - %s/ would not enumerate: %v", a.ID, oureaConfigDir, err))
	}
	if !checks.HasEntry(infra, oureaConfigInDir) {
		// NOT ABSENCE. prime/ is the fleet's flux tree and nothing else's, so
		// a prime/ with no ConfigMap means the ConfigMap moved — and an atom
		// that answers "nothing to check" then is the silent skip that let it
		// grade nothing for a day.
		return checks.VerdictOf(a, 2, string(a.ID)+
			": CANNOT RUN - this is the fleet's flux tree ("+oureaConfigDir+"/ is here) and "+oureaConfigPath+
			" is not in it: the door's ConfigMap moved, and this atom grades nothing until oureaConfigPath says where")
	}

	body, err := r.src.File(oureaConfigPath).Contents(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf(
			"%s: CANNOT RUN - %s would not read: %v", a.ID, oureaConfigPath, err))
	}
	cfg, err := oureaConfigTOML(body)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf(
			"%s: CANNOT RUN - %s would not parse: %v", a.ID, oureaConfigPath, err))
	}

	retired, err := oureaRetiredKeys(ctx)
	if err != nil {
		// THE LIST IS THE CHECK. Without it this atom knows nothing, and a
		// verdict 0 here would be a pass over an ungraded ConfigMap — the
		// zero-file scan this module exists to delete.
		return checks.VerdictOf(a, 2, fmt.Sprintf(
			"%s: CANNOT RUN - ourea's %s would not read, so nothing says which keys are retired: %v",
			a.ID, OureaRetiredKeys, err))
	}

	named := checks.OureaRetiredNamed(cfg, retired)
	if len(named) == 0 {
		return checks.VerdictOf(a, 0, string(a.ID)+
			fmt.Sprintf(": %s names none of ourea's %d retired keys", oureaConfigKey, len(retired)))
	}
	return checks.VerdictOf(a, 1, checks.OureaRetiredReport(named, retired))
}

// oureaRetiredKeys reads the door's published list of keys it stopped reading.
//
// An error is returned rather than swallowed: every caller of this must refuse,
// because a check that cannot learn its own criteria has not checked anything.
//
// `%v` AND NOT `%w`, HERE AND IN oureaConfigTOML, and it is a decision rather
// than a slip. The mutation gate made the argument: three ERRORF_WRAP mutants
// LIVED across these two functions, and no test could have killed them, because
// `%w` and `%v` produce the identical string and nothing unwraps these errors.
// Both functions are file-local, both callers format the result into a verdict
// with `%v`, and a verdict is text. `%w` here is a promise that a caller may
// match the cause — a capability this package does not have and cannot
// demonstrate. The fleet uses `%w` where it is load-bearing (atoms_go.go:549
// runs errors.As over a sentinel type); this is not one of those places.
func oureaRetiredKeys(ctx context.Context) (map[string]string, error) {
	body, err := dag.Git(OureaRepo).Ref(OureaRef).Tree().File(OureaRetiredKeys).Contents(ctx)
	if err != nil {
		return nil, err
	}
	return checks.OureaRetiredList(body)
}

// oureaConfigTOML pulls the door's TOML out of the ConfigMap and decodes it to
// the set of keys it sets.
//
// IT DECODES RATHER THAN GREPS, and that is worth the two dependencies it costs
// (both already vendored here). MEASURED on flux's live ConfigMap, 2026-09-30:
// it SETS 28 top-level keys and NONE of them is retired — and 7 of the 11
// retired names appear in its text anyway, every one inside a comment
// explaining why that key went. A text scan would report seven findings against
// a clean file, including against the line that says the key is gone.
//
// One of those comments is itself wrong (`# gate_job_image STAYS: an empty one
// switches the whole runner off` — gate_job_image is on the retired list), so
// the prose around a key cannot be trusted to agree with the key either.
// Deciding from the decoded value is the only reading that does not inherit
// whatever the last author believed.
//
// The value is decoded into a nested map so a key inside a table keeps its
// table's name, and only the top level is compared: ourea's retired keys are
// all top-level scalars, and a `pin_hook_url` under some future `[hooks]` table
// is a different key that this atom must not claim to have found.
func oureaConfigTOML(configMap string) (map[string]any, error) {
	return checks.OureaConfigKeys(configMap)
}
