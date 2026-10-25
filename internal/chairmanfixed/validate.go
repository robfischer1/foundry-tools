// Package chairmanfixed is the validation of the chairman base's RUN-free image
// (atoms/bases/chairman is its atom). The base used to be made by apt and a
// handful of RUNs whose results (accounts, links, modes) are now committed files
// (bases/chairman/fixed/) laid by COPY, and the packages' maintainer scripts are
// not run at all. This package holds the two to each other: the committed files to
// the tables that describe them and to the Dockerfile that lays them, the lock to
// the hashes of every maintainer script it was reviewed against, and, when the
// build lane has assembled a base, the assembled filesystem to the table.
//
// WHAT IT CANNOT DO is run the image: the checks that would execute inside it
// (visudo -c, `id agent`, `sudo -n true` as agent, getent, systemctl is-enabled)
// need the chairman's own rootfs and a systemd, and the build lane has neither.
// Each has a structural counterpart here (the sudoers files' exact content and
// mode, the account files read against each other, every enable link and mask
// read against the units' [Install] sections), and the exec checks belong to
// the pod that boots the image.
package chairmanfixed

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Result is what a validation found.
type Result struct {
	Problems []string // sorted
	Checks   int
	Rows     int // fixed-set rows
	Locked   int // locked packages
	Note     string
}

// Validate reads bases/chairman under root and, when the assemble capability has
// laid the chairman image's OCI layout at imageDir/chairman, the image too. An
// error is something that could not be read at all.
func Validate(root, imageDir string) (Result, error) {
	s, err := load(filepath.Join(root, "bases", "chairman"))
	if err != nil {
		return Result{}, fmt.Errorf("the base's tables could not be read: %w", err)
	}
	s.sourceChecks()
	layout := filepath.Join(imageDir, "chairman")
	note := "no assembled layout at " + layout + " (the assemble stood down, or this lane assembles no base): the committed files only"
	if _, err := os.Stat(filepath.Join(layout, "index.json")); err == nil {
		hash, watch := map[string]bool{"var/lib/dbus/machine-id": true}, map[string]bool{"etc/passwd": true, "etc/group": true}
		for _, r := range s.set {
			if r.typ == "f" {
				hash[r.path] = true
			}
		}
		img, err := readImage(layout, hash, watch)
		if err != nil {
			return Result{}, fmt.Errorf("the assembled layout could not be read: %w", err)
		}
		s.layoutChecks(img)
		note = fmt.Sprintf("the assembled image (%d paths) held to fixed-set.tsv", len(img.files))
	}
	sort.Strings(s.problems)
	return Result{Problems: s.problems, Checks: s.checkCount, Rows: len(s.set), Locked: len(s.lock), Note: note}, nil
}
