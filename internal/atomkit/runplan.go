package atomkit

// The wire shapes below are the in-pod atom executor's (lld.md E1, E7): the
// same types live byte for byte in daedalus internal/runner/wire, and
// testdata/ holds the goldens both repos round-trip.

// RunPlan is run-plan@1, the dispatcher's whole answer to "what runs in this
// pod". It reaches the pod as the pistis.ci/plan annotation, projected to
// /etc/atomrun/run-plan.json.
type RunPlan struct {
	Version      int         `json:"version"` // 1
	Lane         string      `json:"lane"`
	Repo         string      `json:"repo"`
	Sha          string      `json:"sha"`
	Base         string      `json:"base"` // "" = none
	Ref          string      `json:"ref"`
	Door         string      `json:"door"` // http://ourea:8215, no trailing /
	CatalogueSha string      `json:"catalogue_sha"`
	DiesSha      string      `json:"dies_sha"`
	IndexDigest  string      `json:"index_digest"` // "local" for foundry-tools' own runs
	Fork         string      `json:"fork"`         // the vendored record's name; "" = not a fork
	Node         string      `json:"node"`
	Containers   []Container `json:"containers"`
	Atoms        []PlanAtom  `json:"atoms"`
	Absent       []Absent    `json:"absent"`
	Phases       []Phase     `json:"phases"`
	Rider        *Rider      `json:"rider"` // null = none
}

// Container is one toolchain container of the pod.
type Container struct {
	Name      string   `json:"name"`
	Toolchain string   `json:"toolchain"`
	Image     string   `json:"image"`
	Preflight []string `json:"preflight"` // the toolchain's preflight argv; [] when none
}

// PlanAtom is one atom the run planned.
type PlanAtom struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"` // logic | command
	Container    string   `json:"container"`
	Mount        string   `json:"mount"`    // "" for command
	Manifest     string   `json:"manifest"` // "" for logic
	Stage        string   `json:"stage"`
	Lane         string   `json:"lane"` // the atom's checks.Lane value; "" = any lane
	Surface      string   `json:"surface"`
	Needs        []string `json:"needs"`
	TimeoutS     int      `json:"timeout_s"`
	WritesTree   bool     `json:"writes_tree"`
	History      string   `json:"history"` // shallow | full
	After        []string `json:"after"`
	SubsumedBy   string   `json:"subsumed_by"`
	Group        string   `json:"group"`
	AbsentReason string   `json:"absent_reason"` // the sentence lead writes if the surface does not match
}

// Absent is an atom the planner stood down, with the sentence the record keeps.
type Absent struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// Phase is one step of a lane. The lane@1 file declares the same shape.
type Phase struct {
	Name       string   `json:"name" toml:"name"`
	Kind       string   `json:"kind" toml:"kind"` // fanout | sequence
	Atoms      []string `json:"atoms,omitempty" toml:"atoms"`
	Capability string   `json:"capability,omitempty" toml:"capability"`
	Secrets    []string `json:"secrets,omitempty" toml:"secrets"`
	After      []string `json:"after,omitempty" toml:"after"`
}

// Rider is the lane whose record lead posts after its own.
type Rider struct {
	Lane     string `json:"lane"`
	TokenEnv string `json:"token_env"`
}
