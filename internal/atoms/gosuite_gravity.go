package atoms

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
)

// readStaged reads the staged binary; a variable so a test can say it will not read.
var readStaged = os.ReadFile

// stagedGravitySum is the sha256 of the gravity binary the regen tests will run:
// the file GRAVITY names, else the one `gravity` finds on PATH, the lookup the
// tests make. It is read from the bytes on disk and not from a table of pins, so
// it is the hash of what is staged on either runner (the Dagger lane's
// /usr/local/bin/gravity, the executor's provisioned tool). "" with no error is
// no generator staged: the variable is left unset and the test says so itself.
func stagedGravitySum() (string, error) {
	name := os.Getenv("GRAVITY")
	if name == "" {
		name = "gravity"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", nil
	}
	b, err := readStaged(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
