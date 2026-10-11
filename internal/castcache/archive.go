package castcache

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// skipped reports whether Pack leaves rel out. Cargo's incremental directory
// (<profile>/incremental, <triple>/<profile>/incremental) is large, is not
// reused by a release build that sets incremental off, and is rebuilt on
// demand; the .cargo-lock files are locks, not data.
func skipped(rel string, dir bool) bool {
	base := path.Base(rel)
	if dir && base == "incremental" && strings.Count(rel, "/") <= 2 {
		return true
	}
	return !dir && base == ".cargo-lock"
}

// Pack writes dir as a gzipped tar (fastest level: the archive is rebuilt every
// cast) and answers how many regular files it carried. Names are relative to
// dir. Only directories, regular files and symlinks are carried.
func Pack(dir string, w io.Writer) (int, error) {
	gz, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
	if err != nil {
		return 0, err
	}
	tw := tar.NewWriter(gz)
	files := 0
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if skipped(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		if !mode.IsDir() && !mode.IsRegular() && mode&fs.ModeSymlink == 0 {
			return nil
		}
		link := ""
		if mode&fs.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		h, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		h.Name = rel
		if mode.IsDir() {
			h.Name += "/"
		}
		h.Uid, h.Gid, h.Uname, h.Gname = 0, 0, "", ""
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if !mode.IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(tw, f); err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		files++
		return nil
	})
	if err != nil {
		return files, err
	}
	if err := tw.Close(); err != nil {
		return files, err
	}
	return files, gz.Close()
}

// Unpack lays an archive Pack wrote under dir and answers how many regular
// files it wrote. A name that would land outside dir, or through a symlink
// that leads outside it, is refused. The whole gzip stream is read to its
// checksum, so a truncated download is an error rather than a short target.
func Unpack(r io.Reader, dir string) (int, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, err
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return 0, err
	}
	tr := tar.NewReader(gz)
	files := 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return files, err
		}
		name := path.Clean(h.Name)
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return files, fmt.Errorf("archive entry %q leaves the target", h.Name)
		}
		dst := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return files, err
		}
		if real, err := filepath.EvalSymlinks(filepath.Dir(dst)); err != nil || !within(root, real) {
			return files, fmt.Errorf("archive entry %q resolves outside the target", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(dst, fs.FileMode(h.Mode).Perm()|0o700)
		case tar.TypeSymlink:
			err = os.Symlink(h.Linkname, dst)
		case tar.TypeReg:
			if err = writeFile(dst, tr, fs.FileMode(h.Mode).Perm()); err == nil {
				err = os.Chtimes(dst, h.ModTime, h.ModTime)
				files++
			}
		}
		if err != nil {
			return files, err
		}
	}
	_, err = io.Copy(io.Discard, gz)
	return files, err
}

func within(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

func writeFile(dst string, r io.Reader, mode fs.FileMode) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
