package casttarget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"dagger/foundry-tools/internal/castcache"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FetchJWT asks the SPIRE agent at socket for a JWT-SVID of the given
// audience, answering the token and the SPIFFE id it names. A variable so a
// test can hand over a token without an agent.
var FetchJWT = func(ctx context.Context, socket, audience string) (string, string, error) {
	svid, err := workloadapi.FetchJWTSVID(ctx, jwtsvid.Params{Audience: audience}, workloadapi.WithAddr(socket))
	if err != nil {
		return "", "", err
	}
	return svid.Marshal(), svid.ID.String(), nil
}

// retryEvery is how often a refused or unreachable agent is asked again.
var retryEvery = time.Second

// fetchJWTWaiting asks until ctx ends while the agent answers "not yet".
//
// THE CAST ASKS BEFORE ITS IDENTITY EXISTS. The ca-cast entry is per pod: the
// SPIRE controller registers it after the cast's pod is created, and the node
// agent learns it at its next sync. A warm cast reaches this ask about 3 s
// after its pod starts (2026-10-11, cast-cerberus-c486581: asked 22:55:06,
// pod 22:55:03) and the agent answers PermissionDenied "no identity issued" at
// once; a cold one asks ~45 s in and finds the entry there. So PermissionDenied
// and Unavailable are asked again until the wait runs out; anything else is
// final at once.
func fetchJWTWaiting(ctx context.Context, socket string) (string, string, error) {
	for {
		token, sub, err := FetchJWT(ctx, socket, castcache.Audience)
		if err == nil {
			return token, sub, nil
		}
		if c := status.Code(err); c != codes.PermissionDenied && c != codes.Unavailable {
			return "", "", err
		}
		select {
		case <-ctx.Done():
			return "", "", err
		case <-time.After(retryEvery):
		}
	}
}

// newProvider is minio-go's web-identity provider; a seam, because its only
// error (a nil token getter) cannot be provoked from here.
var newProvider = credentials.NewSTSWebIdentity

// CredsConfig is one credential fetch.
type CredsConfig struct {
	Socket   string // the SPIRE agent's workload socket
	Endpoint string // SeaweedFS' S3 port, which serves STS
	Wait     time.Duration
	HTTP     *http.Client
}

// FetchCreds trades the caller's JWT-SVID (audience seaweedfs) for temporary
// credentials with STS AssumeRoleWithWebIdentity on RoleARN, through minio-go's
// web-identity provider, and answers them with the SPIFFE id that asked. No
// error carries the token or a credential.
func FetchCreds(ctx context.Context, c CredsConfig) (castcache.Creds, string, error) {
	if c.Wait <= 0 {
		c.Wait = 30 * time.Second
	}
	if path, ok := strings.CutPrefix(c.Socket, "unix://"); ok {
		// A socket this process may not open would otherwise cost the whole
		// wait: go-spiffe retries a refused connect silently (hadescall.reach).
		conn, err := net.Dial("unix", path)
		if err != nil {
			return castcache.Creds{}, "", fmt.Errorf("the SPIRE socket cannot be opened: %v", err)
		}
		conn.Close()
	}
	endpoint := endpointOf(c.Endpoint)
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	var id string
	getter := func() (*credentials.WebIdentityToken, error) {
		wctx, cancel := context.WithTimeout(ctx, c.Wait)
		defer cancel()
		token, sub, err := fetchJWTWaiting(wctx, c.Socket)
		if err != nil {
			return nil, fmt.Errorf("no JWT-SVID for audience %s within %s: %v", castcache.Audience, c.Wait, err)
		}
		id = sub
		return &credentials.WebIdentityToken{Token: token, Expiry: 3600}, nil
	}
	provider, err := newProvider(endpoint, getter, func(i *credentials.STSWebIdentity) {
		i.RoleARN = castcache.RoleARN
		i.Client = client
	})
	if err != nil {
		return castcache.Creds{}, "", err
	}
	v, err := provider.GetWithContext(&credentials.CredContext{Client: client, Context: ctx})
	if err != nil {
		return castcache.Creds{}, id, stsError(err)
	}
	if v.AccessKeyID == "" || v.SecretAccessKey == "" {
		return castcache.Creds{}, id, errors.New("STS answered with no credentials")
	}
	return castcache.Creds{AccessKeyID: v.AccessKeyID, SecretAccessKey: v.SecretAccessKey, SessionToken: v.SessionToken}, id, nil
}

// stsError names a failed exchange: the role, and the STS code and message
// where the server gave them. The JWT-SVID getter's own error passes through.
func stsError(err error) error {
	var r credentials.ErrorResponse
	switch {
	case errors.As(err, &r):
		return fmt.Errorf("STS AssumeRoleWithWebIdentity on %s refused: %s: %s", castcache.RoleARN, r.STSError.Code, oneLine(r.STSError.Message, 160))
	case strings.HasPrefix(err.Error(), "no JWT-SVID"):
		return err
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return fmt.Errorf("STS AssumeRoleWithWebIdentity on %s failed: %s", castcache.RoleARN, oneLine(err.Error(), 160))
}

// WriteCreds leaves the credentials at path as JSON, for the module to read as
// a file (never stdout, which dagger logs) and hand on as secrets.
func WriteCreds(path string, c castcache.Creds) error {
	// A struct of three strings always marshals.
	raw, _ := json.Marshal(c)
	return os.WriteFile(path, raw, 0o600)
}

// secretFiles are the three files the archive container mounts, in the order
// ReadCreds reads them. A variable so a test can point at its own.
var secretFiles = [3]string{castcache.AccessKeyFile, castcache.SecretKeyFile, castcache.TokenFile}

// ReadCreds loads the three secret files the archive container mounts.
func ReadCreds() (castcache.Creds, error) {
	var v [3]string
	for i, p := range secretFiles {
		b, err := os.ReadFile(p)
		if err != nil {
			return castcache.Creds{}, fmt.Errorf("credentials: %v", err)
		}
		v[i] = strings.TrimSpace(string(b))
	}
	return castcache.Creds{AccessKeyID: v[0], SecretAccessKey: v[1], SessionToken: v[2]}, nil
}
