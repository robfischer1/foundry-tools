package atomkit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Marshal is the one JSON encoder (lld.md E1): HTML escaping off, fields in
// struct order, map keys sorted, and exactly one trailing newline.
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteFile writes Marshal(v) to path atomically: a temp file beside it,
// fsynced, then renamed over it. A failure leaves no temp file behind.
func WriteFile(path string, v any) (err error) {
	b, err := Marshal(v)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer func() {
		if err != nil {
			os.Remove(tmp)
		}
	}()
	_, werr := f.Write(b)
	serr := f.Sync()
	err = errors.Join(werr, serr, f.Close())
	if err == nil {
		err = os.Rename(tmp, filepath.Clean(path))
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// VersionError is a wire file whose version this build does not understand.
type VersionError struct{ Got int }

func (e *VersionError) Error() string { return fmt.Sprintf("version %d", e.Got) }

// ReadRunPlan reads run-plan@1. Unknown fields are tolerated (E1); a version
// other than 1 is a *VersionError naming the file.
func ReadRunPlan(path string) (RunPlan, error) {
	var p RunPlan
	if err := readJSON(path, &p); err != nil {
		return RunPlan{}, err
	}
	return p, checkVersion(path, p.Version)
}

// ReadContainerPlan reads plan@1.
func ReadContainerPlan(path string) (ContainerPlan, error) {
	var p ContainerPlan
	if err := readJSON(path, &p); err != nil {
		return ContainerPlan{}, err
	}
	return p, checkVersion(path, p.Version)
}

// ReadTree reads tree@1.
func ReadTree(path string) (Tree, error) {
	var t Tree
	if err := readJSON(path, &t); err != nil {
		return Tree{}, err
	}
	return t, checkVersion(path, t.Version)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func checkVersion(path string, got int) error {
	if got != 1 {
		return fmt.Errorf("%s: %w", path, &VersionError{Got: got})
	}
	return nil
}

// EscapeID makes an atom id safe as a file name (lld.md E4): every ":" becomes
// "__", so fleet:check-merge-conflict is fleet__check-merge-conflict.
func EscapeID(id string) string { return strings.ReplaceAll(id, ":", "__") }

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func errOf(s string) error {
	if s == "" {
		return nil
	}
	return errors.New(s)
}
