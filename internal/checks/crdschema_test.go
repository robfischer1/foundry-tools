package checks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const objectStoreCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: objectstores.barmancloud.cnpg.io
spec:
  group: barmancloud.cnpg.io
  names:
    kind: ObjectStore
  versions:
  - name: v1
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              compression:
                type: string
                enum: [bzip2, gzip, lz4, snappy]
              size:
                x-kubernetes-int-or-string: true
              note:
                type: string
                nullable: true
              free:
                type: object
                x-kubernetes-preserve-unknown-fields: true
                properties:
                  a:
                    type: string
  - name: v2
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: not-a-crd
`

func TestCRDSchemasAreGeneratedFromTheCRDsInTheTree(t *testing.T) {
	got, err := CRDSchemas(objectStoreCRD)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("one served version has a schema, got %d: %v", len(got), got)
	}
	body := got["barmancloud.cnpg.io/objectstore_v1.json"]
	var s map[string]any
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatalf("not JSON: %v: %s", err, body)
	}
	for _, want := range []string{`"additionalProperties":false`, `"lz4"`, `"anyOf":[{"type":"integer"},{"type":"string"}]`, `"type":["string","null"]`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("schema lacks %s: %s", want, body)
		}
	}
	free := s["properties"].(map[string]any)["spec"].(map[string]any)["properties"].(map[string]any)["free"].(map[string]any)
	if _, strict := free["additionalProperties"]; strict {
		t.Errorf("preserve-unknown-fields stays open: %v", free)
	}
}

func TestCRDSchemasRefuseWhatTheyCannotRead(t *testing.T) {
	for name, in := range map[string]string{
		"a stream that does not parse": "kind: [\n",
		"a CRD with no group":          "kind: CustomResourceDefinition\nspec:\n  names:\n    kind: X\n",
	} {
		if _, err := CRDSchemas(in); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if got, err := CRDSchemas(""); err != nil || len(got) != 0 {
		t.Errorf("an empty stream carries no CRDs: %v %v", got, err)
	}
}

func TestCRDReportHasNoWarnState(t *testing.T) {
	res := func(kind, ver, status string) string {
		return `{"filename":"f.yaml","kind":"` + kind + `","name":"n","version":"` + ver + `","status":"` + status + `","msg":"m"}`
	}
	doc := func(rs ...string) string { return `{"resources":[` + strings.Join(rs, ",") + `]}` }
	for name, tc := range map[string]struct {
		in    string
		state int
		has   string
	}{
		"all valid":                                {doc(res("A", "x/v1", "statusValid")), 0, "1 valid"},
		"the skipped CRD kind is not a gap":        {doc(res("CustomResourceDefinition", "apiextensions.k8s.io/v1", "statusSkipped")), 0, "0 kind(s)"},
		"a named kind is allowed, with its reason": {doc(res("LinstorCluster", "piraeus.io/v1", "statusSkipped")), 0, "allowed without a schema piraeus.io/v1/LinstorCluster x1: CRD installed by the piraeus-operator chart"},
		"invalid is a finding":                     {doc(res("A", "x/v1", "statusInvalid")), 1, "INVALID f.yaml A n: m"},
		"a kind with no schema is a finding":       {doc(res("Z", "z.io/v1", "statusSkipped"), res("Z", "z.io/v1", "statusSkipped")), 1, "NO SCHEMA z.io/v1/Z x2"},
		"an unreadable schema is a 2":              {doc(res("A", "x/v1", "statusError")), 2, "UNANALYZABLE"},
		"a finding outranks a fault":               {doc(res("A", "x/v1", "statusError"), res("B", "x/v1", "statusInvalid")), 1, "UNREADABLE"},
		"output that is not JSON is a 2":           {"panic: boom", 2, "UNANALYZABLE: kubeconform's report was not JSON"},
	} {
		t.Run(name, func(t *testing.T) {
			state, report := CRDReport(tc.in)
			if state != tc.state || !strings.Contains(report, tc.has) {
				t.Errorf("state %d (want %d), report %q lacks %q", state, tc.state, report, tc.has)
			}
		})
	}
}

func TestWriteCRDSchemasLaysTheTreeAndReportsWhatItCannotWrite(t *testing.T) {
	dir, err := WriteCRDSchemas(map[string][]byte{"g.io/k_v1.json": []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if b, err := os.ReadFile(filepath.Join(dir, "g.io", "k_v1.json")); err != nil || string(b) != "{}" {
		t.Errorf("not laid: %q %v", b, err)
	}
	// a file where a group directory must go
	d2, err := WriteCRDSchemas(map[string][]byte{"a": []byte("x"), "a/b.json": []byte("x")})
	if d2 != "" {
		defer os.RemoveAll(d2)
	}
	if err == nil {
		t.Error("a schema under a file was laid")
	}
	// a path that is itself a directory cannot be written as a file
	d3, err := WriteCRDSchemas(map[string][]byte{"a/b.json": []byte("x"), "a/b.json/c": []byte("x")})
	if d3 != "" {
		defer os.RemoveAll(d3)
	}
	if err == nil {
		t.Error("a schema under a schema was laid")
	}
	t.Setenv("TMPDIR", filepath.Join(dir, "no", "such"))
	if d, err := WriteCRDSchemas(nil); err == nil || d != "" {
		t.Errorf("a missing temp dir was accepted: %q %v", d, err)
	}
}
