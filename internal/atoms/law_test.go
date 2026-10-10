package atoms

import (
	"os"
	"testing"
)

// THE LAW LANES ARE GRADED IN depth BY internal/govlaw; these tests hold what the
// atoms add: they are inert off foundry-stocks, they read the disk, and a tree
// that will not walk is could-not-run.

var lawIDs = []string{"law:lint", "law:verb-liveness", "law:budget"}

func stocksFiles(extra map[string]string) map[string]string {
	files := map[string]string{
		"kits.toml":                  "[kits.root]\nblocks = [\"forge\"]\n",
		"retired-verbs.toml":         ledger,
		"governance/blocks/forge.md": "CI logs through `repo_ci_logs`.\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	return files
}

func TestLawLanesAreAbsentOffFoundryStocks(t *testing.T) {
	for _, id := range lawIDs {
		t.Run(id, func(t *testing.T) {
			in := treeIn(t, map[string]string{
				"governance/blocks/Bad_Name.md": "",
				"renders/c/claude/AGENTS.md":    "`athena_quick_add`",
			})
			expect(t, runAtom(t, id, in), stateOf(0), absent, "not foundry-stocks")
		})
	}
}

func TestLawLanesPassCurrentLaw(t *testing.T) {
	spans := `[{"end_byte":32,"position":0,"slug":"forge","start_byte":0}]`
	in := treeIn(t, stocksFiles(map[string]string{
		"renders/forge-root/claude/AGENTS.md":           "CI logs through `repo_ci_logs`.\n",
		"renders/forge-root/claude/.furnace/spans.json": spans,
		"renders/.budget.toml":                          "[budget.forge-root]\nclaude = 40\n",
	}))
	for _, id := range lawIDs {
		expect(t, runAtom(t, id, in), stateOf(0), pass)
	}
}

func TestVerbLivenessIsRedOnTheAugustText(t *testing.T) {
	in := treeIn(t, stocksFiles(map[string]string{
		"governance/blocks/forge.md": "CI logs through `repo_ci_logs`.\nPin a task with `athena_quick_add`.\n",
	}))
	expect(t, runAtom(t, "law:verb-liveness", in), stateOf(1), findings,
		"governance/blocks/forge.md:2  athena_quick_add is not a verb on hades's surface")
}

func TestLawLanesFindWhatEachOwns(t *testing.T) {
	in := treeIn(t, stocksFiles(map[string]string{
		"governance/blocks/empty.md": "",
		"renders/c/claude/AGENTS.md": "text of the render\n",
		"renders/.budget.toml":       "[budget.c]\nclaude = 5\n",
	}))
	expect(t, runAtom(t, "law:lint", in), stateOf(1), findings, "governance/blocks/empty.md: the block is empty", "has text and no")
	expect(t, runAtom(t, "law:budget", in), stateOf(1), findings, "renders/c/claude: 19 bytes of always-on context is 14 over its ceiling of 5")
}

func TestLawLanesCannotRunOnATreeThatWillNotWalk(t *testing.T) {
	for _, id := range lawIDs {
		t.Run(id, func(t *testing.T) {
			expect(t, runAtom(t, id, missingRoot(t)), stateOf(2), cannot, "the tree would not enumerate")
			in := treeIn(t, stocksFiles(nil))
			in.FS = failingFS{FS: os.DirFS(in.Root), dir: "governance"}
			expect(t, runAtom(t, id, in), stateOf(2), cannot, "the tree would not enumerate: boom")
		})
	}
}
