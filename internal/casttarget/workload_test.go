package casttarget

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/proto/spiffe/workload"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// workloadFake is a SPIRE agent's Workload API, answering JWT-SVID requests.
type workloadFake struct {
	workload.UnimplementedSpiffeWorkloadAPIServer
	id, token string
	refuse    bool
	audiences [][]string
}

func (w *workloadFake) FetchJWTSVID(_ context.Context, req *workload.JWTSVIDRequest) (*workload.JWTSVIDResponse, error) {
	w.audiences = append(w.audiences, req.Audience)
	if w.refuse {
		return nil, status.Error(codes.PermissionDenied, "no identity is registered for this workload")
	}
	return &workload.JWTSVIDResponse{Svids: []*workload.JWTSVID{{SpiffeId: w.id, Svid: w.token}}}, nil
}

// unsignedJWT is a JWT-SVID go-spiffe parses (it does not verify the signature
// of a token the agent hands it) for the given subject and audience.
func unsignedJWT(t *testing.T, sub, aud string) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	sig := base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	return enc(map[string]string{"alg": "ES256", "typ": "JWT"}) + "." +
		enc(map[string]any{"sub": sub, "aud": []string{aud}, "exp": time.Now().Add(time.Hour).Unix()}) + "." + sig
}

// agent serves w on a unix socket and answers its address.
func agent(t *testing.T, w *workloadFake) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "wl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	workload.RegisterSpiffeWorkloadAPIServer(srv, w)
	go srv.Serve(ln)
	t.Cleanup(srv.Stop)
	return "unix://" + sock
}

// The real JWT source asks the agent for audience seaweedfs and answers the
// token and the SPIFFE id as the agent issued them.
func TestFetchJWTAsksTheAgentForTheSeaweedFSAudience(t *testing.T) {
	const id = "spiffe://notusmi.com/ci/prime/ca-cast"
	token := unsignedJWT(t, id, "seaweedfs")
	w := &workloadFake{id: id, token: token}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, gotID, err := FetchJWT(ctx, agent(t, w), "seaweedfs")
	if err != nil || got != token || gotID != id {
		t.Fatalf("token %q id %q error %v", got, gotID, err)
	}
	if len(w.audiences) != 1 || len(w.audiences[0]) != 1 || w.audiences[0][0] != "seaweedfs" {
		t.Errorf("the agent was asked for audiences %v", w.audiences)
	}
}

func TestFetchJWTRefusedOrUnreachableIsAnErrorWithNoToken(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	refusing := agent(t, &workloadFake{refuse: true})
	for name, socket := range map[string]string{"the agent refuses": refusing, "no agent": "unix:///nonexistent/agent.sock"} {
		token, id, err := FetchJWT(ctx, socket, "seaweedfs")
		if err == nil || token != "" || id != "" {
			t.Errorf("%s: token %q id %q error %v", name, token, id, err)
		}
	}
	if _, _, err := FetchJWT(ctx, refusing, "seaweedfs"); err == nil || !strings.Contains(err.Error(), "no identity is registered") {
		t.Errorf("the agent's reason was lost: %v", err)
	}
}
