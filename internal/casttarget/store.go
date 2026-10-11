package casttarget

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"dagger/foundry-tools/internal/castcache"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrNotFound is a GET that found no object.
var ErrNotFound = errors.New("no such object")

// newTransport is the default transport: a bounded dial and a bounded wait for
// response headers, so a store that accepts and never answers costs a timeout,
// not the cast.
func newTransport() http.RoundTripper {
	return &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		ResponseHeaderTimeout: 2 * time.Minute,
	}
}

// store is the cast-cache bucket as minio-go sees it. PATH-STYLE: SeaweedFS is
// reached by a service name, which has no wildcard DNS for <bucket>.<host>.
// The region is fixed so no GetBucketLocation call is made.
func store(endpoint string, c castcache.Creds, tr http.RoundTripper) (*minio.Client, error) {
	if endpoint == "" {
		endpoint = castcache.DefaultEndpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("the endpoint %q is not a URL", endpoint)
	}
	if tr == nil {
		tr = newTransport()
	}
	return minio.New(u.Host, &minio.Options{
		Creds:        credentials.NewStaticV4(c.AccessKeyID, c.SecretAccessKey, c.SessionToken),
		Secure:       u.Scheme == "https",
		Region:       castcache.Region,
		BucketLookup: minio.BucketLookupPath,
		Transport:    tr,
	})
}

// fetch opens an archive. The body is closed by the caller; a missing object
// is ErrNotFound.
func fetch(ctx context.Context, cl *minio.Client, key string) (*minio.Object, error) {
	obj, err := cl.GetObject(ctx, castcache.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, errText(err)
	}
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		if r := minio.ToErrorResponse(err); r.Code == "NoSuchKey" || r.StatusCode == http.StatusNotFound {
			return nil, ErrNotFound
		}
		return nil, errText(err)
	}
	return obj, nil
}

// errText is a store error as one log-sized clause: the S3 code and message
// where the server gave them. Requests carry signatures and are never echoed.
func errText(err error) error {
	if r := minio.ToErrorResponse(err); r.Code != "" {
		return fmt.Errorf("%s: %s (HTTP %d)", r.Code, oneLine(r.Message, 120), r.StatusCode)
	}
	return errors.New(oneLine(err.Error(), 200))
}

// oneLine collapses s to a single line of at most n bytes.
func oneLine(s string, n int) string {
	out := make([]byte, 0, len(s))
	space := false
	for _, b := range []byte(s) {
		if b == ' ' || b == '\n' || b == '\t' || b == '\r' {
			space = len(out) > 0
			continue
		}
		if space {
			out = append(out, ' ')
			space = false
		}
		out = append(out, b)
	}
	if len(out) > n {
		return string(out[:n]) + "…"
	}
	return string(out)
}
