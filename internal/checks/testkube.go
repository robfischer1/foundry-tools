package checks

import (
	"strconv"
	"strings"
)

// THE TREE SAYS WHICH TAG, THE LANE BRINGS THE API SERVER. The Kubernetes half
// of the contract testdb.go states for databases and brokers: a Go star whose
// *_test.go sits behind `//go:build live_kube` gets a REAL kube-apiserver over a
// REAL etcd, bound as Dagger services beside the lane, and three variables
// naming it. No test double stands in for the API server (NotDoubles): a suite
// that creates a ConfigMap, mints a ServiceAccount token and posts a TokenReview
// is exercising the server the cluster runs, not a fake of what we think it does.
//
// A SEPARATE TABLE, like TestBrokers, because nothing about it is a database
// URL or a bootstrap address: three variables name a URL, a bearer token and a
// CA file, and the lane generates the credentials. A REPO STILL HAS NO SAY — the
// tree states the tag, this table is the fleet's closed vocabulary.
type TestKube struct {
	// Tag is the `//go:build` tag the suite sits behind, and the -tags word.
	Tag string
	// URLEnv, TokenEnv and CAEnv are the variables the suite reads: the API
	// server's https URL, an admin bearer token, and the path of the CA file the
	// serving certificate verifies against.
	URLEnv, TokenEnv, CAEnv string
	// Alias is the API server's hostname inside the lane container; EtcdAlias is
	// the datastore's, as the API server dials it.
	Alias, EtcdAlias string
}

// TestKubes is the vocabulary, in binding order.
var TestKubes = []TestKube{{
	Tag: "live_kube", URLEnv: "TEST_KUBE_URL", TokenEnv: "TEST_KUBE_TOKEN", CAEnv: "TEST_KUBE_CA_FILE",
	Alias: "kube", EtcdAlias: "kube-etcd",
}}

// The ports, the places the files go, and the bound on the wait.
const (
	// KubeAPIPort is the API server's secure port; KubeEtcdPort is etcd's client
	// port. Both are exposed on their service.
	KubeAPIPort  = 6443
	KubeEtcdPort = 2379
	// KubeFilesDir is where the API server service finds its credentials.
	KubeFilesDir = "/k"
	// KubeAuthHeaderFile is the file the lane container finds the admin
	// token's header line in, for curl's -H @file.
	KubeAuthHeaderFile = KubeFilesDir + "/auth-header"
	// KubeCAFile is where the lane container finds the CA the serving certificate
	// verifies against; TEST_KUBE_CA_FILE names it.
	KubeCAFile = "/run/test-kube/ca.crt"
	// KubeBootSeconds bounds the wait for /readyz. The databases carry no bound of
	// their own — a Postgres is ready when its port answers — so this is the
	// kube path's own: an API server boots in seconds on this engine and a
	// minute-plus is a service that is not coming. A lane that reaches it fails
	// loudly, naming the service, instead of hanging to the silence limit.
	KubeBootSeconds = 120
)

// AliasFor is the API server's hostname for one LANE, on TestDB.AliasFor's rule
// (testdb.go carries the measurements): the empty scope is the shared binding,
// a named scope gets a host of its own. A separate alias is not enough alone —
// the caller perturbs both service definitions too (atoms_go.go).
func (k TestKube) AliasFor(scope string) string {
	if scope == "" {
		return k.Alias
	}
	return k.Alias + "-" + scope
}

// EtcdAliasFor is the datastore's hostname for one lane, on the same rule. The
// API server advertises nothing of its own to etcd; it dials this name, so a
// scoped lane's etcd is as private as its API server.
func (k TestKube) EtcdAliasFor(scope string) string {
	if scope == "" {
		return k.EtcdAlias
	}
	return k.EtcdAlias + "-" + scope
}

// URLFor is the https URL the suite is handed for one lane's binding.
func (k TestKube) URLFor(scope string) string {
	return "https://" + k.AliasFor(scope) + ":" + strconv.Itoa(KubeAPIPort)
}

// Hosts are the names the serving certificate carries: the lane's alias, and
// the two ways a suite or an operator reaches a port-forward.
func (k TestKube) Hosts(scope string) []string {
	return []string{k.AliasFor(scope), "localhost", "127.0.0.1"}
}

// EtcdArgs is etcd's start command. IT LIVES HERE FOR TestBroker.StartArgs' REASON:
// the caller builds a Dagger container no unit test can inspect, and this is a
// []string one can. The caller starts it through the entrypoint
// (WithDefaultArgs + AsService(UseEntrypoint)), never WithExec — a WithExec
// service never reads ready (StartArgs carries the measurement).
//
// NOT DURABLE: the service lives for one lane and holds nothing anyone reads
// afterwards, so --unsafe-no-fsync buys the speed TestDB.ServerArgs buys the
// databases. The advertised URL is the alias the API server dials.
func (k TestKube) EtcdArgs(scope string) []string {
	return []string{
		"etcd",
		"--data-dir=/tmp/etcd",
		"--listen-client-urls=http://0.0.0.0:" + strconv.Itoa(KubeEtcdPort),
		"--advertise-client-urls=http://" + k.EtcdAliasFor(scope) + ":" + strconv.Itoa(KubeEtcdPort),
		"--unsafe-no-fsync",
	}
}

// APIServerArgs is kube-apiserver's start command: static-token authentication
// (the admin token in tokens.csv, group system:masters, which RBAC's bootstrap
// binds to cluster-admin), RBAC, and the service-account issuer and keys that
// let the server mint and review tokens. The cluster IP range is the API
// server's own requirement; nothing here routes.
func (k TestKube) APIServerArgs(scope string) []string {
	return []string{
		"kube-apiserver",
		"--etcd-servers=http://" + k.EtcdAliasFor(scope) + ":" + strconv.Itoa(KubeEtcdPort),
		"--secure-port=" + strconv.Itoa(KubeAPIPort),
		"--bind-address=0.0.0.0",
		"--tls-cert-file=" + KubeFilesDir + "/tls.crt",
		"--tls-private-key-file=" + KubeFilesDir + "/tls.key",
		"--token-auth-file=" + KubeFilesDir + "/tokens.csv",
		"--authorization-mode=RBAC",
		"--service-account-issuer=https://kubernetes.default.svc",
		"--service-account-key-file=" + KubeFilesDir + "/sa.pub",
		"--service-account-signing-key-file=" + KubeFilesDir + "/sa.key",
		"--api-audiences=https://kubernetes.default.svc",
		"--service-cluster-ip-range=10.0.0.0/24",
	}
}

// KubeService is the name the lane's failures give the API server.
const KubeService = "kube-apiserver"

// LaneName is the name a lane's failures give a scope: the empty scope is the
// shared binding.
func LaneName(scope string) string {
	if scope == "" {
		return "shared"
	}
	return scope
}

// NotReady is the line a lane that reaches KubeBootSeconds prints: it names the
// service and the lane, so the failure reads without the argv.
func (k TestKube) NotReady(scope string) string {
	return KubeService + " (lane " + LaneName(scope) + ") not ready after KubeBootSeconds=" +
		strconv.Itoa(KubeBootSeconds) + "s: GET /readyz"
}

// WaitArgs is the exec that holds the lane until the API server answers
// GET /readyz with 200 under the admin token, bounded by KubeBootSeconds. A
// service that is up (its port answers) is not a service that is ready: the
// API server accepts connections well before its post-start hooks, the RBAC
// bootstrap among them, have run, and a suite that starts then fails on a
// Forbidden that is gone a second later reads as a flake.
//
// NO SHELL (runtime.go rule 7): the retry loop is curl's own. --fail turns the
// 500s /readyz answers while it boots into failures, and --retry-all-errors with
// --retry-connrefused retries them, a second apart, until the bound. Past it the
// exec fails — curl exits 22 for a status, 7 for a refused connection, 35 or 60
// for a handshake that never verified — and curl's own %{onerror} write-out
// prints NotReady on that failure alone, which the engine's error carries.
//
// NO CREDENTIAL ON ARGV: the bearer header is read from a file the lane
// mounted (-H @file, KubeAuthHeaderFile), so the engine's error, which carries
// the whole argv, never carries the token.
func (k TestKube) WaitArgs(scope string) []string {
	bound := strconv.Itoa(KubeBootSeconds)
	return []string{
		"curl", "--fail", "--silent", "--show-error", "--output", "/dev/null",
		"--retry", bound, "--retry-delay", "1", "--retry-max-time", bound,
		"--retry-all-errors", "--retry-connrefused",
		"--connect-timeout", "5", "--max-time", "10",
		"--cacert", KubeCAFile,
		"-H", "@" + KubeAuthHeaderFile,
		"--write-out", "%{onerror}" + k.NotReady(scope) + "\n",
		k.URLFor(scope) + "/readyz",
	}
}

// MissingEnv names, in the vocabulary's order, the variables getenv answers
// empty for: the ones the lane did not bind.
func (k TestKube) MissingEnv(getenv func(string) string) []string {
	var out []string
	for _, name := range []string{k.URLEnv, k.TokenEnv, k.CAEnv} {
		if getenv(name) == "" {
			out = append(out, name)
		}
	}
	return out
}

// NotBound is the CANNOT RUN detail for a tree that carries k's tag on a lane
// that did not bind its API server. The pod path is that lane today: it binds
// the databases and the broker as sidecars and no kube-apiserver.
func (k TestKube) NotBound(missing []string) string {
	return k.Tag + ": " + strings.Join(missing, ", ") +
		" not bound on this lane (the pod path binds one for an atom whose Needs name service:etcd and service:kube-apiserver; see atoms/go/test-race Needs)"
}

// TestKubesFor is the vocabulary's entries for the tags a tree carries, in
// binding order.
func TestKubesFor(tags []string) []TestKube {
	has := map[string]bool{}
	for _, t := range tags {
		has[t] = true
	}
	var out []TestKube
	for _, k := range TestKubes {
		if has[k.Tag] {
			out = append(out, k)
		}
	}
	return out
}

// TestKubeScope is the line an atom prints about the API servers it brought,
// printed EITHER WAY on TestDBScope's rule: a lane that bound nothing says why.
func TestKubeScope(kubes []TestKube, why string) string {
	if len(kubes) == 0 {
		return "test kubes: none — " + why
	}
	parts := make([]string, 0, len(kubes))
	for _, k := range kubes {
		parts = append(parts, k.Tag+" → "+k.URLEnv+", "+k.TokenEnv+", "+k.CAEnv)
	}
	return "test kubes: " + strings.Join(parts, ", ")
}

// SelectKubes answers which API servers the lane binds and the scope line to
// print, from the tree's `//go:build` read (the grep's stdout and exit code).
// THE DECISION IS PURE, THE I/O IS THE CALLER'S — SelectBrokers' split, for its
// reason: a guard inside the Dagger method is one no unit test can execute.
//
// grep exits 1 for no match, which is an ANSWER; 2 and up is grep failing, and a
// negative code is the read that could not start. Neither is the same fact as an
// empty read, and the line says which.
func SelectKubes(tags string, code int) ([]TestKube, string) {
	if code < 0 || code > 1 {
		return nil, TestKubeScope(nil, "the tree's build tags could not be read")
	}
	kubes := TestKubesFor(GoBuildTags(tags))
	if len(kubes) == 0 {
		return nil, TestKubeScope(nil, "no test file sits behind a tag the fleet names")
	}
	return kubes, TestKubeScope(kubes, "")
}
