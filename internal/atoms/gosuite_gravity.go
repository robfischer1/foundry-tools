package atoms

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"

	"dagger/foundry-tools/internal/checks"
)

// readStaged reads the staged binary; a variable so a test can say it will not read.
var readStaged = os.ReadFile

// stagedGravity is the generator the regen tests will run for a tree that pins
// rev: the file GRAVITY names; else the prebuilt the pod provisions for that rev
// (checks.GravityTool on PATH), which the tests are then told by GRAVITY; else
// the one `gravity` finds on PATH, the lookup the tests make (the Dagger lane
// stages exactly one, built at the tree's rev). path "" with no error is no
// generator staged. set is the GRAVITY value to hand the tests, "" when the
// environment already says which.
//
// THE POD PROVISIONS EVERY PREBUILT AND THE TREE CHOOSES, because the executor
// plans before it reads a tree: a gravity on PATH of a different rev than the
// tree's own provenance.json pins is a failed regenerate-and-diff, not a pass.
func stagedGravity(rev string) (path, set string, err error) {
	if name := os.Getenv("GRAVITY"); name != "" {
		path, err = exec.LookPath(name)
		return path, "", nilIfMissing(err)
	}
	if path, err = exec.LookPath(checks.GravityTool(rev)); err == nil {
		return path, path, nil
	}
	path, err = exec.LookPath("gravity")
	return path, "", nilIfMissing(err)
}

// nilIfMissing is no error for a program that is not there: that is no
// generator staged, which the caller answers itself.
func nilIfMissing(err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return nil
	}
	return err
}

// gravitySum is the sha256 of the staged binary, read from the bytes on disk and
// not from a table of pins, so it is the hash of what is staged on either runner.
func gravitySum(path string) (string, error) {
	b, err := readStaged(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
