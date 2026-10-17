package visuallane

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Layer is one tar+gzip layer of the artifact: the bytes the registry stores,
// the digest of those bytes, and the digest of the tar inside them (the image
// config's diff id).
type Layer struct {
	Data   []byte
	Digest string
	DiffID string
}

// Files lists the regular files under root, relative and sorted, less
// node_modules (nothing in it is an artifact, and walking it is the slow part).
// A root that is not there holds none: a suite that wrote no results carries
// nothing.
func Files(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		switch {
		case errors.Is(err, fs.ErrNotExist) && p == root:
			return filepath.SkipAll
		case err != nil:
			return err
		case d.IsDir() && d.Name() == "node_modules":
			return filepath.SkipDir
		case d.Type().IsRegular():
			rel, _ := filepath.Rel(root, p) // p is under root, so a relative path always exists
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	slices.Sort(out)
	return out, err
}

// Snapshots keeps the files the update run's baselines are made of: whatever
// sits under a `*-snapshots` directory, less node_modules. It is the glob the
// Dagger chain filtered the suite directory by (`**/*-snapshots/**`, excluding
// `**/node_modules/**`).
func Snapshots(files []string) []string {
	var out []string
	for _, f := range files {
		segs := strings.Split(f, "/")
		if slices.Contains(segs, "node_modules") {
			continue
		}
		for _, dir := range segs[:len(segs)-1] {
			if strings.HasSuffix(dir, "-snapshots") {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// TarLayer is the files rels, read from under root, as a layer that keeps each
// at prefix/<rel>. The tar is fixed in time, owner and mode, so the same files
// are the same bytes and a re-run uploads nothing new.
func TarLayer(prefix, root string, rels []string) (Layer, error) {
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, rel := range rels {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return Layer{}, fmt.Errorf("the artifact file %s did not read: %w", rel, err)
		}
		h := &tar.Header{Name: path.Join(prefix, rel), Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		// A bytes.Buffer never fails a write, so neither does the tar over it.
		_ = tw.WriteHeader(h)
		_, _ = tw.Write(body)
	}
	_ = tw.Close()
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	_, _ = zw.Write(raw.Bytes())
	_ = zw.Close()
	diff, sum := sha256.Sum256(raw.Bytes()), sha256.Sum256(zipped.Bytes())
	return Layer{
		Data:   zipped.Bytes(),
		Digest: "sha256:" + hex.EncodeToString(sum[:]),
		DiffID: "sha256:" + hex.EncodeToString(diff[:]),
	}, nil
}
