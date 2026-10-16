package atomkit

import "strings"

// The fixtures of integration-tests.md and unit-tests.md, as Go values. The
// goldens under testdata/ are the same values written out; TestFixturesMatchGoldens
// holds the two together.

var (
	shaH = strings.Repeat("a", 40)
	shaP = strings.Repeat("b", 40)
	shaC = strings.Repeat("c", 40)
	shaD = strings.Repeat("d", 40)
	dg3  = "sha256:" + strings.Repeat("3", 64)
	imgG = "golang:1.26.9-bookworm@sha256:" + strings.Repeat("4", 64)
)

const reasonRuff = "python:ruff-check: ABSENT — no .py file anywhere in the tree (vendor/, testdata/ and _ or . directories do not count), so this repo carries no python to check. Nothing was checked and nothing needed to be."

// RP1 is the run-plan@1 fixture RP-1.
func RP1() RunPlan {
	atom := func(id, kind, container, mount, manifest, lane, surface, group string) PlanAtom {
		return PlanAtom{ID: id, Kind: kind, Container: container, Mount: mount, Manifest: manifest,
			Stage: "precommit", Lane: lane, Surface: surface, Needs: []string{}, TimeoutS: 600,
			History: "shallow", After: []string{}, Group: group}
	}
	return RunPlan{
		Version: 1, Lane: "gate", Repo: "rob/nyx", Sha: shaH, Base: shaP, Ref: "refs/heads/main",
		Door: "http://door.test", CatalogueSha: shaC, DiesSha: shaD, IndexDigest: dg3, Node: "dev01",
		Containers: []Container{{Name: "go", Toolchain: "go", Image: imgG, Preflight: []string{}}},
		Atoms: []PlanAtom{
			atom("fleet:check-merge-conflict", "logic", "lead", "/atoms/fleet/check-merge-conflict", "", "", "any:**", "basic"),
			atom("go:vet", "command", "go", "", "atoms/go/vet.toml", "go", "any:**/go.mod", "language"),
		},
		Absent: []Absent{{ID: "python:ruff-check", Reason: reasonRuff}},
		Phases: []Phase{{Name: "gate", Kind: "fanout", Atoms: []string{"stage:precommit"}}},
	}
}

// PEVET is the plan@1 entry PE-VET.
func PEVET() PlanEntry {
	return PlanEntry{ID: "go:vet", Kind: "command", Exec: "/work/catalogue/atoms/go/vet.toml", Stage: "precommit",
		Lane: "go", TimeoutS: 600, After: []string{}, Needs: []string{}, Args: []string{}}
}

// TF1 is the tree@1 fixture TF-1.
func TF1() Tree {
	return Tree{Version: 1, Root: "/src", Files: []string{"go.mod", "main.go"},
		Committable: []string{"go.mod", "main.go"}, Tracked: []string{"go.mod", "main.go"}, Changed: []string{},
		Origin: "http://door.test/rob/nyx.git"}
}
