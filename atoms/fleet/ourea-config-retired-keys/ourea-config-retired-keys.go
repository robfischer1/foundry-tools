// Package fleetoureaconfigretiredkeys is the atom fleet:ourea-config-retired-keys: the ourea
// ConfigMap names no key the door has stopped reading. One file holds the atom, and both runners execute it (lld.md FN-085).
package fleetoureaconfigretiredkeys

import (
	"context"
	"fmt"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It runs in lead: the tree it reads is the pod's, and
// nothing else needs a toolchain.
var Def = atomkit.Def{
	ID: "fleet:ourea-config-retired-keys", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "The ourea ConfigMap names no key the door has stopped reading.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "fleet:ourea-config-retired-keys"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// oureaRetired reads the door's published list of keys ourea stopped reading,
// from ourea's main through the git door, where every other atom here reads the
// fleet from. An answer that is not a 200 is an error: the list is the check.
func oureaRetired(ctx context.Context, in atomkit.In) (map[string]string, error) {
	status, body, err := in.DoorOf().Get(ctx, checks.OureaDoorRepo, checks.OureaRetiredKeysFile)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("the door answered HTTP %d", status)
	}
	return checks.OureaRetiredList(string(body))
}

// oureaConfigRetiredKeys: the ourea ConfigMap names no key the door has stopped
// reading. A prime/ with no ConfigMap in it is NOT absence — prime/ is the
// fleet's flux tree and nothing else's, so the ConfigMap moved.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	entries, err := t.Entries(".")
	if err != nil {
		return checks.CannotEnumerate(row, err)
	}
	if !checks.HasEntry(entries, checks.OureaConfigDir) {
		return checks.VerdictOf(row, int(checks.StatePass), id+
			": ABSENT - this tree carries no "+checks.OureaConfigDir+"/, so it is not the fleet's flux tree and holds no ourea ConfigMap")
	}
	prime, err := t.Entries(checks.OureaConfigDir)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - %s/ would not enumerate: %v", id, checks.OureaConfigDir, err))
	}
	if !checks.HasEntry(prime, checks.OureaConfigFile) {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+
			": CANNOT RUN - this is the fleet's flux tree ("+checks.OureaConfigDir+"/ is here) and "+checks.OureaConfigPath+
			" is not in it: the door's ConfigMap moved, and this atom grades nothing until oureaConfigPath says where")
	}
	body, err := t.Read(checks.OureaConfigPath)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - %s would not read: %v", id, checks.OureaConfigPath, err))
	}
	cfg, err := checks.OureaConfigKeys(body)
	if err != nil {
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - %s would not parse: %v", id, checks.OureaConfigPath, err))
	}
	retired, err := oureaRetired(ctx, in)
	if err != nil {
		// THE LIST IS THE CHECK: a pass over a ConfigMap graded against
		// nothing is the zero-file scan this module exists to delete.
		return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - ourea's %s would not read, so nothing says which keys are retired: %v",
			id, checks.OureaRetiredKeysFile, err))
	}
	named := checks.OureaRetiredNamed(cfg, retired)
	if len(named) == 0 {
		return checks.VerdictOf(row, int(checks.StatePass), id+
			fmt.Sprintf(": %s names none of ourea's %d retired keys", checks.OureaConfigKey, len(retired)))
	}
	return checks.VerdictOf(row, int(checks.StateFindings), checks.OureaRetiredReport(named, retired))
}
