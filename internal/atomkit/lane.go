package atomkit

import (
	"fmt"
	"slices"
)

// LaneSchema is the schema key of a lane file.
const LaneSchema = "lane@1"

// KnownLanes are the lanes the dispatcher knows how to run.
var KnownLanes = []string{"gate", "check", "orbit", "visual", "build", "mutation", "mutation-bg", "bundle", "publish", "bases", "cast", "govern", "atoms", "dev"}

var (
	knownStages = []string{"precommit", "prepush", "mutation", "orbit", "visual", "govern", "dev", "atoms"}
	knownCaps   = []string{"release", "assemble", "scan", "kms-sign", "publish", "push-bundle", "layer-cast", "build-atoms", "dev-output"}
)

// PhaseDef is a phase as a lane file declares it. A run plan holds the same shape.
type PhaseDef = Phase

// LaneDef is lane@1: which atoms a lane runs, in what phases.
type LaneDef struct {
	Schema string     `json:"schema" toml:"schema"`
	Lane   string     `json:"lane" toml:"lane"`
	Stages []string   `json:"stages,omitempty" toml:"stages"`
	Rider  string     `json:"rider,omitempty" toml:"rider"`
	Phases []PhaseDef `json:"phases,omitempty" toml:"phase"`
}

// ParseLane decodes a lane@1 file strictly and checks it (lld.md FN-107).
func ParseLane(b []byte) (LaneDef, error) {
	var d LaneDef
	if err := decodeStrict(b, &d); err != nil {
		return LaneDef{}, err
	}
	if err := checkSchema(d.Schema, LaneSchema); err != nil {
		return LaneDef{}, err
	}
	if !slices.Contains(KnownLanes, d.Lane) {
		return LaneDef{}, fmt.Errorf("lane %s is unknown", d.Lane)
	}
	for _, s := range d.Stages {
		if !slices.Contains(knownStages, s) {
			return LaneDef{}, fmt.Errorf("stage %s is unknown", s)
		}
	}
	if err := checkPhases(d.Phases); err != nil {
		return LaneDef{}, err
	}
	if err := checkSecrets(d.Phases); err != nil {
		return LaneDef{}, err
	}
	return d, nil
}

func checkPhases(phases []PhaseDef) error {
	seen := map[string]bool{}
	for _, p := range phases {
		if p.Kind != "fanout" && p.Kind != "sequence" {
			return fmt.Errorf("phase %s: kind %s is not fanout or sequence", p.Name, p.Kind)
		}
		switch {
		case len(p.Atoms) > 0 && p.Capability != "":
			return fmt.Errorf("phase %s: names atoms and a capability", p.Name)
		case len(p.Atoms) == 0 && p.Capability == "":
			return fmt.Errorf("phase %s: names no atoms and no capability", p.Name)
		case p.Capability != "" && !slices.Contains(knownCaps, p.Capability):
			return fmt.Errorf("capability %s is unknown", p.Capability)
		}
		for _, a := range p.After {
			if !seen[a] {
				return fmt.Errorf("phase %s: after %s, which is not an earlier phase", p.Name, a)
			}
		}
		if seen[p.Name] {
			return fmt.Errorf("phase %s is named twice", p.Name)
		}
		seen[p.Name] = true
	}
	return nil
}

// checkSecrets: a secret reaches one phase's container only (IT-301).
func checkSecrets(phases []PhaseDef) error {
	owner := map[string]string{}
	for _, p := range phases {
		for _, s := range p.Secrets {
			if prev, ok := owner[s]; ok {
				return fmt.Errorf("secret %s is named by phases %s and %s", s, prev, p.Name)
			}
			owner[s] = p.Name
		}
	}
	return nil
}

// ExpandPhases is the lane's phases: those it declares, or else one fanout
// phase over its stages' atoms (lld.md FN-109).
func ExpandPhases(d LaneDef) []PhaseDef {
	if len(d.Phases) > 0 {
		return d.Phases
	}
	atoms := make([]string, len(d.Stages))
	for i, s := range d.Stages {
		atoms[i] = "stage:" + s
	}
	return []PhaseDef{{Name: d.Lane, Kind: "fanout", Atoms: atoms}}
}
