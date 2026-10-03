package checks

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// THE LANE'S HALF OF A LOOKUP (incremental mutation, M4): what it asks the
// store, how it reads the answer, and which units it grades itself. Any
// answer it cannot believe is an error, and an error grades every unit cold.

// LookupBody is the lookup a lane posts: its engine and every keyed unit's
// key. An unkeyable unit is never asked about — it is graded.
func LookupBody(lang, engine string, keys []UnitKey) []byte {
	type unit struct {
		Lang   string `json:"lang"`
		Unit   string `json:"unit"`
		Hash   string `json:"hash"`
		Ranges string `json:"ranges"`
	}
	units := []unit{}
	for _, k := range keys {
		if k.Err == "" {
			units = append(units, unit{lang, k.Unit, k.Hash, k.Ranges})
		}
	}
	raw, _ := json.Marshal(struct {
		Engine string `json:"engine"`
		Units  []unit `json:"units"`
	}{engine, units})
	return raw
}

// AcceptLookup reads the store's answer into the gradings this run may reuse,
// by unit. It believes an answer only whole: a grading of another language, a
// unit or key nobody asked about, or a unit answered twice is a store that
// is not answering this question, and the whole answer is refused.
func AcceptLookup(lang string, keys []UnitKey, raw []byte) (map[string]ReusedGrading, error) {
	var ans struct {
		Gradings []ReusedGrading `json:"gradings"`
	}
	if err := json.Unmarshal(raw, &ans); err != nil {
		return nil, fmt.Errorf("the store's answer is not JSON: %v", err)
	}
	asked := map[string]UnitKey{}
	for _, k := range keys {
		if k.Err == "" {
			asked[k.Unit] = k
		}
	}
	got := map[string]ReusedGrading{}
	for _, g := range ans.Gradings {
		k, ok := asked[g.Unit]
		if g.Lang != lang || !ok || g.Hash != k.Hash || g.Ranges != k.Ranges {
			return nil, fmt.Errorf("the store answered a grading nobody asked for: %s %s", g.Lang, g.Unit)
		}
		if _, twice := got[g.Unit]; twice {
			return nil, errors.New("the store answered unit " + g.Unit + " twice")
		}
		got[g.Unit] = g
	}
	return got, nil
}

// Misses are the keys the run grades itself: every one no reused grading
// answers, unkeyable ones included.
func Misses(keys []UnitKey, reused map[string]ReusedGrading) []UnitKey {
	var out []UnitKey
	for _, k := range keys {
		if _, ok := reused[k.Unit]; !ok {
			out = append(out, k)
		}
	}
	return out
}

// MissPatterns are the misses as package patterns relative to the module at
// dir — what gomutants and the cover step are handed instead of ./... .
func MissPatterns(dir string, misses []UnitKey) []string {
	var out []string
	for _, k := range misses {
		rel := strings.TrimPrefix(k.Unit, dir+"/")
		if rel == dir {
			out = append(out, ".")
			continue
		}
		out = append(out, "./"+rel)
	}
	slices.Sort(out)
	return out
}

// Reused lists a lookup's gradings in unit order.
func Reused(reused map[string]ReusedGrading) []ReusedGrading {
	var out []ReusedGrading
	for _, u := range slices.Sorted(maps.Keys(reused)) {
		out = append(out, reused[u])
	}
	return out
}

// ReuseLine is the one line a mutation verdict carries about reuse whatever
// its state — a pass keeps no output (VerdictOf), and "this pass graded only
// some units" is a fact its reader needs: how many of the run's units were
// reused, and from which runs. "" when nothing was reused.
func ReuseLine(atom string, reused []ReusedGrading, units int) string {
	if len(reused) == 0 {
		return ""
	}
	var from []string
	for _, g := range reused {
		from = append(from, fmt.Sprintf("%s (%s #%d @%.12s)", g.Unit, g.Lane, g.RunNumber, g.Sha))
	}
	return fmt.Sprintf("%s: reused %d of %d unit(s), graded earlier at the same content, scope and engine: %s",
		atom, len(reused), units, strings.Join(from, ", "))
}
