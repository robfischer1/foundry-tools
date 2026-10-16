package pinsync

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The python lock: pytools/requirements.in names what the atoms' venv carries,
// pytools/requirements.txt is uv's hashed lock of it. A requirement pinned with
// `==` whose package also has a `// renovate: datasource=pypi` constant in
// internal/checks (copier: checks.CopierVersion) is held to that constant, and
// the lock is regenerated with the command its own header records.

var (
	pinLine    = regexp.MustCompile(`^([A-Za-z0-9._-]+)==([^\s#;]+)(.*)$`)
	lockHeader = regexp.MustCompile(`(?m)^#\s+(uv pip compile .*)$`)
	nameRun    = regexp.MustCompile(`[-_.]+`)
)

// pythonPins moves each coupled pin in requirements.in (into out) and answers
// the lock regeneration to run after out is written, or nil when the lock
// already pins every coupled requirement at its constant.
func (e *Env) pythonPins(src *goSource, out map[string][]byte) (func(context.Context) error, error) {
	dir := filepath.Join(e.Root, "pytools")
	inPath, lockPath := filepath.Join(dir, "requirements.in"), filepath.Join(dir, "requirements.txt")
	in, err := os.ReadFile(inPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lock, err := os.ReadFile(lockPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(in), "\n")
	stale := e.Relock
	var held []string // name==version lines the lock must carry
	for i, line := range lines {
		m := pinLine.FindStringSubmatch(line)
		name, coupled := "", false
		if m != nil {
			name, coupled = src.byMarker("pypi", m[1])
		}
		if !coupled {
			continue
		}
		want, err := src.eval(name)
		if err != nil {
			return nil, err
		}
		lines[i] = m[1] + "==" + want + m[3]
		pin := normalize(m[1]) + "==" + want
		held = append(held, pin)
		stale = stale || lines[i] != line || !lockHolds(lock, pin)
	}
	if !stale {
		return nil, nil
	}
	if moved := strings.Join(lines, "\n"); moved != string(in) {
		out[filepath.Join("pytools", "requirements.in")] = []byte(moved)
	}
	h := lockHeader.FindStringSubmatch(string(lock))
	if h == nil {
		return nil, fmt.Errorf("%s records no `uv pip compile` command to regenerate it with", lockPath)
	}
	return func(ctx context.Context) error {
		fmt.Fprintf(e.Log, "pinsync: regenerating the lock: %s\n", h[1])
		// uv reads the -o file it is about to overwrite as preferences, hashes
		// included, so a version edited in place keeps the hashes of the one it
		// replaced: renovate's pip_requirements bumped pydantic-core 2.50.0 ->
		// 2.50.1 in rollup 85a79c7 under 2.50.0's hashes, this regeneration
		// kept them, and the lock failed to install (fixed by hand in #447).
		// Stripping the hashes keeps the versions as preferences and makes uv
		// fetch every hash fresh.
		err := os.WriteFile(lockPath, stripHashes(lock), 0o644)
		if err == nil {
			err = e.RunLock(ctx, dir, strings.Fields(h[1]))
		}
		if err != nil {
			return fmt.Errorf("regenerating the lock (%s): %w", h[1], err)
		}
		lock, err := os.ReadFile(lockPath)
		if err != nil {
			return err
		}
		for _, pin := range held {
			if !lockHolds(lock, pin) {
				return fmt.Errorf("the regenerated lock does not hold %s", pin)
			}
		}
		return nil
	}, nil
}

// hashLine is one `--hash=` continuation of a requirement, with the ` \`
// that joined it to the line before.
var hashLine = regexp.MustCompile(` \\\n[ \t]+--hash=[a-z0-9]+:[0-9a-f]+`)

// stripHashes drops every --hash continuation from a uv-compiled lock, leaving
// each requirement on its own line with its comments.
func stripHashes(lock []byte) []byte { return hashLine.ReplaceAll(lock, nil) }

// lockHolds says whether the lock pins exactly this name==version.
func lockHolds(lock []byte, pin string) bool {
	return strings.Contains("\n"+string(lock), "\n"+pin+" \\\n")
}

// normalize is the name a uv lock spells a requirement with (PEP 503, lower
// case, runs of -_. as -).
func normalize(name string) string {
	return strings.ToLower(nameRun.ReplaceAllString(name, "-"))
}
