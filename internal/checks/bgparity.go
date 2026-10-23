package checks

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// fleet:bg-parity's judgement (BG1 F2).
//
// daedalus runs as two colours, each reading its own ConfigMap (flux
// prime/daedalus-jobs.yaml, the blue colour's, and prime/daedalus-jobs-green.yaml).
// Almost every key may differ: the colour that is not serving is where a setting
// is tried. Six things may not, because a difference is an outage when the colours
// swap:
//
//   - the consumer group (DAEDALUS_JOBS_GROUP, default "daedalus-jobs"): another name
//     replays the whole ask topic when the colour is activated;
//   - the Job namespace (GATE_JOB_NAMESPACE): the lanes' Jobs, and the record posts
//     from them, would be looked for somewhere else;
//   - RECORD_LANES, as a set: a lane that owes a record to one colour and not the
//     other settles differently depending on which is serving;
//   - every lane's ServiceAccount (the *_JOB_SERVICE_ACCOUNT keys and each
//     RECIPE_JOB_LANES entry's service_account): ci-lane-sa-guard refuses a Job on
//     another account, a 403 on every Job of the lane (flux#5622);
//   - the CI_IDENTITY_V*_LANES lists, as sets: which lanes mint which SVID.
//
// A FILE NOT READ IS A FILE NOT JUDGED: a colour that is missing or does not parse
// is could-not-run, never "they agree". A tree with neither file (or with only the
// blue one, from before the second colour) is the atom's ABSENT, decided there.

// The two files this atom compares.
const (
	BGParityBlueFile  = RecordLanesJobsFile
	BGParityGreenFile = RecordLanesJobsGreenFile
)

// IsBGParityFile reports whether path is one of the two files this atom reads.
func IsBGParityFile(path string) bool {
	return path == BGParityBlueFile || path == BGParityGreenFile
}

const defaultJobsGroup = "daedalus-jobs"

// BGParity judges files (path -> contents of the two ConfigMaps). State 0 when the
// identity keys agree, 1 naming each that differs, 2 when a file is absent or would
// not read.
func BGParity(files map[string]string) (int, string) {
	const id = "fleet:bg-parity"
	cannot := func(format string, a ...any) (int, string) {
		return int(StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %s", id, fmt.Sprintf(format, a...))
	}
	var maps [2]map[string]string
	for i, p := range []string{BGParityBlueFile, BGParityGreenFile} {
		body, ok := files[p]
		if !ok {
			return cannot("%s is not here (the other is): the colours are compared in pairs, and this atom grades nothing until it has both", p)
		}
		data, err := configMapData(body)
		if err != nil {
			return cannot("%s would not parse: %v", p, err)
		}
		maps[i] = data
	}
	blue, green := maps[0], maps[1]
	var out []string
	note := func(key, b, g string) {
		out = append(out, fmt.Sprintf("  %s: blue %s, green %s", key, b, g))
	}
	scalar := func(key, def string) {
		b, bok := blue[key]
		g, gok := green[key]
		if !bok {
			b, bok = def, def != ""
		}
		if !gok {
			g, gok = def, def != ""
		}
		if b != g || bok != gok {
			note(key, orUnset(b, bok), orUnset(g, gok))
		}
	}
	scalar("DAEDALUS_JOBS_GROUP", defaultJobsGroup)
	scalar("GATE_JOB_NAMESPACE", "")
	for _, k := range unionKeys(blue, green, func(k string) bool { return strings.HasSuffix(k, "_JOB_SERVICE_ACCOUNT") }) {
		scalar(k, "")
	}
	for _, k := range append([]string{recordLanesKey}, unionKeys(blue, green, isIdentityLanesKey)...) {
		if k == recordLanesKey {
			b, g, err := listPair(blue, green, k)
			if err != nil {
				return cannot("%v", err)
			}
			if !slices.Equal(b, g) {
				out = append(out, fmt.Sprintf("  %s: blue %v, green %v", k, b, g))
			}
			continue
		}
		bRaw, bok := blue[k]
		gRaw, gok := green[k]
		if bok != gok {
			note(k, orUnset(bRaw, bok), orUnset(gRaw, gok))
			continue
		}
		if !slices.Equal(commaSet(bRaw), commaSet(gRaw)) {
			note(k, fmt.Sprintf("%v", commaSet(bRaw)), fmt.Sprintf("%v", commaSet(gRaw)))
		}
	}
	bAcc, err := recipeAccounts(blue)
	if err != nil {
		return cannot("%s: %v", BGParityBlueFile, err)
	}
	gAcc, err := recipeAccounts(green)
	if err != nil {
		return cannot("%s: %v", BGParityGreenFile, err)
	}
	lanes := map[string]bool{}
	for l := range bAcc {
		lanes[l] = true
	}
	for l := range gAcc {
		lanes[l] = true
	}
	for _, l := range sortedSet(mapKeys(lanes)) {
		b, bok := bAcc[l]
		g, gok := gAcc[l]
		if b != g || bok != gok {
			note("RECIPE_JOB_LANES["+l+"].service_account", orUnset(b, bok), orUnset(g, gok))
		}
	}
	if len(out) == 0 {
		return int(StatePass), id + ": the colours agree on the group, the namespace, RECORD_LANES, every lane's ServiceAccount and the identity lanes"
	}
	return int(StateFindings), fmt.Sprintf("%s: the colours differ where a flip would turn it into an outage:\n%s", id, strings.Join(out, "\n"))
}

func orUnset(v string, set bool) string {
	if !set {
		return "unset"
	}
	return fmt.Sprintf("%q", v)
}

func isIdentityLanesKey(k string) bool {
	return strings.HasPrefix(k, "CI_IDENTITY_V") && strings.HasSuffix(k, "_LANES")
}

// unionKeys is the sorted keys of either map that match.
func unionKeys(a, b map[string]string, match func(string) bool) []string {
	seen := map[string]bool{}
	for _, m := range []map[string]string{a, b} {
		for k := range m {
			if match(k) {
				seen[k] = true
			}
		}
	}
	return sortedKeysOf(seen)
}

func sortedKeysOf(m map[string]bool) []string {
	out := mapKeys(m)
	sort.Strings(out)
	return out
}

func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// listPair reads a required JSON list of lanes from both maps, as sorted sets.
func listPair(blue, green map[string]string, key string) (b, g []string, err error) {
	for i, m := range []map[string]string{blue, green} {
		file := []string{BGParityBlueFile, BGParityGreenFile}[i]
		lanes, e := jsonLanes(m, key, true)
		if e != nil {
			return nil, nil, fmt.Errorf("%s: %v", file, e)
		}
		if i == 0 {
			b = sortedSet(lanes)
		} else {
			g = sortedSet(lanes)
		}
	}
	return b, g, nil
}

// commaSet is a comma-separated list as a sorted set.
func commaSet(raw string) []string {
	var parts []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return sortedSet(parts)
}

// recipeAccounts is each RECIPE_JOB_LANES entry's service_account (lane -> account).
func recipeAccounts(data map[string]string) (map[string]string, error) {
	raw, ok := data["RECIPE_JOB_LANES"]
	if !ok {
		return nil, nil
	}
	var lanes map[string]struct {
		ServiceAccount string `json:"service_account"`
	}
	if err := json.Unmarshal([]byte(raw), &lanes); err != nil {
		return nil, fmt.Errorf("RECIPE_JOB_LANES is not a JSON object of lanes: %v", err)
	}
	out := map[string]string{}
	for l, e := range lanes {
		out[l] = e.ServiceAccount
	}
	return out, nil
}
