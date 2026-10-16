package atomkit

// ContainerPlan is plan@1, /work/plan/<container>.json: what one container's
// serve runs.
type ContainerPlan struct {
	Version     int         `json:"version"` // 1
	Container   string      `json:"container"`
	MaxParallel int         `json:"max_parallel"`
	Preflight   []string    `json:"preflight"` // copied from run-plan@1 containers[].preflight; [] when none
	Atoms       []PlanEntry `json:"atoms"`
}

// PlanEntry is one atom of a ContainerPlan.
type PlanEntry struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"` // logic | command | capability
	Exec       string            `json:"exec"` // /atoms/<ns>/<name>/atom, /work/catalogue/atoms/<ns>/<name>.toml or /executor/atomrun
	Stage      string            `json:"stage"`
	Lane       string            `json:"lane"`
	TimeoutS   int               `json:"timeout_s"`
	After      []string          `json:"after"`
	WritesTree bool              `json:"writes_tree"`
	Needs      []string          `json:"needs"`
	Args       []string          `json:"args"`          // lane dev, and capability entries
	Env        map[string]string `json:"env,omitempty"` // per-entry environment, e.g. DEV_FRESH=1
}

// DevRequest is the dev-request@1 an input commit's message carries. It is
// in-process only: lead parses it, nothing writes it to a wire file.
type DevRequest struct {
	Verb  string
	Args  []string
	Fresh bool
}

// CacheStats is /work/cache-stats/<escaped id>.json.
type CacheStats struct {
	Hit  int64 `json:"hit"`
	Miss int64 `json:"miss"`
	L2   int64 `json:"l2"`
}
