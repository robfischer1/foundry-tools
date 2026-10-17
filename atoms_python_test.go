package main

import (
	"regexp"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE PYTHON LANE'S CHAIN TESTS, against the paper engine (engine_fake_test.go):
// python:mutation, the lane's provisioning and the planner's reading of its
// manifest. The lane's other atoms live in atoms/python/<name>/ and are tested
// beside their files.

// ---- pip-audit ----

// pipAuditNeedle finds the audit exec in a chain: the pinned requirement, then the program.
const pipAuditNeedle = `"pip-audit==` + checks.PipAuditVersion + `","pip-audit"`

// ---- mutation ----

// AN AUDIT THAT COULD NOT REACH ITS ADVISORIES IS ASKED AGAIN, NOT FILED. The
// first ask times out against pypi.org and exits 1; read as a finding that
// would have been cached and replayed on every push of the tree (measured on
// iris, 2026-09-17). Read as could-not-run, verdictFor asks again past the
// cache, and the second answer — clean — is the verdict.
func TestPythonPipAuditNetworkFaultIsAskedAgainPastTheCache(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(pipAuditNeedle, 1)
	engine.stdout(pipAuditNeedle,
		"Installed 64 packages in 2.05s\nrequests.exceptions.ReadTimeout: HTTPSConnectionPool(host='pypi.org', port=443): Read timed out. (read timeout=15)\n")
	engine.exitCode(`name:"CA_REASK"`, 0)
	engine.stdout(`name:"CA_REASK"`, "No known vulnerabilities found\n")

	v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "python:pip-audit")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != 0 {
		t.Errorf("a timeout on the first ask and a clean second ask is a pass: %+v", v)
	}
	if engine.chain(pipAuditNeedle, `name:"CA_REASK"`) == "" {
		t.Errorf("the audit was not asked again past the cache")
	}

	// The same exit 1 with a real table is a finding, and is never re-asked.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(pipAuditNeedle, 1)
	engine.stdout(pipAuditNeedle,
		"Found 1 known vulnerability in 1 package\nName    Version ID             Fix Versions\nurllib3 2.2.0   GHSA-34jh-p97f 2.2.2\n")
	v, err = verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "python:pip-audit")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != 1 || !strings.Contains(v.Reason, "GHSA-34jh-p97f") {
		t.Errorf("a real finding is a finding, carrying the table: %+v", v)
	}
	if engine.chain(`name:"CA_REASK"`) != "" {
		t.Errorf("a finding was asked again")
	}
}

// THE `Lanes` VERB HAS TO SAY WHICH HALF IS DECLARED, because the files and
// the manifest now answer different questions and a session asking what runs
// here wants both. A repo with .py and no pyproject lints and tests and does
// not build, and that is a sentence, not an omission.
func TestManifestStateNamesWhatIsMissing(t *testing.T) {
	withIt := manifestState([]string{"pyproject.toml"}, checks.LanePython)
	if withIt != "pyproject.toml" {
		t.Errorf("a declared manifest names itself, got %q", withIt)
	}
	without := manifestState([]string{"README.md"}, checks.LanePython)
	if !strings.Contains(without, "no pyproject.toml") {
		t.Errorf("an absent manifest is named, got %q", without)
	}
	if !strings.Contains(without, "lint and test, no build") {
		t.Errorf("and the consequence is said out loud, got %q", without)
	}
}

// MYPY DOES NOT RUN IN A TREE THAT IS NOT A PROJECT, and this is asserted
// through the PLANNER because that is the only place it is decided — runAtom
// calls a runner directly, so an assertion made there would have passed
// whatever the plan said, which is exactly how the first cut of this shipped.
//
// MEASURED on foundry-dies the day the lane widened: a repo whose only python
// is a pytest test file reported `Cannot find implementation or library stub
// for module named "pytest"`, and the same command with the framework present
// reported no issues. The repository was clean; the checker could not see it.
func TestPythonMypyStandsDownWithoutAManifestWhileLintStillRuns(t *testing.T) {
	tree := map[string]string{
		"README.md":               "",
		"tests/test_contracts.py": "",
	}
	for _, want := range []struct {
		id     string
		absent bool
	}{
		{"python:mypy", true},
		{"python:ruff-check", false},
		{"python:pytest", false},
	} {
		engine.reset()
		engine.withTree(tree)
		vs, err := (&FoundryTools{Source: dag.Directory()}).vector(t.Context(), checks.AtomByID(want.id).Stage, want.id, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(vs) != 1 {
			t.Fatalf("%s: one atom asked, %d answered", want.id, len(vs))
		}
		v := vs[0]
		if want.absent {
			if v.Result != "absent" || v.State != 0 {
				t.Errorf("%s: want an absent 0, got %+v", want.id, v)
			}
			if !strings.Contains(v.Reason, "no pyproject.toml at the repository root") {
				t.Errorf("%s: the absence names the manifest it wanted: %s", want.id, v.Reason)
			}
			// AND IT SAYS THE LANE IS STILL RUNNING. "does not build the lane"
			// would be a lie here: ruff and pytest are running on this very
			// tree, and a reader who believed the lane was off would go
			// looking for the wrong thing.
			if !strings.Contains(v.Reason, "lint and tests still run") {
				t.Errorf("%s: the absence must not read as the lane being off: %s", want.id, v.Reason)
			}
			continue
		}
		if v.Result == "absent" {
			t.Errorf("%s: a .py-declared lane still lints and tests, got %+v", want.id, v)
		}
	}
}

// THE PYTHON LANE'S TOOLS ARE INSTALLED ONCE, AT EXACT VERSIONS, IN THE
// PROVISION LAYER. `uvx ruff@x` and `uv run --with cosmic-ray` resolved the tool
// on every run; the go lane's `go install <module>@<version>` is the shape this
// holds them to. Asserted on the chain: the install and its probe are there, are
// exact, and sit BEFORE the cache volumes mount (a layer never reads a volume).
func TestPythonLaneInstallsItsToolsOnceAtExactVersions(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "python:pip-audit", ""), 0)
	c := engine.chain(`"pip-audit"`, "exitCode")

	cacheAt := strings.Index(c, `path:"/opt/uv-cache"`)
	if cacheAt < 0 {
		t.Fatalf("the uv cache volume is not mounted:\n%s", c)
	}
	for _, tool := range checks.PythonLaneTools {
		install := `args:["uv","tool","install","` + tool.Name + `==` + tool.Version + `"]`
		at := strings.Index(c, install)
		if at < 0 {
			t.Errorf("the lane must install %s at its exact pin (%s):\n%s", tool.Name, install, c)
			continue
		}
		if at > cacheAt {
			t.Errorf("%s is installed after the cache volume mounts; a provision layer must not read a volume", tool.Name)
		}
		if !hasCall(c, "withExec", `args:["`+strings.Join(tool.Probe, `","`)+`"]`) {
			t.Errorf("%s has no probe %v", tool.Name, tool.Probe)
		}
	}
	for _, env := range []string{`name:"UV_TOOL_DIR"`, `name:"UV_TOOL_BIN_DIR"`} {
		if !strings.Contains(c, env) {
			t.Errorf("the lane must set %s for `uv tool install`:\n%s", env, c)
		}
	}
}

// NO `uv run --with` IN THE PYTHON LANE NAMES A FLOATING VERSION. The one floor
// that remains is forge-testkit's, which comes from the fleet's own index and is
// a floor on purpose (checks.PythonMutationTestkit).
func TestPythonLaneNamesNoFloatingWith(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "python:pip-audit", ""), 0)
	all := strings.Join(engine.chains(), "\n")
	for _, m := range regexp.MustCompile(`"--with","([^"]+)"`).FindAllStringSubmatch(all, -1) {
		if !strings.Contains(m[1], "==") {
			t.Errorf("`--with %s` floats; pin it with == in checks/toolpins.go", m[1])
		}
	}
}
