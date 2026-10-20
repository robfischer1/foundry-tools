package chairmanfixed

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// entry is one path of the merged filesystem.
type entry struct {
	typ      byte // 'f', 'l', 'd'
	mode     int
	uid, gid int
	link     string
	sha      string
}

// image is the merged filesystem of an assembled image, read from its OCI
// layout without running anything in it.
type image struct {
	files map[string]*entry
	// firstSeen holds the content of the watched files in the first layer that
	// carries them: the FROM image's own.
	firstSeen map[string]string
	// final holds their content in the last.
	final map[string]string
}

type ociIndex struct {
	Manifests []struct {
		Digest string `json:"digest"`
	} `json:"manifests"`
}

type ociManifest struct {
	Layers []struct {
		Digest string `json:"digest"`
	} `json:"layers"`
}

func blobPath(dir, digest string) string {
	return filepath.Join(dir, "blobs", strings.Replace(digest, ":", "/", 1))
}

func readJSON(p string, v any) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// readImage merges the layers of the one image in the layout at dir. It hashes
// the files in hash (paths of the fixed set) and keeps the content of watch.
func readImage(dir string, hash, watch map[string]bool) (*image, error) {
	var idx ociIndex
	if err := readJSON(filepath.Join(dir, "index.json"), &idx); err != nil || len(idx.Manifests) != 1 {
		return nil, fmt.Errorf("the layout at %s does not hold exactly one image (%v)", dir, err)
	}
	var m ociManifest
	if err := readJSON(blobPath(dir, idx.Manifests[0].Digest), &m); err != nil {
		return nil, err
	}
	img := &image{files: map[string]*entry{}, firstSeen: map[string]string{}, final: map[string]string{}}
	for _, l := range m.Layers {
		if err := img.layer(blobPath(dir, l.Digest), hash, watch); err != nil {
			return nil, fmt.Errorf("layer %s: %w", l.Digest, err)
		}
	}
	return img, nil
}

func (img *image) remove(name string) {
	for k := range img.files {
		if k == name || strings.HasPrefix(k, name+"/") {
			delete(img.files, k)
		}
	}
}

func (img *image) layer(p string, hash, watch map[string]bool) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 1<<20)
	var r io.Reader = br
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			return err
		}
		defer zr.Close()
		r = zr
	}
	tr := tar.NewReader(r)
	seenHere := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(strings.TrimPrefix(path.Clean("/"+h.Name), "/"), "/")
		if name == "" {
			continue
		}
		dir, base := path.Split(name)
		dir = strings.TrimSuffix(dir, "/")
		if strings.HasPrefix(base, ".wh.") {
			if base == ".wh..wh..opq" {
				for k := range img.files {
					if strings.HasPrefix(k, dir+"/") {
						delete(img.files, k)
					}
				}
			} else {
				img.remove(path.Join(dir, strings.TrimPrefix(base, ".wh.")))
			}
			continue
		}
		e := &entry{mode: int(h.Mode) & 0o7777, uid: h.Uid, gid: h.Gid}
		switch h.Typeflag {
		case tar.TypeDir:
			e.typ = 'd'
		case tar.TypeSymlink:
			e.typ, e.link = 'l', h.Linkname
		case tar.TypeLink:
			if t := img.files[strings.TrimPrefix(path.Clean("/"+h.Linkname), "/")]; t != nil {
				c := *t
				c.mode, c.uid, c.gid = e.mode, e.uid, e.gid
				e = &c
			} else {
				e.typ = 'f'
			}
		case tar.TypeReg:
			e.typ = 'f'
			if hash[name] || watch[name] {
				body, err := io.ReadAll(tr)
				if err != nil {
					return err
				}
				sum := sha256.Sum256(body)
				e.sha = hex.EncodeToString(sum[:])
				if watch[name] {
					seenHere[name] = string(body)
				}
			}
		default:
			continue
		}
		img.files[name] = e
		// a layer need not carry the directories above a path
		for d := path.Dir(name); d != "." && d != "/"; d = path.Dir(d) {
			if img.files[d] == nil {
				img.files[d] = &entry{typ: 'd', mode: 0o755}
			}
		}
	}
	for n, c := range seenHere {
		if _, ok := img.firstSeen[n]; !ok {
			img.firstSeen[n] = c
		}
		img.final[n] = c
	}
	return nil
}

// resolve follows the symlinks of p (and of every directory above it) inside the
// image. It answers the path it ends at and whether that exists; a target that
// leaves the image or loops does not.
func (img *image) resolve(p string, depth int) (string, bool) {
	if depth > 40 {
		return "", false
	}
	parts := strings.FieldsFunc(path.Clean("/"+p), func(r rune) bool { return r == '/' })
	cur := ""
	for i, part := range parts {
		next := path.Join(cur, part)
		e := img.files[next]
		if e == nil {
			return next, false
		}
		if e.typ == 'l' {
			target := e.link
			if !strings.HasPrefix(target, "/") {
				target = path.Join("/"+cur, target)
			}
			rest := strings.Join(parts[i+1:], "/")
			return img.resolve(path.Join(target, rest), depth+1)
		}
		cur = next
	}
	return cur, true
}

// danglingOK are the link targets that exist only at run time: the volume the
// agent's settings are poured into, the cerberus hooks chairman-seed copies, and
// the null device a mask points at.
var danglingOK = []string{"/home/agent/.claude/", "/etc/cerberus/", "/dev/null"}

func okDangling(target string) bool {
	for _, p := range danglingOK {
		if strings.HasPrefix(target, p) || target == strings.TrimSuffix(p, "/") {
			return true
		}
	}
	return false
}

// layoutChecks holds the assembled image to fixed-set.tsv.
func (s *source) layoutChecks(img *image) {
	s.checkCount++
	for _, r := range s.set {
		e := img.files[r.path]
		if r.typ == "x" {
			if e != nil {
				s.problem("image: %s must be gone and is there", r.path)
			}
			continue
		}
		if e == nil {
			s.problem("image: %s is missing", r.path)
			continue
		}
		if string(e.typ) != r.typ {
			s.problem("image: %s is a %c, fixed-set.tsv says %s", r.path, e.typ, r.typ)
			continue
		}
		if e.typ != 'l' && e.mode != r.mode {
			s.problem("image: %s is %04o, fixed-set.tsv says %04o", r.path, e.mode, r.mode)
		}
		if e.uid != r.uid || e.gid != r.gid {
			s.problem("image: %s is owned by %d:%d, fixed-set.tsv says %d:%d", r.path, e.uid, e.gid, r.uid, r.gid)
		}
		switch e.typ {
		case 'l':
			if e.link != r.val {
				s.problem("image: %s links to %s, fixed-set.tsv says %s", r.path, e.link, r.val)
			}
		case 'f':
			if e.sha != r.val {
				s.problem("image: %s is not the file fixed-set.tsv records (sha256 %.12s, want %.12s)", r.path, e.sha, r.val)
			}
		}
	}
	// every link of the set resolves, or is one that is made at run time
	s.checkCount++
	for _, r := range s.set {
		e := img.files[r.path]
		if r.typ != "l" || e == nil || e.typ != 'l' {
			continue
		}
		target := e.link
		if !strings.HasPrefix(target, "/") {
			target = path.Join("/"+path.Dir(r.path), target)
		}
		if _, ok := img.resolve(target, 0); !ok && !okDangling(target) {
			s.problem("image: %s links to %s, which is not in the image", r.path, e.link)
		}
	}
	// the account files keep every line of the FROM's
	s.checkCount++
	for _, f := range []string{"etc/passwd", "etc/group"} {
		have := map[string]bool{}
		for _, l := range strings.Split(img.final[f], "\n") {
			have[l] = true
		}
		var lost []string
		for _, l := range strings.Split(img.firstSeen[f], "\n") {
			if l != "" && !have[l] {
				lost = append(lost, strings.SplitN(l, ":", 2)[0])
			}
		}
		sort.Strings(lost)
		if len(lost) > 0 {
			s.problem("image: /%s lost the base's lines for %s: refresh fixed/root/%s from the new FROM", f, strings.Join(lost, ", "), f)
		}
	}
	// a machine id is generated at first boot
	s.checkCount++
	for _, p := range []string{"etc/machine-id", "var/lib/dbus/machine-id"} {
		if e := img.files[p]; e != nil && e.typ == 'f' && e.sha != hex.EncodeToString(emptySum[:]) {
			s.problem("image: /%s holds an id", p)
		}
	}
}

var emptySum = sha256.Sum256(nil)
