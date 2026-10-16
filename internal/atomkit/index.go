package atomkit

// AtomIndex is atom-index@1, the OCI artifact the atom builder publishes and
// the dispatcher reads (modules.md IF-036). It lives here so its golden runs
// under the same round-trip as the other wire shapes.
type AtomIndex struct {
	Schema          string              `json:"schema"`
	FoundryToolsSha string              `json:"foundry_tools_sha"`
	Atoms           []IndexAtom         `json:"atoms"`
	Lanes           map[string]LaneDef  `json:"lanes"`
	Toolchains      map[string]string   `json:"toolchains"`
	Preflights      map[string][]string `json:"preflights"`
	Services        map[string]string   `json:"services"`
	Tools           map[string]ToolPin  `json:"tools"`
}

// IndexAtom is one atom of the index: Digest for a logic atom, Manifest for a
// command atom.
type IndexAtom struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"` // logic | command
	Digest       string   `json:"digest,omitempty"`
	Manifest     string   `json:"manifest,omitempty"`
	BinarySHA256 string   `json:"binary_sha256,omitempty"`
	Toolchain    string   `json:"toolchain"`
	Stage        string   `json:"stage"`
	Lane         string   `json:"lane"`
	Group        string   `json:"group"`
	Surface      string   `json:"surface"`
	Needs        []string `json:"needs"`
	TimeoutS     int      `json:"timeout_s"`
	History      string   `json:"history"`
	WritesTree   bool     `json:"writes_tree"`
	SubsumedBy   string   `json:"subsumed_by"`
	NotOnForks   string   `json:"not_on_forks"`
	AbsentReason string   `json:"absent_reason"`
	ForkReason   string   `json:"fork_reason"`
}
