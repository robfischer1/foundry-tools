package checks

import (
	"regexp"
	"strings"
	"testing"
)

// THE DEFECT THIS FILE HOLDS SHUT (foundry-tools#7626, measured 2026-09-09).
//
// The gate could not go green in ANY repository at the declared pin, and not
// one of the three reasons was in a repo's code. They were all properties of
// the containers the atoms ran in: no curl for opengrep's installer, no git for
// the tree walk, and a go command that resolved forgejo.notusmi.com straight at
// the forge's SSO portal. A verdict vector whose max state is 2 refuses the
// push, so every repo taking the delivered hook stopped being pushable.
//
// Each test below pins one of the properties that fix rests on.

// The images the atoms run in must be the fleet's own CI images. A public base
// is a base nobody in this fleet controls the contents of, and the contents are
// exactly what broke.
//
// THE HOST IS zot, AND MOVING IT BACK TO THE FORGE IS THE REGRESSION. This read
// forgejo.notusmi.com until 2026-09-10. The digests did not change in that move
// and must not: the assertion is about WHERE the fleet's images are addressed,
// not which images they are. Two of the four now exist only on zot, so a revert
// of this one string is an unpullable gate.
func TestEveryLaneImageIsAFleetCIImage(t *testing.T) {
	const want = "registry.notusmi.com/rob/stellar_core:"
	for _, img := range []string{ImageGo, ImagePython, ImageRust, ImageTS, ImageFleet} {
		if !strings.HasPrefix(img, want) {
			t.Errorf("lane image %q is not one of the fleet's CI images (%s…) — the engine and CI would grade with two toolchains free to disagree", img, want)
		}
	}
}

// BY DIGEST, NEVER BY TAG. All four CI images are MOVING tags: CronJob
// foundry-weekly rebuilds them every Monday and base-rescan rebuilds them
// whenever the vulnerability DB moves. A gate whose image floats produces a
// verdict that is not a function of the pin the door declared, and that pin is
// the whole of the F7 join.
//
// PinRefPattern is the canonical extractor's own form — the same one
// sweep:digest-pins reads pins with — so this asserts the block is legible to
// the sweep that exists to notice a collected digest, not merely that a hex
// string is present.
func TestEveryImageIsPinnedByDigest(t *testing.T) {
	ref := regexp.MustCompile(PinRefPattern)
	surface := regexp.MustCompile(PinSurfacePattern)
	for _, img := range LaneImages {
		if !surface.MatchString(img) {
			t.Errorf("image %q carries no digest — a rebuild would move the gate under the declared pin", img)
			continue
		}
		if !ref.MatchString(img) {
			t.Errorf("image %q carries a digest the canonical extractor cannot read", img)
		}
	}
}

// Every image an atom names has to be in the block a digest sweep lands on. An
// image referenced from the atom table but absent from LaneImages is an image
// no pin check would ever look at.
func TestEveryAtomRunsInAListedImage(t *testing.T) {
	listed := map[string]bool{}
	for _, img := range LaneImages {
		listed[img] = true
	}
	for _, a := range Atoms {
		if a.Image == "" {
			t.Errorf("atom %q names no image", a.ID)
			continue
		}
		if !listed[a.Image] {
			t.Errorf("atom %q runs in %q, which LaneImages does not list — nothing would ever check that pin", a.ID, a.Image)
		}
	}
}

// The go command's coordinates, asserted in the order that matters. The door
// answers 404 for anything that is not a fleet module, so it must come FIRST
// and `direct` must come LAST: reversed, the fetch leaves for forgejo directly
// and meets the SSO portal, which is the measured failure.
func TestGoProxyPutsTheDoorFirstAndDirectLast(t *testing.T) {
	parts := strings.Split(GoProxy, ",")
	if len(parts) < 2 {
		t.Fatalf("GOPROXY %q has no fallback at all", GoProxy)
	}
	if !strings.HasPrefix(parts[0], "http://ourea.default.svc.cluster.local:") {
		t.Errorf("GOPROXY does not start at the in-cluster door: %q", GoProxy)
	}
	if parts[len(parts)-1] != "direct" {
		t.Errorf("GOPROXY does not end at `direct`: %q", GoProxy)
	}
	for _, p := range parts[:len(parts)-1] {
		if p == "direct" {
			t.Errorf("GOPROXY reaches `direct` before its last element — the fetch would meet the SSO portal: %q", GoProxy)
		}
	}
}

// GOPRIVATE IS EMPTY ON PURPOSE, and the emptiness is the assertion. go-ci
// BAKES GOPRIVATE=git.notusmi.com,forgejo.notusmi.com for the act lane's
// netrc; GOPRIVATE is GONOPROXY's default; a GONOPROXY naming the forge sends
// the fetch direct to it however right GOPROXY is. Anything non-empty here
// silently restores the bug.
func TestGoPrivateIsEmptySoTheImagesBakedOneCannotWin(t *testing.T) {
	if GoPrivate != "" {
		t.Errorf("GOPRIVATE is %q — a non-empty value re-enables the direct fetch that meets the SSO portal", GoPrivate)
	}
	if GoNoSumDB != "forgejo.notusmi.com" {
		t.Errorf("GONOSUMDB is %q — the forge host is what GOPRIVATE was covering", GoNoSumDB)
	}
}

// ── the atom bodies whose classification the fix changed ─────────────────────

// A CHECK THAT CANNOT FIND ITS TOOL HAS NOT FOUND ANYTHING WRONG. The canonical
// script raises FileNotFoundError when git is absent — an uncaught traceback,
// so python exits 1 — and the old body's `|| exit 1` filed that as FINDINGS.
func TestStopJustificationsCallsAMissingGitACannotRun(t *testing.T) {
	body := AtomByID("fleet:stop-justifications").Script
	if !strings.Contains(body, "command -v git >/dev/null 2>&1 ||") {
		t.Error("the atom does not probe for git before running the script")
	}
	if !strings.Contains(body, "CANNOT RUN - git is not on PATH") {
		t.Error("a missing git does not say it could not run")
	}
	if !strings.Contains(body, "command -v python3 >/dev/null 2>&1 ||") {
		t.Error("the atom does not probe for the interpreter the canonical script needs")
	}
	if strings.Contains(body, "stop_justifications.py . || exit 1") {
		t.Error("the script's exit code is flattened to 1 — its own CANNOT RUN (exit 2) would be reported as findings")
	}
	if !strings.Contains(body, `[ "$code" -eq 0 ] || exit "$code"`) {
		t.Error("the script's exit code is not passed through")
	}
}

// A LINKED WORKTREE'S `.git` IS A FILE, and it dangles inside the container:
// the gate hook hands the engine `--source="$PWD"` and this fleet works in
// linked worktrees, so this is the common case, not an edge one.
// THE FLEET ATOMS GRADE THE REPOSITORY, NOT THE DIRECTORY ON DISK.
//
// Measured 2026-09-09 on tongs, a rust star: fleet:check-added-large-files
// answered 40+ findings, every one a file under target/ that git ignores and no
// commit could carry. The same walk fed fleet:detect-secrets a gitignored
// .pytest_cache. A check that refuses every rust and node developer's push over
// their own build directory is a check nobody leaves switched on, and a finding
// about a file that cannot be committed is not a finding about the repository.
func TestTheFleetFileAtomsTakeTheirPopulationFromGit(t *testing.T) {
	// Each of these enumerates files and grades what it finds.
	for _, id := range []string{
		"fleet:check-yaml",
		"fleet:check-added-large-files",
		"fleet:check-merge-conflict",
		"fleet:detect-secrets",
		"fleet:stop-justifications",
	} {
		body := AtomByID(id).Script
		if !strings.Contains(body, worktreeRepo) {
			t.Errorf("atom %q enumerates files and does not carry the git prelude", id)
		}
		if strings.Contains(body, "find . -path ./.git") {
			t.Errorf("atom %q still walks the directory — gitignored build artifacts would be findings about the repository", id)
		}
	}
}

// git ABSENT IS A CANNOT RUN FOR EVERY ONE OF THEM, and the prelude is the one
// place that says so — an atom that reaches its own body without git would
// enumerate nothing and call it clean.
func TestTheGitPreludeRefusesRatherThanScanningNothing(t *testing.T) {
	if !strings.Contains(worktreeRepo, "command -v git >/dev/null 2>&1 ||") {
		t.Fatal("the prelude does not probe for git")
	}
	if !strings.Contains(worktreeRepo, "CANNOT RUN - git is not on PATH") {
		t.Error("a missing git does not say it could not run")
	}
	if !strings.Contains(worktreeRepo, "exit 2") {
		t.Error("a missing git is not a 2")
	}
	// The probe has to come FIRST: the normalisation below it is itself git.
	if strings.Index(worktreeRepo, "command -v git") > strings.Index(worktreeRepo, "git init") {
		t.Error("the prelude runs git before checking git is there")
	}
}

func TestEveryAtomThatShellsToGitSurvivesALinkedWorktree(t *testing.T) {
	// MEASURED on two atoms, which is why the prelude is shared rather than
	// copied: stop-justifications answered CANNOT RUN on its `git ls-files`
	// walk, and detect-secrets exited 1 — a FINDING, for a scan that never
	// happened — having printed nothing but git's own refusal.
	for _, id := range []string{"fleet:stop-justifications", "fleet:detect-secrets"} {
		body := AtomByID(id).Script
		if !strings.Contains(body, worktreeRepo) {
			t.Errorf("atom %q shells out to git and does not carry the worktree prelude", id)
		}
	}
	if !strings.Contains(worktreeRepo, "if [ -f .git ]; then") {
		t.Error("the prelude does not notice a linked worktree's .git FILE")
	}
	if !strings.Contains(worktreeRepo, "git init -q .") {
		t.Error("the prelude does not give the mounted tree a readable repository")
	}
	if !strings.Contains(worktreeRepo, "git remote add origin") {
		t.Error("origin is not reconstructed — repo_name() keys DIRECTORY_EXEMPT on it, so an exemption Rob granted would evaporate on a worktree push")
	}
	if !strings.Contains(worktreeRepo, "git update-index -z --add --stdin") {
		t.Error("the synthesised index is never filled, so `git ls-files` would answer an empty tree")
	}
	// A PRIMARY CHECKOUT MUST BE UNTOUCHED: there `.git` is a directory, the
	// real index rides along with it, and re-initialising would throw away the
	// tracked-file population the atom is supposed to read.
	if !strings.Contains(worktreeRepo, "[ -f .git ]") || strings.Contains(worktreeRepo, "[ -d .git ]") {
		t.Error("the prelude does not key on .git being a FILE, so it could fire on a primary checkout")
	}
}

// opengrep is baked into every lane image now, and the curl installer is the
// fallback. Neither absent is a pass.
func TestOpengrepRefusesRatherThanPassingWhenItCannotBeProvisioned(t *testing.T) {
	body := AtomByID("fleet:opengrep-sast").Script
	if !strings.Contains(body, "command -v curl >/dev/null 2>&1 ||") {
		t.Error("the atom runs the curl installer without checking curl is there — `sh: 3: curl: not found` was the measured cannot-run")
	}
	if !strings.Contains(body, "not baked into this lane image and no curl") {
		t.Error("a missing curl does not name the fix")
	}
	if strings.Count(body, "exit 2") < 2 {
		t.Error("a provisioning failure must be a 2, never a 0")
	}
}
