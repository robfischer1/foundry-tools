package goregen

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Rec is a file provenance.json records: the key of its block, the file's name,
// its sha256 and its size.
type Rec struct {
	Key, File, SHA256 string
	Bytes             int
}

// Prov is the part of a core's provenance.json the regeneration reads. The rest
// (the wit and tape pins, notes) is the person's, and is never rewritten.
type Prov struct {
	CoreCommit, CoreBuild, CoreToolchain string
	GenCommit, GenCommand                string
	Guest                                Rec
	Embedded                             []Rec // embedded_wasm, embedded_wasm_<x>, in key order
	Bindings                             *Rec  // nil where the core records none (the older ones)
}

// Records is every record the regeneration refreshes, in the file's key order.
func (p Prov) Records() []Rec {
	out := append([]Rec{p.Guest}, p.Embedded...)
	if p.Bindings != nil {
		out = append(out, *p.Bindings)
	}
	return out
}

// ParseProv reads provenance.json. A file that names no core commit, generator
// or recorded guest and embedded module is not a gravity core's.
func ParseProv(text string) (Prov, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &top); err != nil {
		return Prov{}, fmt.Errorf("provenance.json is not JSON: %v", err)
	}
	var doc struct {
		Core struct {
			Commit    string `json:"commit"`
			Build     string `json:"build"`
			Toolchain string `json:"toolchain"`
		} `json:"core"`
		Generator struct {
			Commit  string `json:"commit"`
			Command string `json:"command"`
		} `json:"generator"`
	}
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return Prov{}, fmt.Errorf("provenance.json: %v", err)
	}
	p := Prov{
		CoreCommit: doc.Core.Commit, CoreBuild: doc.Core.Build, CoreToolchain: doc.Core.Toolchain,
		GenCommit: doc.Generator.Commit, GenCommand: doc.Generator.Command,
	}
	if p.CoreCommit == "" {
		return Prov{}, errors.New("provenance.json records no core.commit")
	}
	if p.GenCommit == "" || p.GenCommand == "" {
		return Prov{}, errors.New("provenance.json records no generator commit and command")
	}
	read := func(key string) (Rec, error) {
		var r struct {
			File   string `json:"file"`
			SHA256 string `json:"sha256"`
			Bytes  int    `json:"bytes"`
		}
		if err := json.Unmarshal(top[key], &r); err != nil {
			return Rec{}, fmt.Errorf("provenance.json %s: %v", key, err)
		}
		return Rec{Key: key, File: r.File, SHA256: r.SHA256, Bytes: r.Bytes}, nil
	}
	var err error
	if _, ok := top["guest_wasm"]; !ok {
		return Prov{}, errors.New("provenance.json records no guest_wasm")
	}
	if p.Guest, err = read("guest_wasm"); err != nil {
		return Prov{}, err
	}
	keys := make([]string, 0, len(top))
	for k := range top {
		if strings.HasPrefix(k, "embedded_wasm") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		r, err := read(k)
		if err != nil {
			return Prov{}, err
		}
		p.Embedded = append(p.Embedded, r)
	}
	if len(p.Embedded) == 0 {
		return Prov{}, errors.New("provenance.json records no embedded_wasm")
	}
	if _, ok := top["bindings"]; ok {
		b, err := read("bindings")
		if err != nil {
			return Prov{}, err
		}
		p.Bindings = &b
	}
	return p, nil
}

var (
	shaField   = regexp.MustCompile(`("sha256"\s*:\s*")[0-9a-f]{64}(")`)
	bytesField = regexp.MustCompile(`("bytes"\s*:\s*)[0-9]+`)
)

// Rewrite puts each record's sha256 and size into its block of the file's text
// and touches nothing else, so the formatting (indent widths differ from file
// to file), the notes and the pins stay as the person wrote them, and a file
// whose inputs did not move comes back byte for byte.
func Rewrite(text string, recs []Rec) (string, error) {
	for _, r := range recs {
		start, end, ok := objectSpan(text, r.Key)
		if !ok {
			return "", fmt.Errorf("provenance.json has no %s record to rewrite", r.Key)
		}
		block := text[start:end]
		if !shaField.MatchString(block) {
			return "", fmt.Errorf("provenance.json %s has no sha256 to rewrite", r.Key)
		}
		if !bytesField.MatchString(block) {
			return "", fmt.Errorf("provenance.json %s has no bytes to rewrite", r.Key)
		}
		block = shaField.ReplaceAllString(block, "${1}"+r.SHA256+"${2}")
		block = bytesField.ReplaceAllString(block, fmt.Sprintf("${1}%d", r.Bytes))
		text = text[:start] + block + text[end:]
	}
	return text, nil
}

// objectSpan finds the object that is the value of a top-level key: the span
// from its `{` to just past its `}`. It reads strings (a note may hold braces and
// escaped quotes) and nesting, and nothing else of JSON.
func objectSpan(text, key string) (start, end int, ok bool) {
	depth := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		case '"':
			close := stringEnd(text, i)
			if close < 0 {
				return 0, 0, false
			}
			if depth == 1 && text[i+1:close] == key {
				j := skipSpace(text, close+1)
				if j < len(text) && text[j] == ':' {
					j = skipSpace(text, j+1)
					if j < len(text) && text[j] == '{' {
						return j, matching(text, j), matching(text, j) > 0
					}
				}
			}
			i = close
		}
	}
	return 0, 0, false
}

// stringEnd is the index of the quote that closes the string opened at i, or -1.
func stringEnd(text string, i int) int {
	for j := i + 1; j < len(text); j++ {
		switch text[j] {
		case '\\':
			j++
		case '"':
			return j
		}
	}
	return -1
}

// skipSpace is the index of the first byte at or after i that is not whitespace.
func skipSpace(text string, i int) int {
	for i < len(text) && strings.IndexByte(" \t\r\n", text[i]) >= 0 {
		i++
	}
	return i
}

// matching is the index just past the `}` that closes the `{` at i, or -1.
func matching(text string, i int) int {
	depth := 0
	for j := i; j < len(text); j++ {
		switch text[j] {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return j + 1
			}
		case '"':
			close := stringEnd(text, j)
			if close < 0 {
				return -1
			}
			j = close
		}
	}
	return -1
}

// registryPath finds the cargo home in a path a guest carries: a NUL-delimited
// string ending in /registry/src/<index>/, which a panic location inside a
// dependency leaves in the data.
var registryPath = regexp.MustCompile(`\x00(/[\x20-\x7e]*?)/registry/src/[A-Za-z0-9._+-]+/`)

// RecordedCargoHome answers the cargo home the recorded guest was built under,
// read out of the bytes themselves (a dependency's source path enters the wasm),
// or "" when the guest carries none (a core whose dependencies leave no path).
// Two homes in one guest are an error: there is no one directory to rebuild in.
func RecordedCargoHome(guest []byte) (string, error) {
	var homes []string
	for _, m := range registryPath.FindAllSubmatch(guest, -1) {
		if h := string(m[1]); !slices.Contains(homes, h) {
			homes = append(homes, h)
		}
	}
	switch len(homes) {
	case 0:
		return "", nil
	case 1:
		return homes[0], nil
	}
	sort.Strings(homes)
	return "", fmt.Errorf("the recorded guest carries registry paths under %d cargo homes (%s)", len(homes), strings.Join(homes, ", "))
}
