package bundlekit

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/atoms"
)

// copyTree copies the regular files and directories under src into dst; skip,
// when it answers true for a slash path relative to src, leaves that file out
// (a directory it answers true for is left out whole).
func copyTree(src, dst string, skip func(rel string) bool) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		rel = filepath.ToSlash(rel)
		if rel != "." && skip != nil && skip(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			b, err := os.ReadFile(p)
			if err == nil {
				err = osWrite(target, b, 0o644)
			}
			return err
		}
		return nil // links and devices are not bundle data
	})
}

// writeFile writes body at root/rel, making its directories.
func writeFile(root, rel, body string) error {
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return osWrite(p, []byte(body), 0o644)
}

// FleetExcluded is what the fleet die leaves out of the staged fleet/ (bundle.go
// fleet): the retired slag files, the star rows (derived now) and the catalog
// entries, which reach the die as the staged declared/data.json instead.
func FleetExcluded(rel string) bool {
	switch {
	case strings.HasSuffix(rel, ".slag"):
		return true
	case rel == "stars" || strings.HasPrefix(rel, "stars/"):
		return true
	}
	dir, file := path.Split(rel)
	return dir == "repos/" && strings.HasSuffix(file, ".json")
}

// readGlob reads every file a pattern matches under tree, path -> body.
func readGlob(t atoms.Tree, pattern string) (map[string]string, *Fault) {
	out := map[string]string{}
	for _, p := range t.Glob(pattern) {
		body, err := t.Read(p)
		if err != nil {
			return nil, CouldNotRun("%s could not be read: %v", p, err)
		}
		out[p] = body
	}
	return out, nil
}
