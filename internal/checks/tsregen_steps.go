package checks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// THE STEPS OF ts:regen, AS PROGRAMS AND THEIR ARGUMENTS. The atom ran two
// `sh /inner.sh` containers and a diff; it now runs each step as an exec of its
// own, and these builders are the arguments of every one, so a test holds each
// to a golden. Nothing here runs anything.

// RegenGitInit makes an empty repository to fetch one commit into.
func RegenGitInit(dir string) []string { return []string{"git", "init", "--quiet", dir} }

// RegenGitFetch fetches ONE commit, shallow. The door's git answers a fetch by
// sha (measured 2026-10-16), so no history and no blobs but the commit's travel.
func RegenGitFetch(url, commit string) []string {
	return []string{"git", "fetch", "--quiet", "--depth", "1", url, commit}
}

// RegenGitShow prints one file at one commit, as regen-check.sh's `git show`.
func RegenGitShow(commit, path string) []string { return []string{"git", "show", commit + ":" + path} }

// RegenGitArchive writes the commit's tree as a tar, as regen-check.sh's
// `git archive`: the core is built from the archive, not from a checkout.
func RegenGitArchive(tar, commit string) []string {
	return []string{"git", "archive", "--format=tar", "--output=" + tar, commit}
}

// RegenTar unpacks a tar into dir: gz for a tarball, strip leading components,
// and one member when only that is wanted.
func RegenTar(file, dir string, gz bool, strip int, member string) []string {
	flags := "-xf"
	if gz {
		flags = "-xzf"
	}
	argv := []string{"tar", flags, file, "-C", dir}
	if strip > 0 {
		argv = append(argv, fmt.Sprintf("--strip-components=%d", strip))
	}
	if member != "" {
		argv = append(argv, member)
	}
	return argv
}

// RegenCopyTree copies a directory's contents into an existing one, modes and
// links kept: rustup's home, so `rustup target add` has a place it may write.
func RegenCopyTree(from, to string) []string { return []string{"cp", "-a", from + "/.", to} }

// RegenDiff compares every regenerated file with what the tree ships, byte for
// byte, less the provenance that records the diff and jco's version stamp.
func RegenDiff(gen, shipped string) []string {
	return []string{"diff", "-r", "-q", "-x", "provenance.json", "-x", ".jco-version", gen, shipped}
}

// The star's scripts name /out and /in because they ran at a container's root.
// A path is one only when it stands alone: after a space, a quote, `=` or `>`
// or at a line's start, and before `/`, a space, a quote or the line's end, so
// `/output` and `/index` are left alone.
var regenRootPath = regexp.MustCompile(`(?m)(^|[ \t"'=>])/(out|in)($|[ \t"'/])`)

// wasmToolsInstall is the one line of a star's build script that compiles
// wasm-tools, dropped when a release binary is already on PATH.
const wasmToolsInstall = `cargo install wasm-tools --locked --version "$WASM_TOOLS_VERSION"`

// RegenScript is a star's inner script laid on the directories this run owns:
// every /out is out, every /in is in. With dropInstall the exact line that
// compiles wasm-tools is dropped (a line that is not exactly it stays, and
// compiles: slower, never different). The pod's root is read-only and the
// atom is not root, so the script cannot write where it was written to.
func RegenScript(body, out, in string, dropInstall bool) string {
	const outMark, inMark = "\x00out\x00", "\x00in\x00"
	for regenRootPath.MatchString(body) {
		body = regenRootPath.ReplaceAllStringFunc(body, func(m string) string {
			sub := regenRootPath.FindStringSubmatch(m)
			mark := outMark
			if sub[2] == "in" {
				mark = inMark
			}
			return sub[1] + mark + sub[3]
		})
	}
	body = strings.ReplaceAll(strings.ReplaceAll(body, outMark, out), inMark, in)
	if !dropInstall {
		return body
	}
	lines := strings.SplitAfter(body, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if strings.TrimSuffix(l, "\n") != wasmToolsInstall {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "")
}

// RegenRelabelDiff words diff's answer as the record always has: the regenerated
// tree is /gen and the shipped one /shipped, whatever directories they are now.
func RegenRelabelDiff(out, gen, shipped string) string {
	return strings.NewReplacer(gen, "/gen", shipped, "/shipped").Replace(out)
}

// SHA256File is the digest of a file's bytes, as `sha256sum`: the way a binary
// (guest.wasm) is measured, never through a string.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// THE NODE THE TRANSPILE RUNS UNDER. The chain ran it in the node image
// (ImageNode, node 24.21.0 at its pin); a pod's container is one toolchain's, so
// the atom brings the same release as a pinned tarball and checks its sum like
// every asset here (ToolSources: the mirror first, upstream second).
const (
	Node24Version = "24.21.0"
	NodeRegenURL  = "https://nodejs.org/dist/v24.21.0/node-v24.21.0-linux-x64.tar.gz"
)

// FetchPinned writes the first source that serves bytes of the pinned sha256 to
// dest, making its directory. A source that fails or serves other bytes is
// skipped and named in the error if none answers. A nil client is the default
// with a deadline.
func FetchPinned(ctx context.Context, client *http.Client, sources []string, sum, dest string) error {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	var failed []error
	for _, src := range sources {
		err := fetchOne(ctx, client, src, sum, dest)
		if err == nil {
			return nil
		}
		failed = append(failed, fmt.Errorf("could not fetch %s: %w", src, err))
	}
	if len(failed) == 0 {
		return errors.New("no source for the asset")
	}
	return errors.Join(failed...)
}

func fetchOne(ctx context.Context, client *http.Client, src, sum, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s", resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), resp.Body)
	err = errors.Join(err, f.Close())
	if got := hex.EncodeToString(h.Sum(nil)); err == nil && got != sum {
		err = fmt.Errorf("sha256 %s, pinned %s", got, sum)
	}
	if err != nil {
		_ = os.Remove(dest)
	}
	return err
}
