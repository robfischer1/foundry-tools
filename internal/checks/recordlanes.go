package checks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"go.yaml.in/yaml/v3"
)

// ops:record-lanes' judgement (system-tests DEF-4-002, unit-tests UT-680).
//
// THE RULE. Flux names the lanes that owe a run record twice: Daedalus reads
// RECORD_LANES (prime/daedalus-jobs.yaml, a JSON list inside a string value) and
// the door reads record_lanes (the TOML inside prime/ourea-config.yaml). The two
// must be the same SET over the lanes the executor runs (a retired lane may sit
// in one only), and each must hold every lane the in-pod executor runs
// (EXECUTOR_LANES, same ConfigMap, absent means none) plus "atoms" and "dev".
// A lane that owes a record in one reader and not the other settles
// differently depending on who is asked, and a lane missing from both yields
// no record and settles could-not-run (cause=no_record).
//
// A LIST NOT READ IS A LIST NOT JUDGED. A file that is present but does not
// parse, or no longer carries its key, is could-not-run: the manifests moved,
// and a pass would say the lanes agree when nobody looked.

// The files and keys this atom reads.
const (
	RecordLanesJobsFile  = "prime/daedalus-jobs.yaml"
	RecordLanesOureaFile = "prime/ourea-config.yaml"
	// RecordLanesJobsGreenFile is the green colour's copy of the jobs ConfigMap
	// (BG1). Judged when present, by the rule the blue file is.
	RecordLanesJobsGreenFile = "prime/daedalus-jobs-green.yaml"

	recordLanesKey   = "RECORD_LANES"
	executorLanesKey = "EXECUTOR_LANES"
	oureaTOMLKey     = "config.toml"
)

// recordLanesAlways are the lanes every record-lane list must hold whatever the
// executor runs.
var recordLanesAlways = []string{"atoms", "dev"}

// IsRecordLanesFile reports whether path is one of the two files this atom reads.
func IsRecordLanesFile(path string) bool {
	return path == RecordLanesJobsFile || path == RecordLanesOureaFile || path == RecordLanesJobsGreenFile
}

// RecordLanes judges files (path -> contents of the two flux files). State 0
// when the lists agree and are complete, 1 naming each missing lane and its
// file, 2 when a file is absent or unreadable.
func RecordLanes(files map[string]string) (int, string) {
	state, report := recordLanesOf(RecordLanesJobsFile, files)
	green, ok := files[RecordLanesJobsGreenFile]
	if !ok {
		return state, report
	}
	gs, gr := recordLanesOf(RecordLanesJobsGreenFile, map[string]string{RecordLanesJobsGreenFile: green, RecordLanesOureaFile: files[RecordLanesOureaFile]})
	switch {
	case gs == int(StatePass):
		return state, report
	case state == int(StatePass):
		return gs, gr
	}
	return max(state, gs), report + "\n" + gr
}

// recordLanesOf judges one jobs ConfigMap (blue or green, jobsFile names which)
// against the door's list.
func recordLanesOf(jobsFile string, files map[string]string) (int, string) {
	const id = "ops:record-lanes"
	cannot := func(format string, a ...any) (int, string) {
		return int(StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %s", id, fmt.Sprintf(format, a...))
	}
	jobsBody, ok := files[jobsFile]
	if !ok {
		return cannot("%s is not here (the other is): the flux manifests moved, and this atom grades nothing until it knows where", jobsFile)
	}
	oureaBody, ok := files[RecordLanesOureaFile]
	if !ok {
		return cannot("%s is not here (the other is): the flux manifests moved, and this atom grades nothing until it knows where", RecordLanesOureaFile)
	}
	data, err := configMapData(jobsBody)
	if err != nil {
		return cannot("%s would not parse: %v", jobsFile, err)
	}
	jobsRecord, err := jsonLanes(data, recordLanesKey, true)
	if err != nil {
		return cannot("%s: %v", jobsFile, err)
	}
	executor, err := jsonLanes(data, executorLanesKey, false)
	if err != nil {
		return cannot("%s: %v", jobsFile, err)
	}
	oureaData, err := configMapData(oureaBody)
	if err != nil {
		return cannot("%s would not parse: %v", RecordLanesOureaFile, err)
	}
	tomlText, ok := oureaData[oureaTOMLKey]
	if !ok {
		return cannot("%s carries no %s", RecordLanesOureaFile, oureaTOMLKey)
	}
	var cfg struct {
		RecordLanes *[]string `toml:"record_lanes"`
	}
	if _, err := toml.Decode(tomlText, &cfg); err != nil {
		return cannot("%s's %s would not parse: %v", RecordLanesOureaFile, oureaTOMLKey, err)
	}
	if cfg.RecordLanes == nil {
		return cannot("%s's %s carries no record_lanes", RecordLanesOureaFile, oureaTOMLKey)
	}
	oureaRecord := *cfg.RecordLanes

	executorNames, malformed, unbacked := executorLaneNames(executor)
	owed := sortedSet(append(append([]string{}, executorNames...), recordLanesAlways...))
	var out []string
	for _, e := range malformed {
		out = append(out, fmt.Sprintf("  %s entry %q is malformed (want lane, lane@repo or !lane@repo, the repo with at most one '/')", executorLanesKey, e))
	}
	for _, e := range unbacked {
		out = append(out, fmt.Sprintf("  %s exclusion %q has no bare lane beside it (it would mean nothing)", executorLanesKey, e))
	}
	for _, l := range owed {
		if !slices.Contains(jobsRecord, l) {
			out = append(out, fmt.Sprintf("  %s: missing from %s in %s", l, recordLanesKey, jobsFile))
		}
	}
	for _, l := range owed {
		if !slices.Contains(oureaRecord, l) {
			out = append(out, fmt.Sprintf("  %s: missing from record_lanes in %s", l, RecordLanesOureaFile))
		}
	}
	// A lane the executor does not run is retired or dead: it may sit in one list
	// until the other is edited (the serving colour's map cannot be touched alone).
	live := map[string]bool{}
	for _, l := range owed {
		live[l] = true
	}
	if only := symmetricDiff(onlyLive(jobsRecord, live), onlyLive(oureaRecord, live)); len(only) > 0 {
		out = append(out, fmt.Sprintf("  the two lists are not equal as sets: %s", strings.Join(only, ", ")))
	}
	if len(out) == 0 {
		return int(StatePass), fmt.Sprintf("%s: %s and record_lanes agree (%s) and hold %s", id, recordLanesKey, strings.Join(sortedSet(jobsRecord), ","), strings.Join(owed, ","))
	}
	return int(StateFindings), fmt.Sprintf("%s: the record-lane lists drift or lack a lane the executor needs (Daedalus and the door must owe the same records):\n%s", id, strings.Join(out, "\n"))
}

// configMapData is the data map of the first document that has one.
func configMapData(body string) (map[string]string, error) {
	dec := yaml.NewDecoder(strings.NewReader(body))
	for {
		var doc struct {
			Data map[string]string `yaml:"data"`
		}
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return nil, errors.New("no document carries a data map")
			}
			return nil, err
		}
		if doc.Data != nil {
			return doc.Data, nil
		}
	}
}

// jsonLanes reads a JSON list of lane names from a data key. A missing key is
// an error when required and an empty list when not.
func jsonLanes(data map[string]string, key string, required bool) ([]string, error) {
	raw, ok := data[key]
	if !ok {
		if required {
			return nil, fmt.Errorf("carries no %s", key)
		}
		return nil, nil
	}
	var lanes []string
	if err := json.Unmarshal([]byte(raw), &lanes); err != nil {
		return nil, fmt.Errorf("%s is not a JSON list of lanes: %v", key, err)
	}
	return lanes, nil
}

// executorLaneNames is the lane each EXECUTOR_LANES entry names: a bare lane
// is itself, `lane@repo` (a per-repo canary) is its lane. `!lane@repo` (keep
// that repo on Dagger) names no lane of its own: it contributes nothing, and
// its lane must be there bare (unbacked otherwise, as daedalus refuses it at
// load). The shape is the one daedalus refuses at load: exactly one '@', a
// non-empty lane without '!' and a repo with at most one '/' (bare or
// owner/repo). Malformed entries come back apart, contributing no lane.
func executorLaneNames(entries []string) (names, malformed, unbacked []string) {
	var excluded [][2]string
	for _, e := range entries {
		body, exclusion := strings.CutPrefix(e, "!")
		lane, repo, scoped := strings.Cut(body, "@")
		switch {
		case !scoped && !exclusion && !strings.Contains(e, "!"):
			names = append(names, e)
		case !scoped, lane == "" || repo == "" || strings.Contains(repo, "@") || strings.Contains(lane, "!") || strings.Count(repo, "/") > 1:
			malformed = append(malformed, e)
		case exclusion:
			excluded = append(excluded, [2]string{e, lane})
		default:
			names = append(names, lane)
		}
	}
	for _, x := range excluded {
		if !slices.Contains(entries, x[1]) {
			unbacked = append(unbacked, x[0])
		}
	}
	return names, malformed, unbacked
}

func sortedSet(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range list {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

func symmetricDiff(a, b []string) []string {
	var out []string
	for _, x := range sortedSet(a) {
		if !slices.Contains(b, x) {
			out = append(out, x+" (only in "+recordLanesKey+")")
		}
	}
	for _, x := range sortedSet(b) {
		if !slices.Contains(a, x) {
			out = append(out, x+" (only in record_lanes)")
		}
	}
	return out
}
