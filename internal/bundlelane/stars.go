package bundlelane

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// THE CATALOG ENTRY AND THE FLUX FACTS (slag-by-kind F8). Slag retires: a
// star's record becomes one declared document, foundry-dies
// fleet/stars/<name>.json, and what the record used to declare about the
// runtime (address, database) is derived from foundry/flux instead. This
// lane serves both into the fleet die:
//
//	fleet/stars/<name>.json      -> data.fleet.declared.<name>
//	foundry/flux prime/star-*    -> data.fleet.flux.<name>
//
// declared IS A PARALLEL KEY, NOT data.fleet.stars. data.fleet.stars is still
// the lean fleet/stars/<name>/data.json hades and nyx route by (address,
// conform, db, topics), and none of that is in the declared document; the
// swap waits on the Rego that derives those fields (F9).
//
// opa build -b reads only files named data.json, so neither source reaches
// the bundle by itself: the lane stages one data.json per key.

// The data.fleet keys this lane injects, and where they are staged.
const (
	DeclaredKey  = "declared"
	FluxKey      = "flux"
	DeclaredPath = "fleet/" + DeclaredKey + "/data.json"
	FluxPath     = "fleet/" + FluxKey + "/data.json"
	// DeclaredGlob finds the catalog entries: files directly under
	// fleet/stars/, beside the per-star directories.
	DeclaredGlob = "fleet/stars/*.json"
	// StarManifestGlob and ClusterGlob are read in foundry/flux.
	StarManifestGlob = "prime/star-*.yaml"
	ClusterGlob      = "data/*.yaml"
)

// Declared reads the catalog entries, path -> body, into name -> document. A
// document must be one JSON object whose name is its filename; anything else
// is an error naming the file, because a document served under the wrong key
// is a star routed as another. The schema is foundry-dies' gate (dies:schema);
// this is only what the lane needs to key the row.
func Declared(files map[string]string) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for _, p := range sortedKeys(files) {
		stem := strings.TrimSuffix(path.Base(p), ".json")
		var doc map[string]json.RawMessage
		if err := json.Unmarshal([]byte(files[p]), &doc); err != nil {
			return nil, fmt.Errorf("%s is not one JSON object: %w", p, err)
		}
		var name string
		if err := json.Unmarshal(doc["name"], &name); err != nil || name != stem {
			return nil, fmt.Errorf("%s: name must be %q, its filename", p, stem)
		}
		out[stem] = json.RawMessage(files[p])
	}
	return out, nil
}

// Ports is a star's listen and mTLS port, read off its Service.
type Ports struct {
	Listen int `json:"listen,omitempty"`
	MTLS   int `json:"mtls,omitempty"`
}

// FluxFact is what flux says about one star.
type FluxFact struct {
	Ports *Ports   `json:"ports,omitempty"`
	DB    []string `json:"db,omitempty"`
}

// sslRootCert is the CA directory a star's DSN pins: the CNPG cluster's
// server CA mounts at /etc/stellar/<cluster>-ca/.
var sslRootCert = regexp.MustCompile(`sslrootcert=/etc/stellar/([a-z0-9-]+)-ca/`)

// dsnHost is a DSN's host, between the userinfo and the port or path.
var dsnHost = regexp.MustCompile(`^postgres(?:ql)?://(?:[^@/]*@)?([^:/?]+)`)

// Clusters answers the CNPG Cluster names declared in flux's data/*.yaml.
// Only these may be a star's db: a DSN host that is an alias (aether,
// erebus-timescaledb) or a template ({aether}) names no cluster.
func Clusters(manifests map[string]string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, p := range sortedKeys(manifests) {
		docs, err := documents(manifests[p])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		for _, d := range docs {
			if d.Kind == "Cluster" && strings.HasPrefix(d.APIVersion, "postgresql.cnpg.io/") && d.Metadata.Name != "" {
				out[d.Metadata.Name] = true
			}
		}
	}
	return out, nil
}

// FluxFacts reads each star's manifest (prime/star-<name>.yaml, path ->
// body) into its facts, for the stars named in stars only — flux carries
// manifests that are not stars (svid-sidecar). A star with no manifest, or a
// manifest that yields no port and no database, gets no entry.
//
// PORTS come from the Service named for the star, then the Services named
// <star>-*, in name order: listen is the first port named mcp or http, mtls
// the first named mtls. A redis or git port is not the star's address
// (mnemosyne-redis, tron-redis, ourea's git).
//
// DB is every CNPG cluster a container's Postgres DSN names, sorted: by the
// CA directory its sslrootcert pins, else by its host with a -rw/-ro/-r
// suffix taken off. Only a name in clusters counts.
func FluxFacts(manifests map[string]string, stars, clusters map[string]bool) (map[string]FluxFact, error) {
	out := map[string]FluxFact{}
	for _, p := range sortedKeys(manifests) {
		base := path.Base(p)
		if !strings.HasPrefix(base, "star-") || !strings.HasSuffix(base, ".yaml") {
			continue
		}
		star := strings.TrimSuffix(strings.TrimPrefix(base, "star-"), ".yaml")
		if !stars[star] {
			continue
		}
		docs, err := documents(manifests[p])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		var fact FluxFact
		if ports := servicePorts(star, docs); ports != (Ports{}) {
			fact.Ports = &ports
		}
		fact.DB = databases(docs, clusters)
		if fact.Ports != nil || len(fact.DB) > 0 {
			out[star] = fact
		}
	}
	return out, nil
}

// servicePorts reads the star's ports off its Services (FluxFacts).
func servicePorts(star string, docs []manifest) Ports {
	var svcs []manifest
	for _, d := range docs {
		if d.Kind == "Service" && (d.Metadata.Name == star || strings.HasPrefix(d.Metadata.Name, star+"-")) {
			svcs = append(svcs, d)
		}
	}
	sort.SliceStable(svcs, func(i, j int) bool {
		a, b := svcs[i].Metadata.Name, svcs[j].Metadata.Name
		if (a == star) != (b == star) {
			return a == star
		}
		return a < b
	})
	var p Ports
	for _, s := range svcs {
		for _, port := range s.Spec.Ports {
			switch port.Name {
			case "mcp", "http":
				if p.Listen == 0 {
					p.Listen = port.Port
				}
			case "mtls":
				if p.MTLS == 0 {
					p.MTLS = port.Port
				}
			}
		}
	}
	return p
}

// databases reads the CNPG clusters a manifest's DSNs name (FluxFacts).
func databases(docs []manifest, clusters map[string]bool) []string {
	seen := map[string]bool{}
	for _, d := range docs {
		for _, c := range d.containers() {
			for _, e := range c.Env {
				if name := cluster(e.Value); name != "" && clusters[name] {
					seen[name] = true
				}
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	return sortedKeys(seen)
}

// cluster names the CNPG cluster one env value's DSN points at, or "".
func cluster(value string) string {
	m := dsnHost.FindStringSubmatch(value)
	if m == nil {
		return ""
	}
	if ca := sslRootCert.FindStringSubmatch(value); ca != nil {
		return ca[1]
	}
	host := strings.SplitN(m[1], ".", 2)[0]
	for _, suffix := range []string{"-rw", "-ro", "-r"} {
		if strings.HasSuffix(host, suffix) {
			return strings.TrimSuffix(host, suffix)
		}
	}
	return host
}

// Stage renders one injected key's data.json: the rows keyed by star, sorted,
// two-space indented, one trailing LF — the same bytes for the same inputs,
// so the die's data.json moves only when a fact did.
func Stage[T any](rows map[string]T) (string, error) {
	if rows == nil {
		rows = map[string]T{}
	}
	b, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return "", err
	}
	// MarshalIndent re-indents a RawMessage too, so a declared document's own
	// layout does not reach the die; encoding/json sorts the map keys.
	return string(b) + "\n", nil
}

// Collides answers the injected keys fleet/data.json already carries. OPA
// refuses a bundle whose data.json and fleet/<key>/data.json both set
// data.fleet.<key>, so the lane names the collision before it builds.
func Collides(dataJSON string) ([]string, error) {
	var d map[string]json.RawMessage
	if err := json.Unmarshal([]byte(dataJSON), &d); err != nil {
		return nil, fmt.Errorf("fleet/data.json is not JSON: %w", err)
	}
	var hit []string
	for _, k := range []string{DeclaredKey, FluxKey} {
		if _, ok := d[k]; ok {
			hit = append(hit, k)
		}
	}
	return hit, nil
}

// Injected grades the built roster's data.json: it carries data.fleet.declared
// and data.fleet.flux with exactly the rows the lane staged. problem is ""
// when it does.
func Injected(dataJSON string, declared, flux int) (string, error) {
	var d struct {
		Fleet map[string]json.RawMessage `json:"fleet"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &d); err != nil {
		return "", fmt.Errorf("the built roster's data.json is not JSON: %w", err)
	}
	for _, k := range []struct {
		key  string
		want int
	}{{DeclaredKey, declared}, {FluxKey, flux}} {
		raw, ok := d.Fleet[k.key]
		if !ok {
			return fmt.Sprintf("the built roster carries no data.fleet.%s — the staged %s did not reach the die", k.key, "fleet/"+k.key+"/data.json"), nil
		}
		if got := size(raw); got != k.want {
			return fmt.Sprintf("the built roster's data.fleet.%s has %d rows, the lane staged %d", k.key, got, k.want), nil
		}
	}
	return "", nil
}

// manifest is the part of a Kubernetes object these readers need.
type manifest struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Ports []struct {
			Name string `yaml:"name"`
			Port int    `yaml:"port"`
		} `yaml:"ports"`
		Template podTemplate `yaml:"template"`
		// JobTemplate is a CronJob's.
		JobTemplate struct {
			Spec struct {
				Template podTemplate `yaml:"template"`
			} `yaml:"spec"`
		} `yaml:"jobTemplate"`
	} `yaml:"spec"`
}

type podTemplate struct {
	Spec struct {
		Containers     []container `yaml:"containers"`
		InitContainers []container `yaml:"initContainers"`
	} `yaml:"spec"`
}

type container struct {
	Env []struct {
		Value string `yaml:"value"`
	} `yaml:"env"`
}

// containers is every container a workload object runs, init ones included.
func (m manifest) containers() []container {
	var out []container
	for _, t := range []podTemplate{m.Spec.Template, m.Spec.JobTemplate.Spec.Template} {
		out = append(out, t.Spec.Containers...)
		out = append(out, t.Spec.InitContainers...)
	}
	return out
}

// documents decodes every YAML document in one file; an empty one is skipped.
func documents(body string) ([]manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader([]byte(body)))
	var out []manifest
	for {
		var m manifest
		err := dec.Decode(&m)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if m.Kind != "" {
			out = append(out, m)
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
