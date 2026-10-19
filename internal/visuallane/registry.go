package visuallane

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// THE ARTIFACT'S PUSH. The Dagger chain handed the engine a rootfs and let it
// publish; a pod has no engine, so the atom speaks the registry's API itself:
// each blob, then the manifest that names them. It is the distribution spec's
// monolithic upload and nothing more.
//
// THE CREDENTIAL IS ONE OF TWO. The pod's JWT-SVID rides as a bearer token on
// every call (zot's token endpoint refuses a Basic login that carries it; flux
// foundry/zot.yaml, "a CI lane presents the SVID as a bearer token directly").
// A static username and password, which the Dagger runner is still handed as
// --artifact-auth, answers the registry's Bearer challenge instead: the
// password goes to the realm for a token and never to the API.

const (
	mediaManifest = "application/vnd.oci.image.manifest.v1+json"
	mediaConfig   = "application/vnd.oci.image.config.v1+json"
	mediaLayer    = "application/vnd.oci.image.layer.v1.tar+gzip"
)

// Credential is how a push proves who it is. Bearer, when set, is sent as the
// token; otherwise User and Password answer the registry's challenge.
type Credential struct {
	Bearer, User, Password string
}

// Registry is a registry reached at Base ("https://registry.notusmi.com").
type Registry struct {
	Base string
	HTTP *http.Client
	Cred Credential
}

// ParseRef splits "host/repo/path:tag" into its parts.
func ParseRef(ref string) (host, repo, tag string, err error) {
	slash := strings.Index(ref, "/")
	colon := strings.LastIndex(ref, ":")
	if slash < 0 || colon < slash || colon == len(ref)-1 {
		return "", "", "", fmt.Errorf("the artifact reference %q is not host/repository:tag", ref)
	}
	return ref[:slash], ref[slash+1 : colon], ref[colon+1:], nil
}

// PushRef is Push to the repository and tag a "host/repo:tag" reference names;
// the host is the registry's and is not read again.
func (r Registry) PushRef(ctx context.Context, ref string, layers []Layer) error {
	_, repo, tag, err := ParseRef(ref)
	if err != nil {
		return err
	}
	return r.Push(ctx, repo, tag, layers)
}

// Push stores layers as an image at repo:tag. The blobs go first and the
// manifest last, so a push that fails part-way leaves no tag behind.
func (r Registry) Push(ctx context.Context, repo, tag string, layers []Layer) error {
	c := &session{Registry: r}
	cfg := imageConfig(layers)
	cfgSum := sha256.Sum256(cfg)
	cfgDigest := "sha256:" + hex.EncodeToString(cfgSum[:])
	if err := c.blob(ctx, repo, cfgDigest, cfg); err != nil {
		return err
	}
	descs := make([]descriptor, 0, len(layers))
	for _, l := range layers {
		if err := c.blob(ctx, repo, l.Digest, l.Data); err != nil {
			return err
		}
		descs = append(descs, descriptor{MediaType: mediaLayer, Digest: l.Digest, Size: len(l.Data)})
	}
	// A struct of strings and integers always encodes.
	manifest, _ := json.Marshal(imageManifest{
		SchemaVersion: 2, MediaType: mediaManifest,
		Config: descriptor{MediaType: mediaConfig, Digest: cfgDigest, Size: len(cfg)}, Layers: descs,
	})
	resp, err := c.do(ctx, http.MethodPut, r.Base+"/v2/"+repo+"/manifests/"+tag, mediaManifest, manifest)
	if err != nil {
		return err
	}
	return finish(resp, http.StatusCreated)
}

type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int    `json:"size"`
}

type imageManifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        descriptor   `json:"config"`
	Layers        []descriptor `json:"layers"`
}

// imageConfig is the minimal image config the layers' diff ids make: an image
// registries and clients accept, with nothing to run.
func imageConfig(layers []Layer) []byte {
	diffs := make([]string, 0, len(layers))
	for _, l := range layers {
		diffs = append(diffs, l.DiffID)
	}
	// A map of strings always encodes.
	b, _ := json.Marshal(map[string]any{
		"architecture": "amd64", "os": "linux", "config": map[string]any{},
		"rootfs": map[string]any{"type": "layers", "diff_ids": diffs},
	})
	return b
}

// session is one push's state: the registry, and the bearer token a Basic
// credential earned from the realm.
type session struct {
	Registry
	token string
}

// blob stores body at digest unless the registry holds it already.
func (s *session) blob(ctx context.Context, repo, digest string, body []byte) error {
	head, err := s.do(ctx, http.MethodHead, s.Base+"/v2/"+repo+"/blobs/"+digest, "", nil)
	if err != nil {
		return err
	}
	if head.StatusCode == http.StatusOK {
		_ = head.Body.Close()
		return nil
	}
	_ = head.Body.Close()
	if head.StatusCode != http.StatusNotFound {
		return fmt.Errorf("HEAD blob %s: %s", digest, head.Status)
	}
	start, err := s.do(ctx, http.MethodPost, s.Base+"/v2/"+repo+"/blobs/uploads/", "", nil)
	if err != nil {
		return err
	}
	loc := start.Header.Get("Location")
	if err := finish(start, http.StatusAccepted); err != nil {
		return err
	}
	to, err := s.upload(start.Request.URL, loc, digest)
	if err != nil {
		return err
	}
	put, err := s.do(ctx, http.MethodPut, to, "application/octet-stream", body)
	if err != nil {
		return err
	}
	return finish(put, http.StatusCreated)
}

// upload is the URL the blob is put to: the session's Location, resolved
// against the request that opened it, with the digest added.
func (s *session) upload(from *url.URL, loc, digest string) (string, error) {
	u, err := from.Parse(loc)
	if err != nil {
		return "", fmt.Errorf("the registry's upload location %q is not a URL: %w", loc, err)
	}
	q := u.Query()
	q.Set("digest", digest)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// do sends one request. A 401 that carries a Bearer challenge is answered once
// by asking the realm for a token with the Basic credential; the request is
// then sent again.
func (s *session) do(ctx context.Context, method, target, contentType string, body []byte) (*http.Response, error) {
	resp, err := s.send(ctx, method, target, contentType, body)
	if err != nil || resp.StatusCode != http.StatusUnauthorized || s.Cred.Bearer != "" || s.Cred.User == "" || s.token != "" {
		return resp, err
	}
	challenge := resp.Header.Get("Www-Authenticate")
	_ = resp.Body.Close()
	if s.token, err = s.realmToken(ctx, challenge); err != nil {
		return nil, err
	}
	return s.send(ctx, method, target, contentType, body)
}

func (s *session) send(ctx context.Context, method, target, contentType string, body []byte) (*http.Response, error) {
	// A target the code built from a parsed reference is always a URL.
	req, _ := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	switch {
	case s.Cred.Bearer != "":
		req.Header.Set("Authorization", "Bearer "+s.Cred.Bearer)
	case s.token != "":
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, target, err)
	}
	return resp, nil
}

// realmToken trades the Basic credential for a bearer token at the realm the
// challenge names.
func (s *session) realmToken(ctx context.Context, challenge string) (string, error) {
	params := challengeParams(challenge)
	realm := params["realm"]
	if realm == "" {
		return "", errors.New("the registry refused the push with 401 and named no realm to log in at")
	}
	u, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("the registry's realm %q is not a URL: %w", realm, err)
	}
	q := u.Query()
	for _, k := range []string{"service", "scope"} {
		if v := params[k]; v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(s.Cred.User+":"+s.Cred.Password)))
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", realm, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the registry's realm refused the login: %s", resp.Status)
	}
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("the registry's realm answered no token: %w", err)
	}
	if t := cmp.Or(tok.Token, tok.AccessToken); t != "" {
		return t, nil
	}
	return "", errors.New("the registry's realm answered no token")
}

// challengeParams reads the key="value" pairs of a `Bearer realm="…",service="…"`
// challenge.
func challengeParams(h string) map[string]string {
	out := map[string]string{}
	_, rest, _ := strings.Cut(h, " ")
	for _, part := range strings.Split(rest, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok {
			out[k] = strings.Trim(v, `"`)
		}
	}
	return out
}

// finish closes a response and says what it was if it was not want.
func finish(resp *http.Response, want int) error {
	defer resp.Body.Close()
	if resp.StatusCode == want {
		return nil
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	return fmt.Errorf("%s %s: %s %s", resp.Request.Method, resp.Request.URL.Path, resp.Status, strings.TrimSpace(string(msg)))
}
