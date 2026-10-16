package checks

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"
)

// THE CREDENTIALS A LANE'S API SERVER IS BORN WITH, generated per lane and never
// stored: a CA, a serving certificate for the lane's names, an RSA key pair the
// API server signs service-account tokens with, and a random admin token. They
// are throwaway by construction — the service lives for one lane and is
// reachable from nowhere else — and they are fresh every time, so two lanes
// never share a secret and a leaked run is worth nothing.
//
// STANDARD LIBRARY ONLY (crypto/x509 and friends): a dependency for this would
// be more code than it saves. TLS is ECDSA P-256; the service-account signing
// key is RSA-2048, the shape the API server's issuer reads.
type KubePKI struct {
	// CA, Cert, Key are PEM: the CA certificate, the serving certificate it signed
	// and the serving certificate's private key.
	CA, Cert, Key string
	// SAKey and SAPub are the PEM service-account signing key (PKCS#1) and its
	// public half (PKIX).
	SAKey, SAPub string
	// Token is the admin bearer token: 32 random bytes as hex.
	Token string
}

// kubeGen is the seams the PKI is built through. Production uses systemGen;
// a test replaces one at a time so every failure path is reachable.
type kubeGen struct {
	ec     func() (*ecdsa.PrivateKey, error)
	rsa    func() (*rsa.PrivateKey, error)
	rand   func(n int) ([]byte, error)
	sign   func(tmpl, parent *x509.Certificate, pub, priv any) ([]byte, error)
	ecDER  func(*ecdsa.PrivateKey) ([]byte, error)
	pubDER func(any) ([]byte, error)
}

var systemGen = kubeGen{
	ec:  func() (*ecdsa.PrivateKey, error) { return ecdsa.GenerateKey(elliptic.P256(), rand.Reader) },
	rsa: func() (*rsa.PrivateKey, error) { return rsa.GenerateKey(rand.Reader, 2048) },
	rand: func(n int) ([]byte, error) {
		b := make([]byte, n)
		_, err := rand.Read(b)
		return b, err
	},
	sign: func(tmpl, parent *x509.Certificate, pub, priv any) ([]byte, error) {
		return x509.CreateCertificate(rand.Reader, tmpl, parent, pub, priv)
	},
	ecDER:  x509.MarshalECPrivateKey,
	pubDER: x509.MarshalPKIXPublicKey,
}

// NewKubePKI builds one lane's credentials. hosts are the names the serving
// certificate carries (TestKube.Hosts); now anchors the validity window, which
// opens an hour early for clock skew between the module and the engine and runs
// a day — far past any lane's deadline, short enough to mean throwaway.
func NewKubePKI(hosts []string, now time.Time) (KubePKI, error) {
	return systemGen.build(hosts, now)
}

func (g kubeGen) build(hosts []string, now time.Time) (KubePKI, error) {
	caKey, err := g.ec()
	if err != nil {
		return KubePKI{}, fmt.Errorf("generating the CA key: %w", err)
	}
	leafKey, err := g.ec()
	if err != nil {
		return KubePKI{}, fmt.Errorf("generating the serving key: %w", err)
	}
	saKey, err := g.rsa()
	if err != nil {
		return KubePKI{}, fmt.Errorf("generating the service-account key: %w", err)
	}
	caSerial, err := g.serial()
	if err != nil {
		return KubePKI{}, err
	}
	leafSerial, err := g.serial()
	if err != nil {
		return KubePKI{}, err
	}
	token, err := g.rand(32)
	if err != nil {
		return KubePKI{}, fmt.Errorf("generating the admin token: %w", err)
	}

	window := func(t *x509.Certificate) *x509.Certificate {
		t.NotBefore, t.NotAfter = now.Add(-time.Hour), now.Add(24*time.Hour)
		return t
	}
	caTmpl := window(&x509.Certificate{
		SerialNumber:          caSerial,
		Subject:               pkix.Name{CommonName: "foundry-test-kube-ca"},
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	})
	caDER, err := g.sign(caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return KubePKI{}, fmt.Errorf("signing the CA certificate: %w", err)
	}
	leafTmpl := window(&x509.Certificate{
		SerialNumber:          leafSerial,
		Subject:               pkix.Name{CommonName: "kube-apiserver"},
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			leafTmpl.IPAddresses = append(leafTmpl.IPAddresses, ip)
		} else {
			leafTmpl.DNSNames = append(leafTmpl.DNSNames, h)
		}
	}
	leafDER, err := g.sign(leafTmpl, caTmpl, &leafKey.PublicKey, caKey)
	if err != nil {
		return KubePKI{}, fmt.Errorf("signing the serving certificate: %w", err)
	}
	leafKeyDER, err := g.ecDER(leafKey)
	if err != nil {
		return KubePKI{}, fmt.Errorf("encoding the serving key: %w", err)
	}
	saPubDER, err := g.pubDER(&saKey.PublicKey)
	if err != nil {
		return KubePKI{}, fmt.Errorf("encoding the service-account public key: %w", err)
	}
	return KubePKI{
		CA:    pemOf("CERTIFICATE", caDER),
		Cert:  pemOf("CERTIFICATE", leafDER),
		Key:   pemOf("EC PRIVATE KEY", leafKeyDER),
		SAKey: pemOf("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(saKey)),
		SAPub: pemOf("PUBLIC KEY", saPubDER),
		Token: hex.EncodeToString(token),
	}, nil
}

// serial is a random 128-bit certificate serial number.
func (g kubeGen) serial() (*big.Int, error) {
	b, err := g.rand(16)
	if err != nil {
		return nil, fmt.Errorf("generating a certificate serial: %w", err)
	}
	return new(big.Int).SetBytes(b), nil
}

func pemOf(kind string, der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}))
}

// Files is what the API server service finds in KubeFilesDir, by file name. The
// token file is the API server's static-token format: token, user, uid, groups —
// system:masters is the group RBAC's bootstrap binds to cluster-admin.
func (p KubePKI) Files() map[string]string {
	return map[string]string{
		"ca.crt":     p.CA,
		"tls.crt":    p.Cert,
		"tls.key":    p.Key,
		"sa.key":     p.SAKey,
		"sa.pub":     p.SAPub,
		"tokens.csv": p.Token + ",admin,admin,system:masters\n",
	}
}
