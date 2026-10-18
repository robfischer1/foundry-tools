// Package fleetmanifestonecommand is the atom fleet:manifest-one-command: every
// command manifest (atoms/**/*.toml) runs exactly one command (R46, R47). It
// asks the same code the manifest loader asks (atomkit.OneCommand), so the
// refusal at run time and this check in CI cannot disagree.
package fleetmanifestonecommand

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It reads manifests and nothing else, so it runs in lead.
var Def = atomkit.Def{
	ID: "fleet:manifest-one-command", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "Every command manifest runs exactly one command: no second step, no shell, no inline script, no heredoc, no chained command.",
	Surface:   "any:atoms/**/*.toml",
	Toolchain: "none",
	Timeout:   60 * time.Second,
	Check:     check,
}

const (
	id     = "fleet:manifest-one-command"
	ending = " — a manifest runs exactly one command (R46)"
)

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// The causes of a finding, one slug per rule of IF-050 not owned by OneCommand.
const (
	causeSecondStep = "second-step"
	causeArgvShape  = "argv-shape"
	causeNotTOML    = "not-toml"
)

// fields is command-atom@1's field set: a top-level key outside it declares a
// second step (rule 1).
var fields = []string{"schema", "id", "stage", "lane", "group", "desc", "surface", "toolchain", "argv", "per_module",
	"judge", "needs", "timeout", "subsumed_by", "not_on_forks", "writes_tree", "args", "default", "preflight", "env"}

// breach is one rule a manifest breaks, before its path and ending are added.
type breach struct{ cause, text string }

// isManifest is the surface atoms/**/*.toml: any depth under atoms/, none included.
func isManifest(p string) bool {
	return strings.HasPrefix(p, "atoms/") && strings.HasSuffix(p, ".toml")
}

// check walks every manifest in the tree's population, collecting the breaches
// of each in rule order.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	var paths []string
	for _, f := range in.Files {
		if isManifest(f) {
			paths = append(paths, f)
		}
	}
	if len(paths) == 0 {
		return absent()
	}
	var logs []string
	var findings []checks.Finding
	for _, p := range paths {
		raw, err := os.ReadFile(filepath.Join(in.Root, p))
		if err != nil {
			return unreadable(p, err)
		}
		breaches := breachesOf(raw)
		if len(breaches) == 0 {
			logs = append(logs, p+": one command")
			continue
		}
		for _, b := range breaches {
			line := p + ": " + b.text + ending
			logs = append(logs, line)
			findings = append(findings, checks.Finding{Verdict: "violated", Subject: p, Cause: b.cause, Detail: line, Probe: id})
		}
	}
	v := checks.Verdict{Atom: id, Stage: row.Stage, State: int(checks.StatePass), Result: checks.StatePass.String(), Reason: id + ": PASS"}
	if len(findings) > 0 {
		v.State, v.Result, v.Reason, v.Findings = int(checks.StateFindings), checks.StateFindings.String(), id+": FINDINGS (exit 1)", findings
	}
	v.Logs, v.Truncated, v.OriginalBytes = checks.CaptureLogs(strings.Join(logs, "\n"))
	return v
}

// breachesOf is rules 1 to 7 over one manifest's bytes.
func breachesOf(raw []byte) []breach {
	var doc map[string]any
	if _, err := toml.Decode(string(raw), &doc); err != nil {
		return []breach{{causeNotTOML, "not TOML: " + err.Error()}}
	}
	var out []breach
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if !slices.Contains(fields, k) {
			out = append(out, breach{causeSecondStep, "key " + k + " declares a second step"})
		}
	}
	argv, ok := stringList(doc["argv"])
	if !ok {
		out = append(out, breach{causeArgvShape, "argv must be one non-empty list of strings"})
		argv = nil
	}
	argv = append(argv, defaultArgs(doc["default"])...)
	for _, b := range atomkit.OneCommandBreaches(argv, envOf(doc["env"])) {
		out = append(out, breach{b.Cause, b.Text})
	}
	return out
}

// stringList is v as a non-empty list of strings.
func stringList(v any) ([]string, bool) {
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return nil, false
	}
	out := make([]string, len(list))
	for i, e := range list {
		s, ok := e.(string)
		if !ok {
			return nil, false
		}
		out[i] = s
	}
	return out, true
}

// defaultArgs is the arguments of every [[default]] table, in order: what the
// manifest can add to its argv on its own, which the script rules read too.
func defaultArgs(v any) []string {
	var out []string
	tables, _ := v.([]map[string]any)
	for _, t := range tables {
		args, _ := stringList(t["args"])
		out = append(out, args...)
	}
	return out
}

// envOf is the manifest's env table, its string values only: a value of any
// other type is the loader's to refuse, not a script.
func envOf(v any) map[string]string {
	table, _ := v.(map[string]any)
	out := map[string]string{}
	for k, e := range table {
		if s, ok := e.(string); ok {
			out[k] = s
		}
	}
	return out
}

// absent is the answer for a tree with no manifest: the surface stands the atom
// down, and this says so for a runner that asks anyway.
func absent() checks.Verdict {
	return checks.Verdict{
		Atom: id, Stage: row.Stage, State: int(checks.StatePass), Result: "absent",
		Reason: id + ": ABSENT - this tree carries no atoms/**/*.toml, so it declares no command atom. Nothing was checked and nothing needed to be.",
		Logs:   []string{},
	}
}

// unreadable is a manifest the atom could not read: it did not judge it.
func unreadable(p string, err error) checks.Verdict {
	return checks.Verdict{
		Atom: id, Stage: row.Stage, State: int(checks.StateCannotRun), Result: checks.StateCannotRun.String(),
		Reason: fmt.Sprintf("%s: unreadable: %v", p, err), Logs: []string{},
	}
}
