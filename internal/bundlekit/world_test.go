package bundlekit

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/checks"
)

const (
	sha     = "0123456789abcdef0123456789abcdef01234567"
	fluxSha = "fedcba9876543210fedcba9876543210fedcba98"
)

var row = checks.AtomDef{ID: "bundle:test", Stage: checks.StageBundle, Lane: checks.LaneAny}

// world is a foundry-dies checkout, a flux checkout, the out and stage
// directories, and an opa that writes what opa writes: a bundle tarball for a
// build, the scripted answer for an eval.
type world struct {
	t                               *testing.T
	in                              atomkit.In
	out, stage, flux                string
	calls                           [][]string
	dataJSON                        string // the built policy's data.json
	table                           string // what the derive answers
	allowed                         string
	roster                          string
	deny                            string
	parent                          int    // git rev-parse sha^1's code
	diff                            string // git diff --name-only
	buildCode, evalCode, runCode    int
	failEval                        map[string]int // by a needle in the eval's argv
	gitDead, headEmpty              bool
	fleetManifest                   string
	builtPolicyRoot, builtStageRoot string
}

func newWorld(t *testing.T, extra map[string]string) *world {
	t.Helper()
	w := &world{t: t, failEval: map[string]int{}, diff: "policy/admission/a.rego\nfleet/data.json\norbits/x-y.toml\n",
		dataJSON: `{"authz_audience":{"star_only":{"chaos":["graph_nodes"]}},"grants":{"g":1},"principals":{"p":1},"capabilities":{"c":1}}`,
		allowed:  `{"result":[{"expressions":[{"value":["search"]}]}]}`,
		deny:     `{"result":[{"expressions":[{"value":["fleet roster: a","fleet roster: b"]}]}]}`,
	}
	files := map[string]string{
		"policy/.manifest":          `{"roots":["admission"]}`,
		"policy/admission/a.rego":   "package admission\n",
		"fleet/data.json":           `{"map":{"chaos":{}},"topics":{"t":{}}}`,
		"fleet/repos/chaos.json":    `{"name":"chaos","kind":"go","verb_prefix":"chaos"}`,
		"fleet/stars/old.slag":      "x",
		"orbits/urania-themis.toml": contractBody,
	}
	for k, v := range extra {
		if v == "" {
			delete(files, k)
			continue
		}
		files[k] = v
	}
	in, fake := atomtest.ToolTree(t, files, w.answer)
	_ = fake
	w.in = in
	w.out, w.stage, w.flux = t.TempDir(), filepath.Join(t.TempDir(), "stage"), t.TempDir()
	t.Setenv(OutEnv, w.out)
	t.Setenv(StageEnv, w.stage)
	t.Setenv(FluxEnv, w.flux)
	t.Setenv("ATOM_SHA", sha)
	t.Setenv("ATOM_WORK", t.TempDir())
	atomtest.Put(t, w.flux, FluxShaFile, fluxSha+"\n")
	atomtest.Put(t, w.flux, "prime/star-chaos.yaml", chaosManifest)
	atomtest.Put(t, w.flux, "data/dbs.yaml", "apiVersion: postgresql.cnpg.io/v1\nkind: Cluster\nmetadata: {name: chaos-db}\n")
	w.roster = `{"fleet":{"map":{"chaos":{}},"topics":{"t":{}},"stars":{"chaos":{"kind":"go"}},"repos":{"chaos":{}},"declared":{"chaos":{}},"flux":{"chaos":{}},"flux_source":{"repo":"foundry/flux","sha":"` + fluxSha + `"}}}`
	return w
}

func (w *world) run() Run { return Run{In: w.in, Row: row} }

func (w *world) ran(needle string) int {
	n := 0
	for _, c := range w.calls {
		if strings.Contains(strings.Join(c, " "), needle) {
			n++
		}
	}
	return n
}

// answer is the programs on PATH.
func (w *world) answer(c atoms.Cmd) (string, int) {
	w.calls = append(w.calls, append([]string{c.Name}, c.Args...))
	args := strings.Join(c.Args, " ")
	switch c.Name {
	case "git":
		if w.gitDead {
			return "git: gone", -1
		}
		switch {
		case strings.Contains(args, "rev-parse HEAD"):
			if w.headEmpty {
				return "", 0
			}
			return sha + "\n", 0
		case strings.Contains(args, "rev-parse"):
			return "", w.parent
		case strings.Contains(args, "diff"):
			return w.diff, 0
		}
	case "opa":
		return w.opa(c, args)
	}
	return "", 0
}

func (w *world) opa(c atoms.Cmd, args string) (string, int) {
	switch c.Args[0] {
	case "build":
		if w.buildCode != 0 {
			return "build error", w.buildCode
		}
		out := valueAfter(c.Args, "-o")
		signed := slices.Contains(c.Args, "--signing-key")
		root := valueAfter(c.Args, "-b")
		files := map[string]string{"/.manifest": `{"revision":"r"}`, "/p/x.rego": "package x\n"}
		switch {
		case strings.HasPrefix(root, "policy"):
			files["/data.json"] = w.dataJSON
			w.builtPolicyRoot = c.Dir
		case valueAfter(c.Args, "--revision") == "gate":
		default:
			files["/data.json"] = `{"fleet":{"map":{}}}`
			files["/.manifest"] = bundlelane.FleetManifest
			if w.fleetManifest != "" {
				files["/.manifest"] = w.fleetManifest
			}
			w.builtStageRoot = root
		}
		if err := os.WriteFile(out, bundleOf(w.t, files, signed), 0o644); err != nil {
			w.t.Fatal(err)
		}
		return "", 0
	case "eval":
		for needle, code := range w.failEval {
			if strings.Contains(args, needle) {
				return "eval failed", code
			}
		}
		if w.evalCode != 0 {
			return "eval failed", w.evalCode
		}
		switch {
		case strings.Contains(args, "data.authz_v2_table.decisions"):
			return w.table, 0
		case strings.Contains(args, "data.authz.callable.allowed"):
			return w.allowed, 0
		case strings.Contains(args, "data.admission.deny"):
			return w.deny, 0
		}
		return w.roster, 0
	case "run":
		return "bundle did not load", w.runCode
	}
	return "", 0
}

func valueAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// bundleOf writes a gzip tar the way opa does: leading-slash names, mode 0600
// and, for a twin, a .signatures.json signed ES256 by a throwaway key.
func bundleOf(t *testing.T, files map[string]string, signed bool) []byte {
	t.Helper()
	var order []string
	for n := range files {
		order = append(order, n)
	}
	slices.Sort(order)
	if signed {
		k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		var listed []map[string]string
		for _, n := range order {
			h := sha256.Sum256([]byte(files[n]))
			listed = append(listed, map[string]string{"name": strings.TrimPrefix(n, "/"), "hash": hex.EncodeToString(h[:]), "algorithm": "SHA-256"})
		}
		b64 := base64.RawURLEncoding
		hdr, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": "default", "typ": "JWT"})
		pay, _ := json.Marshal(map[string]any{"files": listed, "keyid": "default"})
		in := b64.EncodeToString(hdr) + "." + b64.EncodeToString(pay)
		h := sha256.Sum256([]byte(in))
		r, s, _ := ecdsa.Sign(rand.Reader, k, h[:])
		sig := make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
		files["/.signatures.json"] = `{"signatures":["` + in + "." + b64.EncodeToString(sig) + `"]}`
		order = append(order, "/.signatures.json")
	}
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	for _, n := range order {
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o600, Size: int64(len(files[n])), ModTime: time.Unix(0, 0)}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(files[n]))
	}
	tw.Close()
	zw.Close()
	return out.Bytes()
}

// kms is the OPA key in the KMS: it signs what the signing input says and its
// public half is what the twins are verified under.
type kms struct{ key *ecdsa.PrivateKey }

func newKMS(t *testing.T) kms {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return kms{k}
}

func (k kms) pub(t *testing.T) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&k.key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func (k kms) sign(t *testing.T, message string) string {
	t.Helper()
	h := sha256.Sum256([]byte(message))
	d, err := ecdsa.SignASN1(rand.Reader, k.key, h[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(d)
}

func (w *world) read(rel string) string {
	w.t.Helper()
	b, err := os.ReadFile(filepath.Join(w.out, rel))
	if err != nil {
		w.t.Fatal(err)
	}
	return string(b)
}

func expect(t *testing.T, f *Fault, findings bool, needles ...string) {
	t.Helper()
	if f == nil {
		t.Fatalf("no fault, want one carrying %v", needles)
	}
	if f.Findings != findings {
		t.Errorf("findings = %v, want %v: %s", f.Findings, findings, f.Msg)
	}
	for _, n := range needles {
		if !strings.Contains(f.Msg, n) {
			t.Errorf("%q lacks %q", f.Msg, n)
		}
	}
}

const contractBody = `version = "1"
status = "generated"
verbs = ["shape_for", "neighbors"]

[witness]
calls = 3
verbs = ["shape_for"]
`

const chaosManifest = `apiVersion: apps/v1
kind: Deployment
metadata: {name: chaos}
spec:
  template:
    spec:
      containers:
        - name: chaos
          env:
            - {name: CHAOS_PORT, value: "8206"}
            - {name: CHAOS_BIGINT_DATABASE_URL, value: "postgresql://chaos_svid@chaos-db-rw:5432/chaos?pool_max_conns=32&sslmode=verify-full&sslrootcert=/etc/stellar/chaos-db-ca/ca.crt"}
---
apiVersion: v1
kind: Service
metadata: {name: chaos}
spec:
  ports:
    - {name: mcp,  port: 8206, targetPort: 8206, protocol: TCP}
    - {name: mtls, port: 8207, targetPort: 8207, protocol: TCP}
`

// failWrite makes every write to a path holding needle fail.
func failWrite(t *testing.T, needle string) (restore func()) {
	t.Helper()
	prev := osWrite
	osWrite = func(name string, data []byte, perm os.FileMode) error {
		if strings.Contains(name, needle) {
			return os.ErrPermission
		}
		return prev(name, data, perm)
	}
	t.Cleanup(func() { osWrite = prev })
	return func() { osWrite = prev }
}
