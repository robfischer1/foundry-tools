package castcache

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Creds are the temporary credentials STS issued. They are held in memory and
// in mounted secret files, and are never part of an error or a log line.
type Creds struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token"`
}

// unsigned are the headers a transport adds after signing.
var unsigned = map[string]bool{"authorization": true, "user-agent": true, "content-length": true, "accept-encoding": true, "connection": true}

// Sign adds AWS Signature Version 4 to req for S3. payloadHash is the hex
// SHA-256 of the body (of nothing, for a GET). Every header already on req is
// signed with host.
func Sign(req *http.Request, c Creds, payloadHash string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if c.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", c.SessionToken)
	}
	names := []string{"host"}
	values := map[string]string{"host": req.URL.Host}
	for k, v := range req.Header {
		l := strings.ToLower(k)
		if unsigned[l] {
			continue
		}
		names = append(names, l)
		values[l] = strings.Join(strings.Fields(strings.Join(v, ",")), " ")
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, n := range names {
		canonHeaders.WriteString(n + ":" + values[n] + "\n")
	}
	signed := strings.Join(names, ";")
	canonical := strings.Join([]string{req.Method, uriEncode(req.URL.Path), req.URL.RawQuery, canonHeaders.String(), signed, payloadHash}, "\n")
	scope := date + "/" + Region + "/s3/aws4_request"
	sum := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	key := mac([]byte("AWS4"+c.SecretAccessKey), date)
	for _, part := range []string{Region, "s3", "aws4_request"} {
		key = mac(key, part)
	}
	sig := hex.EncodeToString(mac(key, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.AccessKeyID+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

func mac(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(msg))
	return h.Sum(nil)
}

// uriEncode is S3's canonical path: each byte outside the unreserved set is
// percent-encoded, slashes kept.
func uriEncode(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-_.~/", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// ErrNotFound is a GET that found no object.
var ErrNotFound = errors.New("no such object")

// emptyHash is the SHA-256 of no bytes: a GET's payload.
const emptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// S3 is the one bucket's client.
type S3 struct {
	Endpoint string
	Creds    Creds
	HTTP     *http.Client
	Now      func() time.Time
}

func (s S3) url(key string) string {
	return strings.TrimRight(s.Endpoint, "/") + "/" + Bucket + "/" + key
}

func (s S3) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Get opens an object; the caller closes the body. A 404 is ErrNotFound.
func (s S3) Get(ctx context.Context, key string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url(key), nil)
	if err != nil {
		return nil, err
	}
	Sign(req, s.Creds, emptyHash, s.now())
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusOK:
		return resp, nil
	case resp.StatusCode == http.StatusNotFound:
		resp.Body.Close()
		return nil, ErrNotFound
	}
	defer resp.Body.Close()
	return nil, statusError("GET", resp)
}

// Put uploads size bytes of body, whose hex SHA-256 is sum, as one object.
func (s S3) Put(ctx context.Context, key string, body io.Reader, size int64, sum string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.url(key), body)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/gzip")
	Sign(req, s.Creds, sum, s.now())
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return statusError("PUT", resp)
	}
	return nil
}

// statusError is a refused request, with as much of the server's reason as
// fits in a log line. The request is never echoed: it carries the signature.
func statusError(verb string, resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("%s answered HTTP %d: %s", verb, resp.StatusCode, oneLine(string(raw), 160))
}

// oneLine collapses s to a single line of at most n bytes.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "…"
	}
	return s
}
