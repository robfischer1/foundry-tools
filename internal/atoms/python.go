package atoms

// THE ATOMS THAT RUN A PYTHON PROGRAM. A repository's own checker
// (tools/check_contracts.py, infra's tools/dup-check, flux's tools/wit-topics)
// is the tree's, so it is run, not reimplemented: what is Go here is everything
// around it, which is what decides ABSENT, what the checker may be handed, and
// what its exit means.
//
// THE CHAINS PROVISIONED ITS PACKAGES AT RUN TIME, per atom, per gate: `uv run
// --with jsonschema`, `--with tomli`, `--with pyyaml`, `uvx --from copier`. The
// binary runs in a container whose venv already holds every one of them, pinned
// (the module's atoms_tools_python.go builds it from pytools/requirements.txt),
// so an atom execs `python3` and nothing resolves a package when it runs. What
// the chain's `uv run --with X` was for is the venv; the checker's own argv is
// unchanged.

const (
	// PythonVenvDir is the venv the tools container installs the packages in.
	PythonVenvDir = "/opt/atoms-py"
	// PythonBinDir is its bin, first on the container's PATH, so `python3` and the
	// entry points (copier, ansible-playbook, ansible-lint) are the venv's.
	PythonBinDir = "/opt/atoms-py/bin"
	// AnsibleCollectionsDir is where the container installed the ansible
	// collections, outside any tree, so a tree's own collections_path cannot place
	// them where lint then reads them as source.
	AnsibleCollectionsDir = "/opt/ansible-collections"
)

// pythonEnv keeps python from writing __pycache__ beside the scripts it imports:
// a checker that imports its sibling modules (tools/check_wit_regenerated.py
// imports wit_from_schema) would otherwise leave tools/__pycache__ in the tree
// it grades. In the chain that was the engine's copy of the tree; the binary
// runs on the tree.
var pythonEnv = []string{"PYTHONDONTWRITEBYTECODE=1"}
