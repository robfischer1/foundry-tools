package checks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// TOOL SCHEMAS SERVED (F11 A1-P4, Design 6.3 step 3 and 6.7). The twin of
// dies:capabilities-served. Each star's code serves an input schema per verb
// (stellar-core mcpserve), committed under capabilities/served-inputs/<star>.json
// beside the verb-name export under capabilities/served/<star>.json. The dies
// own the schema files (schema/tool-input, schema/tool-output); this gate says
// the bytes the stars serve are still the bytes the files hold. Pure: the atom
// reads the tree, the judgement is here so a table test can hold each case.

// Paths in the policy die's tree.
const (
	// ServedInputsDir holds each star's served input schemas, <star>.json.
	ServedInputsDir = "capabilities/served-inputs"
	// ToolInputDir holds schema/tool-input/<prefix>_<verb>.schema.json.
	ToolInputDir = "schema/tool-input"
	// ToolOutputDir holds schema/tool-output/<prefix>_<verb>.schema.json.
	ToolOutputDir = "schema/tool-output"
	// ToolSchemasLockPath is the ratchet lock the tool-schema gate and this one share.
	// (The spec names this const ToolSchemasLock too, which collides with the type.)
	ToolSchemasLockPath = "tools/tool_schemas.lock.json"
)

// ToolSchemasServedID is the gate's atom id.
const ToolSchemasServedID = "dies:tool-schemas-served"

const (
	toolSchemasLockFormat = "tool-schemas-lock/1"
	servedInputsSchema    = 1
	toolSchemaSuffix      = ".schema.json"
)

// toolFileRootKeys are the root keys the dies add to or replace on a served body:
// dropped from BOTH sides before they are compared.
var toolFileRootKeys = []string{"$schema", "$id", "title", "x-ops"}

// ServedInputs is one star's export of input schemas: the BARE verb name to the
// schema body as the star serves it.
type ServedInputs struct {
	Schema int                        `json:"schema"`
	Star   string                     `json:"star"`
	Verbs  map[string]json.RawMessage `json:"verbs"`
}

// ToolSchemasLock is tools/tool_schemas.lock.json. The three owed maps are keyed
// by GATEWAY name (prefix_verb) and hold the reason the file is owed.
type ToolSchemasLock struct {
	Format          string              `json:"format"`
	PendingKeywords map[string][]string `json:"pending_keywords"`
	OwedInput       map[string]string   `json:"owed_input"`
	OwedOutput      map[string]string   `json:"owed_output"`
	OwedServed      map[string]string   `json:"owed_served"`
}

// GradeToolSchemasServed is dies:tool-schemas-served over a policy-die tree
// (the caller has already settled that it IS one).
//
// STATES. ABSENT (0) when the tree carries no served-verbs export, no
// served-inputs, no tool schema file and no lock: nothing to compare. FINDINGS
// (1) when the served schemas and the files differ in any way the six rules name,
// or a file cannot be believed. CANNOT RUN (2) when a file that exists cannot be
// read: a comparison nobody made is not one that passed.
func GradeToolSchemasServed(a AtomDef, tr TreeReader) Verdict {
	servedPaths := tr.Glob(CapabilitiesServedDir + "/*.json")
	inputPaths := tr.Glob(ServedInputsDir + "/*.json")
	inFiles := tr.Glob(ToolInputDir + "/*" + toolSchemaSuffix)
	outFiles := tr.Glob(ToolOutputDir + "/*" + toolSchemaSuffix)
	lockPaths := tr.Glob(ToolSchemasLockPath)
	sort.Strings(servedPaths)
	sort.Strings(inputPaths)
	if len(servedPaths)+len(inputPaths)+len(inFiles)+len(outFiles)+len(lockPaths) == 0 {
		return VerdictOf(a, int(StatePass), a.ID+": ABSENT - this tree carries no served-verbs export, no served-inputs under "+ServedInputsDir+"/, no tool schema file and no "+ToolSchemasLockPath+", so there is nothing to compare.")
	}
	cannot := func(format string, args ...any) Verdict {
		return VerdictOf(a, int(StateCannotRun), a.ID+": CANNOT RUN - "+fmt.Sprintf(format, args...))
	}
	var findings []string
	add := func(format string, args ...any) { findings = append(findings, fmt.Sprintf(format, args...)) }

	var lock ToolSchemasLock
	if len(lockPaths) > 0 {
		raw, err := tr.Read(ToolSchemasLockPath)
		if err != nil {
			return cannot("%s could not be read (%v).", ToolSchemasLockPath, err)
		}
		if err := json.Unmarshal([]byte(raw), &lock); err != nil {
			add("%s: not a tool-schemas lock (%v)", ToolSchemasLockPath, err)
		} else if lock.Format != toolSchemasLockFormat {
			add("%s: format %q, this gate reads %q", ToolSchemasLockPath, lock.Format, toolSchemasLockFormat)
		}
	}

	prefixes := map[string]string{}
	for _, p := range tr.Glob("fleet/repos/*.json") {
		raw, err := tr.Read(p)
		if err != nil {
			return cannot("%s could not be read (%v), so the star's route prefix is unknown.", p, err)
		}
		var rec struct {
			VerbPrefix string `json:"verb_prefix"`
		}
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return cannot("%s is not JSON (%v), so the star's route prefix is unknown.", p, err)
		}
		prefixes[strings.TrimSuffix(path.Base(p), ".json")] = rec.VerbPrefix
	}

	var served []Served
	for _, p := range servedPaths {
		raw, err := tr.Read(p)
		if err != nil {
			return cannot("%s could not be read (%v).", p, err)
		}
		s, err := ParseServed(strings.TrimSuffix(path.Base(p), ".json"), []byte(raw))
		if err != nil {
			add("%s: %v", p, err)
			continue
		}
		served = append(served, s)
	}

	inputs := map[string]ServedInputs{}
	for _, p := range inputPaths {
		raw, err := tr.Read(p)
		if err != nil {
			return cannot("%s could not be read (%v).", p, err)
		}
		stem := strings.TrimSuffix(path.Base(p), ".json")
		si, err := parseServedInputs(stem, []byte(raw))
		if err != nil {
			add("%s: %v", p, err)
			continue
		}
		inputs[stem] = si
	}

	// owner maps each served gateway name to its star; haveIn / haveOut are the
	// tool files by gateway name.
	owner := map[string]string{}
	for _, s := range served {
		if prefixes[s.Star] == "" {
			continue
		}
		for _, v := range s.Verbs {
			owner[prefixes[s.Star]+"_"+v] = s.Star
		}
	}
	haveIn, haveOut := toolFileNames(inFiles), toolFileNames(outFiles)

	stars := 0
	verbs := 0
	for _, s := range served {
		prefix := prefixes[s.Star]
		if prefix == "" {
			if len(s.Verbs) > 0 {
				add("%s: serves %d verb(s) but its record has no verb_prefix, so no gateway name can be derived", s.Star, len(s.Verbs))
			}
			continue // no verbs of its own, none served: nothing to compare
		}
		stars++
		si, haveFile := inputs[s.Star]
		inputsRel := ServedInputsDir + "/" + s.Star + ".json"
		servedSet := map[string]bool{}
		for _, v := range s.Verbs {
			servedSet[v] = true
		}
		var owedIn []string
		for _, v := range s.Verbs {
			if _, owed := lock.OwedInput[prefix+"_"+v]; !owed {
				owedIn = append(owedIn, v)
			}
		}
		if !haveFile && len(owedIn) > 0 && !inputPathListed(inputPaths, inputsRel) {
			add("%s: %s is missing", s.Star, inputsRel)
		}
		var extra []string
		for v := range si.Verbs {
			if !servedSet[v] {
				extra = append(extra, v)
			}
		}
		sort.Strings(extra)
		for _, v := range extra {
			add("%s: %s holds %s, which %s/%s.json does not serve", s.Star, inputsRel, v, CapabilitiesServedDir, s.Star)
		}
		for _, v := range s.Verbs {
			verbs++
			gw := prefix + "_" + v
			inFile := ToolInputDir + "/" + gw + toolSchemaSuffix
			outFile := ToolOutputDir + "/" + gw + toolSchemaSuffix
			_, owedInput := lock.OwedInput[gw]
			_, owedOutput := lock.OwedOutput[gw]
			body, hasEntry := si.Verbs[v]
			if !owedInput {
				if haveFile && !hasEntry {
					add("%s: %s is served but %s has no entry for it", s.Star, gw, inputsRel)
				}
				if !haveIn[gw] {
					add("%s: %s has no %s", s.Star, gw, inFile)
				} else if hasEntry {
					raw, err := tr.Read(inFile)
					if err != nil {
						return cannot("%s could not be read (%v).", inFile, err)
					}
					fileCanon, ferr := canonicalToolBody([]byte(raw), true)
					servedCanon, serr := canonicalToolBody(body, true)
					switch {
					case ferr != nil:
						add("%s: %s %s is not a JSON object (%v)", s.Star, gw, inFile, ferr)
					case serr != nil:
						add("%s: %s served schema in %s is not a JSON object (%v)", s.Star, gw, inputsRel, serr)
					case fileCanon != servedCanon:
						add("%s: %s %s differs from the served schema in %s (root keys %s dropped, canonical form compared)", s.Star, gw, inFile, inputsRel, strings.Join(toolFileRootKeys, ", "))
					}
				}
			}
			if !haveOut[gw] && !owedOutput {
				add("%s: %s has no %s", s.Star, gw, outFile)
			}
		}
	}

	// Stale lock rows: a ratchet that never loosens in the other direction is a
	// debt list that cannot be paid.
	for _, gw := range sortedToolKeys(lock.OwedInput) {
		switch {
		case owner[gw] == "":
			add("owed_input: %s is not served by any star - drop the row", gw)
		case haveIn[gw]:
			add("owed_input: %s now has %s/%s%s - drop the row", gw, ToolInputDir, gw, toolSchemaSuffix)
		}
	}
	for _, gw := range sortedToolKeys(lock.OwedOutput) {
		switch {
		case owner[gw] == "":
			add("owed_output: %s is not served by any star - drop the row", gw)
		case haveOut[gw]:
			add("owed_output: %s now has %s/%s%s - drop the row", gw, ToolOutputDir, gw, toolSchemaSuffix)
		}
	}
	for _, gw := range sortedToolKeys(lock.OwedServed) {
		if st := owner[gw]; st != "" {
			add("owed_served: %s is now served by %s - drop the row", gw, st)
		}
	}
	// Orphans: a file no star serves, nor excused.
	for dir, names := range map[string]map[string]bool{ToolInputDir: haveIn, ToolOutputDir: haveOut} {
		for _, gw := range sortedToolKeys(names) {
			if owner[gw] == "" {
				if _, excused := lock.OwedServed[gw]; !excused {
					add("%s/%s%s: no star serves %s and %s does not list it in owed_served", dir, gw, toolSchemaSuffix, gw, ToolSchemasLockPath)
				}
			}
		}
	}
	for stem := range inputs {
		hasServed := false
		for _, s := range served {
			hasServed = hasServed || s.Star == stem
		}
		if !hasServed {
			add("%s: %s/%s.json holds inputs for a star with no served-verbs export", stem, ServedInputsDir, stem)
		}
	}

	if len(findings) > 0 {
		sort.Strings(findings)
		return VerdictOf(a, int(StateFindings), fmt.Sprintf("%s: FINDINGS - the tool schemas the stars serve and the schema files differ (%d):\n%s\nThe served bytes are the source: re-harvest %s and %s from the stars' exports, or settle %s.",
			a.ID, len(findings), strings.Join(findings, "\n"), ToolInputDir+"/", ToolOutputDir+"/", ToolSchemasLockPath))
	}
	return VerdictOf(a, int(StatePass), fmt.Sprintf("%s: %d verb(s) across %d star(s) are filed exactly as served.", a.ID, verbs, stars))
}

func inputPathListed(paths []string, rel string) bool {
	for _, p := range paths {
		if p == rel {
			return true
		}
	}
	return false
}

// parseServedInputs reads one star's served-inputs and refuses what it cannot
// vouch for: unknown fields, another schema version, a star that is not the
// file's stem.
func parseServedInputs(stem string, raw []byte) (ServedInputs, error) {
	var si ServedInputs
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&si); err != nil {
		return ServedInputs{}, fmt.Errorf("not a served-inputs export: %w", err)
	}
	if si.Schema != servedInputsSchema {
		return ServedInputs{}, fmt.Errorf("schema %d, this gate reads schema %d", si.Schema, servedInputsSchema)
	}
	if si.Star != stem {
		return ServedInputs{}, fmt.Errorf("names star %q but is committed as %q", si.Star, stem)
	}
	return si, nil
}

// canonicalToolBody re-serialises a JSON object as sorted keys, compact, with
// HTML escaping off. dropRoot removes the root keys the dies strip ($schema, $id,
// title, x-ops) first; both sides are canonicalised the same way, because a kit may
// serve its own root $schema (calliope serves draft-07) that the dies replace.
func canonicalToolBody(raw []byte, dropRoot bool) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v map[string]any
	if err := dec.Decode(&v); err != nil {
		return "", err
	}
	if v == nil {
		return "", fmt.Errorf("null")
	}
	if dropRoot {
		for _, k := range toolFileRootKeys {
			delete(v, k)
		}
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSuffix(out.String(), "\n"), nil
}

// toolFileNames answers the gateway names of a directory's tool schema files.
func toolFileNames(paths []string) map[string]bool {
	out := map[string]bool{}
	for _, p := range paths {
		out[strings.TrimSuffix(path.Base(p), toolSchemaSuffix)] = true
	}
	return out
}

func sortedToolKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
