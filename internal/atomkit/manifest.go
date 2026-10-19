package atomkit

import (
	"errors"
	"fmt"
	"path"
	"sort"
)

// ManifestSchema is the schema key of a command atom's TOML file.
const ManifestSchema = "command-atom@1"

// Manifest is command-atom@1: an atom whose whole job is one command. The
// decoded form is also testdata/command-atom.json.
type Manifest struct {
	Schema     string            `json:"schema" toml:"schema"`
	ID         string            `json:"id" toml:"id"`
	Stage      string            `json:"stage" toml:"stage"`
	Lane       string            `json:"lane" toml:"lane"`
	Group      string            `json:"group" toml:"group"`
	Desc       string            `json:"desc" toml:"desc"`
	Surface    string            `json:"surface" toml:"surface"`
	Toolchain  string            `json:"toolchain" toml:"toolchain"`
	Argv       []string          `json:"argv" toml:"argv"`
	PerModule  bool              `json:"per_module" toml:"per_module"`
	Judge      string            `json:"judge" toml:"judge"` // exit | output
	Needs      []string          `json:"needs" toml:"needs"`
	Timeout    string            `json:"timeout" toml:"timeout"`
	SubsumedBy string            `json:"subsumed_by" toml:"subsumed_by"`
	NotOnForks string            `json:"not_on_forks" toml:"not_on_forks"`
	WritesTree bool              `json:"writes_tree" toml:"writes_tree"`
	Args       string            `json:"args" toml:"args"`                     // none | append
	Default    []Default         `json:"default,omitempty" toml:"default"`     // args.go; omitted when empty
	Preflight  *bool             `json:"preflight,omitempty" toml:"preflight"` // false opts out; unset is true
	Env        map[string]string `json:"env,omitempty" toml:"env"`
}

// ParseManifest decodes a command-atom@1 file strictly (lld.md FN-055): an
// unknown key, a wrong schema, a missing field, a value outside its closed
// set, or more than one command is an error. The error text carries no path;
// the caller adds it.
func ParseManifest(b []byte) (Manifest, error) {
	var m Manifest
	if err := decodeStrict(b, &m); err != nil {
		return Manifest{}, err
	}
	if err := checkSchema(m.Schema, ManifestSchema); err != nil {
		return Manifest{}, err
	}
	if m.ID == "" {
		return Manifest{}, errors.New("missing id")
	}
	if len(m.Argv) == 0 {
		return Manifest{}, errors.New("missing argv")
	}
	if m.Toolchain == "" {
		return Manifest{}, errors.New("missing toolchain")
	}
	if m.Judge == "" {
		m.Judge = "exit"
	}
	if m.Args == "" {
		m.Args = "none"
	}
	if m.Timeout == "" {
		m.Timeout = "10m"
	}
	if m.Judge != "exit" && m.Judge != "output" {
		return Manifest{}, fmt.Errorf("judge %s is not exit or output", m.Judge)
	}
	if m.Args != "none" && m.Args != "append" {
		return Manifest{}, fmt.Errorf("args %s is not none or append", m.Args)
	}
	if err := checkDefaults(m); err != nil {
		return Manifest{}, err
	}
	if err := oneCommand(m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// oneCommand is R46 for a parsed manifest: the script rules first, in rule
// order, and a shell as argv[0] last (DEF-8-028).
func oneCommand(m Manifest) error {
	if b := scriptBreaches(m.AllArgv(), m.Env); len(b) > 0 {
		return errors.New(b[0].Text)
	}
	if name := path.Base(m.Argv[0]); shells[name] {
		return fmt.Errorf("argv[0] %s is not allowed", name)
	}
	return nil
}

// sortedKeys is a map's keys in order, so a breach list is the same every run.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
