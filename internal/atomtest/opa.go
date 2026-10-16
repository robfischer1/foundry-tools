package atomtest

import (
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// GoodData is a built bundle's data.json that carries every data root.
const GoodData = `{"authz_audience":{"star_only":{"chaos":["v"],"nyx":["w"]}},"authz_grants":{"a":1},"authz_meta":{"a":1},"path_grants":{"a":1},"subject_aliases":{"a":1},"capabilities":{"stars":{"athena":{"prefix":"plan","verbs":["task_read"],"tier":{"task_read":"default"}}}}}`

// PinnedOpa is what the pinned opa answers to `opa version`.
func PinnedOpa() string { return "Version: " + checks.OpaVersion }

// OpaScript answers for opa, tar and git. Version is what `opa version` says;
// On maps the opa subcommand (test, eval, build) to its answer.
type OpaScript struct {
	Version string
	VCode   int
	On      map[string]func(atoms.Cmd) (string, int)
}

// Answer is the script as an Exec answer.
func (s OpaScript) Answer(c atoms.Cmd) (string, int) {
	switch {
	case c.Name == "git":
		return "abc123", 0
	case c.Name == "tar":
		return GoodData, 0
	case c.Args[0] == "version":
		return s.Version, s.VCode
	}
	if h, ok := s.On[c.Args[0]]; ok {
		return h(c)
	}
	return "", 0
}

// Const0 is an answer that does not depend on the call.
func Const0(out string, code int) func(atoms.Cmd) (string, int) {
	return func(atoms.Cmd) (string, int) { return out, code }
}
