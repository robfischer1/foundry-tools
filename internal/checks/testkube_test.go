package checks

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

// THE VOCABULARY IS CLOSED, and has exactly the one row the fleet's kube-gated
// suites sit behind. The names are the contract with every suite that reads
// them, so they are pinned whole.
func TestTestKubesIsTheClosedVocabulary(t *testing.T) {
	want := []TestKube{{
		Tag: "live_kube", URLEnv: "TEST_KUBE_URL", TokenEnv: "TEST_KUBE_TOKEN", CAEnv: "TEST_KUBE_CA_FILE",
		Alias: "kube", EtcdAlias: "kube-etcd",
	}}
	if !reflect.DeepEqual(TestKubes, want) {
		t.Fatalf("TestKubes = %+v, want %+v", TestKubes, want)
	}
}

// THE SCOPE RULE IS TestDB's, word for word: the empty scope is the shared
// binding, a named scope gets a host of its own — for BOTH services, because
// the API server and its etcd are two content-addressed definitions a second
// lane would otherwise share.
func TestKubeAliasesFollowTheScopeRule(t *testing.T) {
	k := TestKubes[0]
	for _, c := range []struct{ scope, api, etcd string }{
		{"", "kube", "kube-etcd"},
		{"mutation", "kube-mutation", "kube-etcd-mutation"},
		{"diffcov", "kube-diffcov", "kube-etcd-diffcov"},
	} {
		if got := k.AliasFor(c.scope); got != c.api {
			t.Errorf("AliasFor(%q) = %q, want %q", c.scope, got, c.api)
		}
		if got := k.EtcdAliasFor(c.scope); got != c.etcd {
			t.Errorf("EtcdAliasFor(%q) = %q, want %q", c.scope, got, c.etcd)
		}
	}
}

// THE URL IS HTTPS ON THE API PORT of the scope's alias: a suite handed the
// wrong scheme or port would fail as a TLS error, which reads as a flake.
func TestKubeURLIsHTTPSOnTheAPIPort(t *testing.T) {
	k := TestKubes[0]
	if got := k.URLFor(""); got != "https://kube:6443" {
		t.Errorf("URLFor(\"\") = %q", got)
	}
	if got := k.URLFor("mutation"); got != "https://kube-mutation:6443" {
		t.Errorf("URLFor(mutation) = %q", got)
	}
}

func TestTestKubesForOverTagSets(t *testing.T) {
	for _, c := range []struct {
		name string
		tags []string
		want int
	}{
		{"none", nil, 0},
		{"only other tags", []string{"integration", "live_db", "live_kafka"}, 0},
		{"the tag", []string{"live_kube"}, 1},
		{"among others", []string{"integration", "live_db", "live_kube"}, 1},
		{"a near miss is not the tag", []string{"live_kubernetes", "kube", "live_kube2"}, 0},
	} {
		if got := TestKubesFor(c.tags); len(got) != c.want {
			t.Errorf("%s: TestKubesFor(%v) = %d kubes, want %d", c.name, c.tags, len(got), c.want)
		}
	}
	if got := TestKubesFor([]string{"live_kube"}); got[0].Tag != "live_kube" {
		t.Errorf("TestKubesFor returned %+v", got[0])
	}
}

// live_kube IS COMPILED WHEN PRESENT: BuildTags carries it, and UncompiledTags
// no longer names it — the scope line that called a compiled suite uncompiled
// is the exact shape testdb.go exists to end.
func TestLiveKubeIsCompiledNotUncompiled(t *testing.T) {
	tags := []string{"integration", "live_db", "live_kafka", "live_kube"}
	dbs := TestDBsFor(tags)
	brokers := TestBrokersFor(tags)
	kubes := TestKubesFor(tags)
	if got := BuildTags(dbs, brokers, nil, kubes); got != "live_db,live_kafka,live_kube" {
		t.Errorf("BuildTags = %q", got)
	}
	if got := BuildTags(nil, nil, nil, kubes); got != "live_kube" {
		t.Errorf("BuildTags(kubes only) = %q", got)
	}
	if got := BuildTags(nil, nil, nil, nil); got != "" {
		t.Errorf("BuildTags(nothing) = %q", got)
	}
	if got := UncompiledTags(tags, dbs, brokers, nil, kubes); !reflect.DeepEqual(got, []string{"integration"}) {
		t.Errorf("UncompiledTags = %v, want only the tag no vocabulary names", got)
	}
	// Told nothing about the kubes found, it is the VOCABULARY the caller passes
	// (as it does for brokers), and the tag is still named when no vocabulary row
	// is handed over.
	if got := UncompiledTags([]string{"live_kube"}, nil, nil, nil, nil); !reflect.DeepEqual(got, []string{"live_kube"}) {
		t.Errorf("UncompiledTags with an empty vocabulary = %v", got)
	}
}

// THE SCOPE LINE IS PRINTED EITHER WAY.
func TestTestKubeScopeIsPrintedEitherWay(t *testing.T) {
	none := TestKubeScope(nil, "no test file sits behind live_kube")
	if none != "test kubes: none — no test file sits behind live_kube" {
		t.Errorf("none = %q", none)
	}
	some := TestKubeScope(TestKubes, "")
	if some != "test kubes: live_kube → TEST_KUBE_URL, TEST_KUBE_TOKEN, TEST_KUBE_CA_FILE" {
		t.Errorf("some = %q", some)
	}
}

// THE DECISION IS A TABLE: a refused read, an empty read and a tagged tree each
// say which they are, and grep's exit 1 is an answer, not a failure.
func TestSelectKubes(t *testing.T) {
	for _, c := range []struct {
		name  string
		tags  string
		code  int
		kubes int
		line  string
	}{
		{"tagged", "//go:build live_db\n//go:build live_kube\n", 0, 1, "test kubes: live_kube → TEST_KUBE_URL, TEST_KUBE_TOKEN, TEST_KUBE_CA_FILE"},
		{"no match is an answer", "", 1, 0, "test kubes: none — no test file sits behind a tag the fleet names"},
		{"other tags only", "//go:build live_db\n", 0, 0, "test kubes: none — no test file sits behind a tag the fleet names"},
		{"grep failed", "", 2, 0, "test kubes: none — the tree's build tags could not be read"},
		{"could not start", "", -1, 0, "test kubes: none — the tree's build tags could not be read"},
		{"a tag read despite a failed grep is not trusted", "//go:build live_kube\n", 2, 0, "test kubes: none — the tree's build tags could not be read"},
	} {
		kubes, line := SelectKubes(c.tags, c.code)
		if len(kubes) != c.kubes || line != c.line {
			t.Errorf("%s: SelectKubes = %d kubes, %q; want %d, %q", c.name, len(kubes), line, c.kubes, c.line)
		}
	}
}

// ETCD'S ARGV IS THE CONTRACT. It advertises the alias the API server dials,
// and it is not durable: the service lives for one lane and holds nothing.
func TestEtcdArgs(t *testing.T) {
	k := TestKubes[0]
	want := []string{
		"etcd", "--data-dir=/tmp/etcd",
		"--listen-client-urls=http://0.0.0.0:2379",
		"--advertise-client-urls=http://kube-etcd-mutation:2379",
		"--unsafe-no-fsync",
	}
	if got := k.EtcdArgs("mutation"); !reflect.DeepEqual(got, want) {
		t.Errorf("EtcdArgs = %v, want %v", got, want)
	}
	if got := k.EtcdArgs(""); got[3] != "--advertise-client-urls=http://kube-etcd:2379" {
		t.Errorf("shared advertise = %q", got[3])
	}
}

func TestAPIServerArgs(t *testing.T) {
	k := TestKubes[0]
	want := []string{
		"kube-apiserver",
		"--etcd-servers=http://kube-etcd-mutation:2379",
		"--secure-port=6443",
		"--bind-address=0.0.0.0",
		"--tls-cert-file=/k/tls.crt",
		"--tls-private-key-file=/k/tls.key",
		"--token-auth-file=/k/tokens.csv",
		"--authorization-mode=RBAC",
		"--service-account-issuer=https://kubernetes.default.svc",
		"--service-account-key-file=/k/sa.pub",
		"--service-account-signing-key-file=/k/sa.key",
		"--api-audiences=https://kubernetes.default.svc",
		"--service-cluster-ip-range=10.0.0.0/24",
	}
	if got := k.APIServerArgs("mutation"); !reflect.DeepEqual(got, want) {
		t.Errorf("APIServerArgs = %v, want %v", got, want)
	}
	if got := k.APIServerArgs(""); got[1] != "--etcd-servers=http://kube-etcd:2379" {
		t.Errorf("shared etcd server = %q", got[1])
	}
}

// THE FILES THE API SERVER IS HANDED are exactly the six its argv names.
func TestKubeFilesNamesWhatTheArgvReads(t *testing.T) {
	p, err := NewKubePKI(TestKubes[0].Hosts(""), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	files := p.Files()
	for _, name := range []string{"ca.crt", "tls.crt", "tls.key", "sa.key", "sa.pub", "tokens.csv"} {
		if files[name] == "" {
			t.Errorf("file %s is missing or empty", name)
		}
	}
	if len(files) != 6 {
		t.Errorf("files = %d, want 6", len(files))
	}
	argv := strings.Join(TestKubes[0].APIServerArgs(""), " ")
	for _, name := range []string{"tls.crt", "tls.key", "tokens.csv", "sa.pub", "sa.key"} {
		if !strings.Contains(argv, KubeFilesDir+"/"+name) {
			t.Errorf("argv does not read %s/%s", KubeFilesDir, name)
		}
	}
	if KubeCAFile != "/run/test-kube/ca.crt" {
		t.Errorf("KubeCAFile = %q", KubeCAFile)
	}
}

func TestKubeHostsAreTheScopesAliasLocalhostAndLoopback(t *testing.T) {
	if got := TestKubes[0].Hosts("mutation"); !reflect.DeepEqual(got, []string{"kube-mutation", "localhost", "127.0.0.1"}) {
		t.Errorf("Hosts = %v", got)
	}
}

func TestKubePinsMatchTheClusterRelease(t *testing.T) {
	if !strings.HasPrefix(ImageKubeAPIServer, "registry.k8s.io/kube-apiserver:v1.36.3@sha256:") {
		t.Errorf("ImageKubeAPIServer = %q", ImageKubeAPIServer)
	}
	// 3.6.8-0 is DefaultEtcdVersion in kubernetes@v1.36.3 kubeadm constants.
	if !strings.HasPrefix(ImageEtcd, "registry.k8s.io/etcd:3.6.8-0@sha256:") {
		t.Errorf("ImageEtcd = %q", ImageEtcd)
	}
	for _, img := range []string{ImageKubeAPIServer, ImageEtcd} {
		if !containsString(LaneImages, img) {
			t.Errorf("LaneImages does not list %s", img)
		}
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ---- THE WAIT -----------------------------------------------------------

// THE LANE WAITS FOR /readyz, and the wait is bounded and shell-free: curl's own
// retry loop. The bearer header is read from a mounted file (-H @file), so no
// credential is on argv, and a timeout prints, through curl's own %{onerror},
// the line that names the service and the lane.
func TestKubeWaitArgsAreBoundedShellFreeAndKeepTheCredentialOffArgv(t *testing.T) {
	k := TestKubes[0]
	got := k.WaitArgs("mutation")
	want := []string{
		"curl", "--fail", "--silent", "--show-error", "--output", "/dev/null",
		"--retry", "120", "--retry-delay", "1", "--retry-max-time", "120",
		"--retry-all-errors", "--retry-connrefused",
		"--connect-timeout", "5", "--max-time", "10",
		"--cacert", "/run/test-kube/ca.crt",
		"-H", "@/k/auth-header",
		"--write-out", "%{onerror}kube-apiserver (lane mutation) not ready after KubeBootSeconds=120s: GET /readyz\n",
		"https://kube-mutation:6443/readyz",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("WaitArgs = %v\nwant %v", got, want)
	}
	if got[0] == "sh" || got[0] == "bash" {
		t.Error("the wait is a shell")
	}
	for _, a := range got {
		if strings.Contains(a, "Bearer") {
			t.Errorf("a credential is on argv: %q", a)
		}
	}
	if KubeBootSeconds != 120 {
		t.Errorf("KubeBootSeconds = %d", KubeBootSeconds)
	}
	if KubeAuthHeaderFile != KubeFilesDir+"/auth-header" {
		t.Errorf("KubeAuthHeaderFile = %q", KubeAuthHeaderFile)
	}
	shared := k.WaitArgs("")
	if shared[len(shared)-1] != "https://kube:6443/readyz" || !strings.Contains(strings.Join(shared, " "), "(lane shared)") {
		t.Errorf("the shared wait = %v", shared)
	}
}

// THE HEADER FILE is curl's header line for the admin token, and nothing else.
func TestKubeAuthHeaderIsTheBearerLine(t *testing.T) {
	p := KubePKI{Token: "tok123"}
	if got := p.AuthHeader(); got != "Authorization: Bearer tok123\n" {
		t.Errorf("AuthHeader = %q", got)
	}
}

// ---- WHAT THE ATOM IS HANDED ---------------------------------------------

// MissingEnv names, in the vocabulary's order, the variables a suite environment
// leaves empty; the refusal's detail is one line that names them.
func TestKubeMissingEnvAndTheRefusalDetail(t *testing.T) {
	k := TestKubes[0]
	env := map[string]string{"TEST_KUBE_URL": "u", "TEST_KUBE_CA_FILE": ""}
	got := k.MissingEnv(func(n string) string { return env[n] })
	if !reflect.DeepEqual(got, []string{"TEST_KUBE_TOKEN", "TEST_KUBE_CA_FILE"}) {
		t.Errorf("MissingEnv = %v", got)
	}
	if got := k.MissingEnv(func(string) string { return "x" }); len(got) != 0 {
		t.Errorf("MissingEnv with all bound = %v", got)
	}
	want := "live_kube: TEST_KUBE_TOKEN, TEST_KUBE_CA_FILE not bound on this lane (the pod path binds one for an atom whose Needs name service:etcd and service:kube-apiserver; see atoms/go/test-race Needs)"
	if got := k.NotBound([]string{"TEST_KUBE_TOKEN", "TEST_KUBE_CA_FILE"}); got != want {
		t.Errorf("NotBound = %q", got)
	}
}

// ---- THE PKI ------------------------------------------------------------

func mustPEM(t *testing.T, s, kind string) []byte {
	t.Helper()
	b, rest := pem.Decode([]byte(s))
	if b == nil || b.Type != kind || len(strings.TrimSpace(string(rest))) != 0 {
		t.Fatalf("want one %s PEM block, got %q", kind, s)
	}
	return b.Bytes
}

// THE SERVING CERTIFICATE VERIFIES AGAINST THE CA, for each name the suite and
// the lane use, and only as a server certificate.
func TestKubePKIServingCertVerifiesForEveryHost(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	hosts := TestKubes[0].Hosts("mutation")
	p, err := NewKubePKI(hosts, now)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(mustPEM(t, p.CA, "CERTIFICATE"))
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(mustPEM(t, p.Cert, "CERTIFICATE"))
	if err != nil {
		t.Fatal(err)
	}
	if !ca.IsCA || !ca.BasicConstraintsValid || ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Errorf("the CA is not a CA: %+v", ca)
	}
	if leaf.IsCA {
		t.Error("the serving certificate is a CA")
	}
	if !reflect.DeepEqual(leaf.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}) {
		t.Errorf("ExtKeyUsage = %v", leaf.ExtKeyUsage)
	}
	if leaf.KeyUsage != x509.KeyUsageDigitalSignature {
		t.Errorf("KeyUsage = %v", leaf.KeyUsage)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	for _, name := range []string{"kube-mutation", "localhost", "127.0.0.1"} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: name, CurrentTime: now}); err != nil {
			t.Errorf("verify for %s: %v", name, err)
		}
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "kube", CurrentTime: now}); err == nil {
		t.Error("verified for a name the certificate does not carry")
	}
	if len(leaf.DNSNames) != 2 || len(leaf.IPAddresses) != 1 || !leaf.IPAddresses[0].Equal(net.ParseIP("127.0.0.1")) {
		t.Errorf("SANs = %v %v", leaf.DNSNames, leaf.IPAddresses)
	}
	// valid now, not valid before the hour of skew allowance and not for long
	if !leaf.NotBefore.Before(now) || !leaf.NotAfter.After(now.Add(12*time.Hour)) || leaf.NotAfter.After(now.Add(48*time.Hour)) {
		t.Errorf("validity %v..%v around %v", leaf.NotBefore, leaf.NotAfter, now)
	}
	if !ca.NotBefore.Before(now) || !ca.NotAfter.After(now.Add(12*time.Hour)) {
		t.Errorf("CA validity %v..%v", ca.NotBefore, ca.NotAfter)
	}
	if _, ok := leaf.PublicKey.(*ecdsa.PublicKey); !ok {
		t.Errorf("TLS key is %T, want ECDSA", leaf.PublicKey)
	}
	// the key file is the certificate's key
	if _, err := tls.X509KeyPair([]byte(p.Cert), []byte(p.Key)); err != nil {
		t.Errorf("tls key does not match its certificate: %v", err)
	}
	if k, _ := leaf.PublicKey.(*ecdsa.PublicKey); k == nil || k.Curve.Params().Name != "P-256" {
		t.Errorf("curve is not P-256")
	}
}

// THE SERVICE-ACCOUNT KEYS ARE AN RSA-2048 PAIR the API server can read back,
// and the public half is the private half's.
func TestKubePKIServiceAccountKeys(t *testing.T) {
	p, err := NewKubePKI(TestKubes[0].Hosts(""), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	priv, err := x509.ParsePKCS1PrivateKey(mustPEM(t, p.SAKey, "RSA PRIVATE KEY"))
	if err != nil {
		t.Fatal(err)
	}
	if priv.N.BitLen() != 2048 {
		t.Errorf("SA key is %d bits", priv.N.BitLen())
	}
	pub, err := x509.ParsePKIXPublicKey(mustPEM(t, p.SAPub, "PUBLIC KEY"))
	if err != nil {
		t.Fatal(err)
	}
	rp, ok := pub.(*rsa.PublicKey)
	if !ok || !rp.Equal(&priv.PublicKey) {
		t.Errorf("sa.pub is not sa.key's public half")
	}
}

// THE TOKEN IS 32 RANDOM BYTES AS HEX, different every build, and the token
// file grants it the admin identity in system:masters — the one line the API
// server's static-token format reads.
func TestKubePKITokenAndTokensFile(t *testing.T) {
	a, err := NewKubePKI(TestKubes[0].Hosts(""), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewKubePKI(TestKubes[0].Hosts(""), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Token) != 64 || strings.Trim(a.Token, "0123456789abcdef") != "" {
		t.Errorf("token = %q", a.Token)
	}
	if a.Token == b.Token || a.Key == b.Key || a.SAKey == b.SAKey || a.CA == b.CA {
		t.Error("two builds shared secret material")
	}
	if got := a.Files()["tokens.csv"]; got != a.Token+",admin,admin,system:masters\n" {
		t.Errorf("tokens.csv = %q", got)
	}
	files := a.Files()
	if files["ca.crt"] != a.CA || files["tls.crt"] != a.Cert || files["tls.key"] != a.Key ||
		files["sa.key"] != a.SAKey || files["sa.pub"] != a.SAPub {
		t.Error("Files does not map the fields to their names")
	}
}

// THE ENTROPY IS ASKED FOR AT THE SIZES THE FORMATS NEED: a 128-bit serial for
// each certificate and a 256-bit token. Asserted on the requests, not on random
// output, which could not tell 15 bytes from 16.
func TestKubePKIAsksForTheRightAmountOfEntropy(t *testing.T) {
	var asked []int
	g := systemGen
	g.rand = func(n int) ([]byte, error) {
		asked = append(asked, n)
		return systemGen.rand(n)
	}
	if _, err := g.build(TestKubes[0].Hosts(""), time.Now()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(asked, []int{16, 16, 32}) {
		t.Errorf("entropy requests = %v, want [16 16 32]", asked)
	}
}

// EVERY STEP THAT CAN FAIL FAILS LOUDLY: the entropy, the keys, the signatures
// are injected so each failure is reachable. A half-built PKI is never returned.
func TestKubePKIFailureAtEveryStep(t *testing.T) {
	boom := errors.New("boom")
	for _, c := range []struct {
		name string
		gen  func() kubeGen
		want string
	}{
		{"ca key", func() kubeGen { g := systemGen; g.ec = failNth(1, g.ec); return g }, "CA key"},
		{"serving key", func() kubeGen { g := systemGen; g.ec = failNth(2, g.ec); return g }, "serving key"},
		{"sa key", func() kubeGen { g := systemGen; g.rsa = func() (*rsa.PrivateKey, error) { return nil, boom }; return g }, "service-account key"},
		{"serial", func() kubeGen { g := systemGen; g.rand = failRandNth(1, g.rand); return g }, "serial"},
		{"second serial", func() kubeGen { g := systemGen; g.rand = failRandNth(2, g.rand); return g }, "serial"},
		{"token", func() kubeGen { g := systemGen; g.rand = failRandNth(3, g.rand); return g }, "token"},
		{"ca signature", func() kubeGen { g := systemGen; g.sign = failSignNth(1, g.sign); return g }, "CA certificate"},
		{"serving signature", func() kubeGen { g := systemGen; g.sign = failSignNth(2, g.sign); return g }, "serving certificate"},
		{"key encoding", func() kubeGen {
			g := systemGen
			g.ecDER = func(*ecdsa.PrivateKey) ([]byte, error) { return nil, boom }
			return g
		}, "encoding the serving key"},
		{"public key encoding", func() kubeGen {
			g := systemGen
			g.pubDER = func(any) ([]byte, error) { return nil, boom }
			return g
		}, "encoding the service-account public key"},
	} {
		p, err := c.gen().build(TestKubes[0].Hosts(""), time.Now())
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want one containing %q", c.name, err, c.want)
		}
		if p != (KubePKI{}) {
			t.Errorf("%s: a half-built PKI was returned: %+v", c.name, p)
		}
	}
}

func failNth(n int, real func() (*ecdsa.PrivateKey, error)) func() (*ecdsa.PrivateKey, error) {
	i := 0
	return func() (*ecdsa.PrivateKey, error) {
		i++
		if i == n {
			return nil, errors.New("boom")
		}
		return real()
	}
}

func failRandNth(n int, real func(int) ([]byte, error)) func(int) ([]byte, error) {
	i := 0
	return func(size int) ([]byte, error) {
		i++
		if i == n {
			return nil, errors.New("boom")
		}
		return real(size)
	}
}

func failSignNth(n int, real func(tmpl, parent *x509.Certificate, pub, priv any) ([]byte, error)) func(tmpl, parent *x509.Certificate, pub, priv any) ([]byte, error) {
	i := 0
	return func(tmpl, parent *x509.Certificate, pub, priv any) ([]byte, error) {
		i++
		if i == n {
			return nil, errors.New("boom")
		}
		return real(tmpl, parent, pub, priv)
	}
}
