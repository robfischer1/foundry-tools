package castcache

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
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

// CredsConfig is one credential fetch.
type CredsConfig struct {
	Socket   string // the SPIRE agent's workload socket
	Endpoint string // SeaweedFS' S3 port, which serves STS
	Session  string // the role session's name
	Wait     time.Duration
	HTTP     *http.Client
}

// FetchCreds trades the caller's JWT-SVID (audience seaweedfs) for temporary
// credentials with STS AssumeRoleWithWebIdentity on RoleARN, and answers them
// with the SPIFFE id that asked. No error carries the token or a credential.
func FetchCreds(ctx context.Context, c CredsConfig) (Creds, string, error) {
	if c.Wait <= 0 {
		c.Wait = 30 * time.Second
	}
	if path, ok := strings.CutPrefix(c.Socket, "unix://"); ok {
		// A socket this process may not open would otherwise cost the whole
		// wait: go-spiffe retries a refused connect silently (hadescall.reach).
		conn, err := net.Dial("unix", path)
		if err != nil {
			return Creds{}, "", fmt.Errorf("the SPIRE socket cannot be opened: %v", err)
		}
		conn.Close()
	}
	wctx, cancel := context.WithTimeout(ctx, c.Wait)
	defer cancel()
	token, id, err := FetchJWT(wctx, c.Socket, Audience)
	if err != nil {
		return Creds{}, "", fmt.Errorf("no JWT-SVID for audience %s within %s: %v", Audience, c.Wait, err)
	}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	form := url.Values{
		"Action":           {"AssumeRoleWithWebIdentity"},
		"Version":          {"2011-06-15"},
		"RoleArn":          {RoleARN},
		"RoleSessionName":  {c.Session},
		"WebIdentityToken": {token},
		"DurationSeconds":  {"3600"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/", strings.NewReader(form.Encode()))
	if err != nil {
		return Creds{}, id, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return Creds{}, id, fmt.Errorf("STS at %s did not answer: %v", endpoint, urlless(err))
	}
	defer resp.Body.Close()
	creds, err := parseSTS(resp)
	return creds, id, err
}

// urlless is an error without the request URL a *url.Error repeats.
func urlless(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

type stsAnswer struct {
	Creds struct {
		AccessKeyID     string `xml:"AccessKeyId"`
		SecretAccessKey string
		SessionToken    string
	} `xml:"AssumeRoleWithWebIdentityResult>Credentials"`
	Error struct{ Code, Message string }
}

// parseSTS reads an AssumeRoleWithWebIdentity answer.
func parseSTS(resp *http.Response) (Creds, error) {
	var a stsAnswer
	_ = xml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&a)
	if resp.StatusCode/100 != 2 {
		why := a.Error.Code
		if a.Error.Message != "" {
			why += ": " + a.Error.Message
		}
		return Creds{}, fmt.Errorf("STS AssumeRoleWithWebIdentity on %s answered HTTP %d %s", RoleARN, resp.StatusCode, oneLine(why, 160))
	}
	c := Creds{AccessKeyID: a.Creds.AccessKeyID, SecretAccessKey: a.Creds.SecretAccessKey, SessionToken: a.Creds.SessionToken}
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return Creds{}, errors.New("STS answered HTTP 200 with no credentials")
	}
	return c, nil
}

// WriteCreds leaves the credentials at path as JSON, for the module to read as
// a file (never stdout, which dagger logs) and hand on as secrets.
func WriteCreds(path string, c Creds) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

// ReadCreds loads the three secret files the archive container mounts.
func ReadCreds() (Creds, error) {
	read := func(p string) (string, error) {
		b, err := os.ReadFile(p)
		return strings.TrimSpace(string(b)), err
	}
	var c Creds
	var err error
	if c.AccessKeyID, err = read(AccessKeyFile); err != nil {
		return c, fmt.Errorf("credentials: %v", err)
	}
	if c.SecretAccessKey, err = read(SecretKeyFile); err != nil {
		return c, fmt.Errorf("credentials: %v", err)
	}
	if c.SessionToken, err = read(TokenFile); err != nil {
		return c, fmt.Errorf("credentials: %v", err)
	}
	return c, nil
}
