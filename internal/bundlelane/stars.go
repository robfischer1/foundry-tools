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
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// THE CATALOG ENTRY AND THE FLUX FACTS (slag-by-kind F8, F14). Slag is
// retired: a repo's record is one declared document, foundry-dies
// fleet/repos/<name>.json, and what the record used to declare about the
// runtime (address, database) is derived from foundry/flux instead. This
// lane serves both into the fleet die:
//
//	fleet/repos/<name>.json      -> data.fleet.declared.<name>
//	foundry/flux prime/star-*    -> data.fleet.flux.<name>
//
// declared IS A PARALLEL KEY, NOT data.fleet.stars: data.fleet.stars and
// data.fleet.repos are the rules of fleet/derive.rego, and OPA refuses a rule
// and a data document at the same path ("conflicting rule for data path
// fleet/stars").
//
// opa build -b reads only files named data.json, so neither source reaches
// the bundle by itself: the lane stages one data.json per key.

// The data.fleet keys this lane injects, and where they are staged.
const (
	DeclaredKey   = "declared"
	FluxKey       = "flux"
	FluxSourceKey = "flux_source"
	DeclaredPath  = "fleet/" + DeclaredKey + "/data.json"
	FluxPath      = "fleet/" + FluxKey + "/data.json"
	// FluxSourcePath stamps the flux commit the facts were read at:
	// data.fleet.flux_source = {repo, sha}.
	FluxSourcePath = "fleet/" + FluxSourceKey + "/data.json"
	// FluxRepo is the repository the facts are read from.
	FluxRepo = "foundry/flux"
	// ReposKey is data.fleet.repos, the catalog fleet/derive.rego derives
	// (one row per declared key). The lane does not stage it; it is
	// reserved so a tier data.json cannot collide with the rule.
	ReposKey = "repos"
	// DeclaredGlob finds the catalog entries: fleet/repos/<name>.json, the
	// F10 documents (decision absent-wrap-width).
	DeclaredGlob = "fleet/repos/*.json"
	// StarManifestGlob and ClusterGlob are read in foundry/flux.
	StarManifestGlob = "prime/star-*.yaml"
	ClusterGlob      = "data/*.yaml"
)

// ServiceGlobs find every manifest that may hold a Service in namespace prime
// or the Service an ExternalName there points at: prime itself, the blades
// namespace (blade-runner) and hemera's (hemera-otelcol).
//
// clusters/pantheon/*.yaml is read beside them for the Flux Kustomizations: a
// directory whose Kustomization sets targetNamespace (blades) names no
// namespace in its own manifests.
var ServiceGlobs = []string{"prime/*.yaml", "blades/*.yaml", "hemera/*.yaml", "clusters/pantheon/*.yaml"}

// FluxReadGlobs is every glob of foundry/flux this lane reads, the set a flux
// landing must touch to change what the lane would stage. The ourea door
// mirrors it (internal/verbs fluxFactGlobs) to decide when a flux landing asks
// this lane again; TestFluxReadGlobsPinned fails when the set moves so the
// mirror is updated with it.
func FluxReadGlobs() []string {
	return append([]string{StarManifestGlob, ClusterGlob}, ServiceGlobs...)
}

// starLabel is the label every star's workload carries.
const starLabel = "pistis.star"

// PrimeNamespace is the namespace whose Services make a repo a deployed star.
const PrimeNamespace = "prime"

// Declared reads the catalog entries, path -> body, into name -> document. A
// document must be one JSON object whose name is its filename; anything else
// is an error naming the file, because a document served under the wrong key
// is a star routed as another. The schema is foundry-dies' gate (dies:schema);
// this is only what the lane needs to key the row.
func Declared(files map[string]string) (map[string]map[string]any, error) {
	out := map[string]map[string]any{}
	for _, p := range sortedKeys(files) {
		stem := strings.TrimSuffix(path.Base(p), ".json")
		var doc map[string]any
		if err := json.Unmarshal([]byte(files[p]), &doc); err != nil {
			return nil, fmt.Errorf("%s is not one JSON object: %w", p, err)
		}
		if name, _ := doc["name"].(string); name != stem {
			return nil, fmt.Errorf("%s: name must be %q, its filename", p, stem)
		}
		out[stem] = doc
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
	Ports *Ports `json:"ports,omitempty"`
	// Interface is the protocols the star's Service port names imply, sorted:
	// a port named mcp is "mcp", a port named http is "http", a port named
	// otlp-http or otlp-grpc is "otlp", a port named openapi is "openapi" (hades
	// then routes the star over REST; removing the port reverts it to MCP), a port
	// named openapi-http is "openapi-http:<port>" (the plaintext REST listener's
	// Service port, the address hades dials in place of the mTLS door). Absent
	// when no Service port carries one of those names.
	Interface []string `json:"interface,omitempty"`
	DB        []string `json:"db,omitempty"`
	// NoGateway is the star's NoGatewayAnnotation, the reason the session
	// gateway does not call it; absent when no Service of the star carries one.
	NoGateway string `json:"no_gateway,omitempty"`
}

// NoGatewayAnnotation, on a star's Service, says the session gateway (hades)
// is not one of the star's callers, and why. fleet/derive.rego copies it onto
// the star's row as no_gateway, and hades routes no such row: no dial, no
// index, no verbs. The star keeps serving its declared callers on its faces
// (aletheia: ares's tee on openapi-http), which is why the port name cannot say
// it — nyx resolves the REST port by that name.
const NoGatewayAnnotation = "hades.notusmi.com/no-gateway"

// sslRootCert is the CA directory a star's DSN pins: the CNPG cluster's
// server CA mounts at /etc/stellar/<cluster>-ca/.
var sslRootCert = regexp.MustCompile(`sslrootcert=/etc/stellar/([a-z0-9-]+)-ca/`)

// fullSHA is a full git commit id.
var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

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
			if d.Kind == "Cluster" && strings.HasPrefix(d.APIVersion, "postgresql.cnpg.io/") && d.Name != "" {
				out[d.Name] = true
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
// INTERFACE is the set of protocols those names imply: mcp -> "mcp", http ->
// "http", otlp-http or otlp-grpc -> "otlp", openapi -> "openapi", openapi-http ->
// "openapi-http:<port>", over every Service of the star (the
// same selection as PORTS). The name is the declaration: a star that serves
// plain HTTP names its port http, and a collector that serves OTLP names its
// ports for it. The listen port of an OTLP star is the one named otlp-http
// (hemera's 4318), when no port is named mcp or http.
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
		fact.Interface = interfaces(star, docs)
		fact.NoGateway = noGatewayOf(starServices(star, docs))
		fact.DB = databases(docs, clusters)
		if fact.Ports != nil || len(fact.DB) > 0 {
			out[star] = fact
		}
	}
	return out, nil
}

// ServiceFacts is the flux entry of every repo in repos that has a Service in
// namespace prime and none in have (FluxFacts' rows): the deployed repos a
// star manifest does not describe. manifests is path -> body over
// ServiceGlobs. The Service is the one named for the repo; an ExternalName is
// followed to its target Service (<name>.<ns>.svc...) in the same manifests,
// and the ports and interface are read off that target by FluxFacts' rules.
// The entry exists on the Service alone: a target flux does not carry, or one
// whose ports are named for none of mcp, http, mtls or otlp-*, yields an empty
// fact, which is still the repo's membership in data.fleet.stars.
func ServiceFacts(manifests map[string]string, repos map[string]bool, have map[string]FluxFact) (map[string]FluxFact, error) {
	parsed, targetNS, err := parseAll(manifests)
	if err != nil {
		return nil, err
	}
	byName := map[string]manifest{}
	for _, p := range sortedKeys(parsed) {
		for _, d := range parsed[p] {
			if d.Kind == "Service" {
				byName[namespaceOf(d, p, targetNS)+"/"+d.Name] = d
			}
		}
	}
	out := map[string]FluxFact{}
	for name := range repos {
		if _, ok := have[name]; ok {
			continue
		}
		svc, ok := byName[PrimeNamespace+"/"+name]
		if !ok {
			continue
		}
		if svc.Type == "ExternalName" {
			target, ok := byName[externalTarget(svc.ExternalName)]
			if !ok {
				out[name] = FluxFact{}
				continue
			}
			svc = target
		}
		var fact FluxFact
		if ports := portsOf([]manifest{svc}); ports != (Ports{}) {
			fact.Ports = &ports
		}
		fact.Interface = interfacesOf([]manifest{svc})
		fact.NoGateway = noGatewayOf([]manifest{svc})
		out[name] = fact
	}
	return out, nil
}

// parseAll decodes every manifest file, and reads each Flux Kustomization's
// targetNamespace by the directory it builds (directory -> namespace).
func parseAll(manifests map[string]string) (map[string][]manifest, map[string]string, error) {
	parsed := map[string][]manifest{}
	targetNS := map[string]string{}
	for _, p := range sortedKeys(manifests) {
		docs, err := documents(manifests[p])
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", p, err)
		}
		parsed[p] = docs
		for _, d := range docs {
			// documents() sets TargetNamespace for a Kustomization alone, so the
			// kind needs no test of its own.
			if strings.HasPrefix(d.APIVersion, "kustomize.toolkit.fluxcd.io/") && d.TargetNamespace != "" {
				targetNS[strings.Trim(strings.TrimPrefix(d.Path, "./"), "/")] = d.TargetNamespace
			}
		}
	}
	return parsed, targetNS, nil
}

// namespaceOf is where an object sits: the namespace it names, else the one
// its Kustomization puts its directory in (targetNamespace), else the one named
// for its directory: blades/blade-controller.yaml's blade-runner names none.
func namespaceOf(d manifest, file string, targetNS map[string]string) string {
	dir := strings.SplitN(file, "/", 2)[0]
	if d.Namespace != "" {
		return d.Namespace
	}
	if ns := targetNS[dir]; ns != "" {
		return ns
	}
	return dir
}

// WorkloadFacts is the flux entry of every repo in repos whose workload flux
// deploys in namespace prime — a Deployment or StatefulSet — and that has none
// in have (FluxFacts' and ServiceFacts' rows): the workers that serve nothing
// (tron consumes from Kafka; naiad pulls from Poseidon). The workload is the
// one labelled pistis.star=<repo> (on the object or its pod template; a label
// naming no repo in repos is an error), else the one named for the repo. The entry states no ports and no interface — there
// is no Service to read them off — and carries db when the workload's env
// names a CNPG cluster in clusters. It is still the repo's membership in
// data.fleet.stars.
func WorkloadFacts(manifests map[string]string, repos map[string]bool, have map[string]FluxFact, clusters map[string]bool) (map[string]FluxFact, error) {
	parsed, targetNS, err := parseAll(manifests)
	if err != nil {
		return nil, err
	}
	out := map[string]FluxFact{}
	for _, p := range sortedKeys(parsed) {
		for _, d := range parsed[p] {
			if (d.Kind != "Deployment" && d.Kind != "StatefulSet") || namespaceOf(d, p, targetNS) != PrimeNamespace {
				continue
			}
			name := d.Name
			if d.Star != "" {
				// A label that names no catalog repo is a typo or a repo never
				// declared: refuse, rather than fall back to the Deployment name
				// and state a different star than the manifest says.
				if !repos[d.Star] {
					return nil, fmt.Errorf("%s: the %s %s carries %s=%s, which is no catalog repo", p, d.Kind, d.Name, starLabel, d.Star)
				}
				name = d.Star
			}
			if !repos[name] {
				continue
			}
			if _, ok := have[name]; ok {
				continue
			}
			fact := out[name]
			fact.DB = mergeSorted(fact.DB, databases([]manifest{d}, clusters))
			out[name] = fact
		}
	}
	return out, nil
}

// mergeSorted is the sorted union of two lists; nil when both are empty.
func mergeSorted(a, b []string) []string {
	seen := map[string]bool{}
	for _, v := range append(append([]string{}, a...), b...) {
		seen[v] = true
	}
	if len(seen) == 0 {
		return nil
	}
	return sortedKeys(seen)
}

// externalTarget turns an ExternalName (<svc>.<ns>.svc[.cluster.local]) into
// the <ns>/<svc> key ServiceFacts indexes by; "" when it is not in-cluster.
func externalTarget(host string) string {
	parts := strings.Split(host, ".")
	if len(parts) < 3 || parts[2] != "svc" {
		return ""
	}
	return parts[1] + "/" + parts[0]
}

// starServices is the star's Services, the one named for it first (FluxFacts).
func starServices(star string, docs []manifest) []manifest {
	var svcs []manifest
	for _, d := range docs {
		if d.Kind == "Service" && (d.Name == star || strings.HasPrefix(d.Name, star+"-")) {
			svcs = append(svcs, d)
		}
	}
	sort.SliceStable(svcs, func(i, j int) bool {
		a, b := svcs[i].Name, svcs[j].Name
		if (a == star) != (b == star) {
			return a == star
		}
		return a < b
	})
	return svcs
}

// interfaces reads the protocols the star's Service port names imply
// (FluxFacts), sorted.
func interfaces(star string, docs []manifest) []string {
	return interfacesOf(starServices(star, docs))
}

// interfacesOf is interfaces over the Services already chosen.
func interfacesOf(svcs []manifest) []string {
	seen := map[string]bool{}
	for _, s := range svcs {
		for _, port := range s.Ports {
			switch port.Name {
			case "mcp", "http":
				seen[port.Name] = true
			case "otlp-http", "otlp-grpc":
				seen["otlp"] = true
			case "openapi":
				// A marker, never an address: hades routes the star through
				// its OpenAPI translator (the REST surface on the mtls port).
				seen["openapi"] = true
			case "openapi-http":
				// A marker that carries its port: the plaintext REST listener
				// (STELLAR_REST_PLAINTEXT_PORT) lives on a port of its own, and
				// the roster address names only the mTLS door. A port the
				// manifest does not state as a number names nothing.
				if port.Port > 0 {
					seen["openapi-http:"+strconv.Itoa(port.Port)] = true
				}
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	return sortedKeys(seen)
}

// noGatewayOf is the first NoGatewayAnnotation among the Services, or "".
func noGatewayOf(svcs []manifest) string {
	for _, s := range svcs {
		if s.NoGateway != "" {
			return s.NoGateway
		}
	}
	return ""
}

// servicePorts reads the star's ports off its Services (FluxFacts).
func servicePorts(star string, docs []manifest) Ports {
	return portsOf(starServices(star, docs))
}

// portsOf is servicePorts over the Services already chosen.
func portsOf(svcs []manifest) Ports {
	var p, otlp Ports
	for _, s := range svcs {
		for _, port := range s.Ports {
			switch port.Name {
			case "mcp", "http":
				if p.Listen == 0 {
					p.Listen = port.Port
				}
			case "mtls":
				if p.MTLS == 0 {
					p.MTLS = port.Port
				}
			case "otlp-http":
				// An OTLP collector's address is its http port; it counts
				// only when no port is named mcp or http.
				if otlp.Listen == 0 {
					otlp.Listen = port.Port
				}
			}
		}
	}
	if p.Listen == 0 {
		p.Listen = otlp.Listen
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
// two-space indented, one trailing LF — the same bytes for the same inputs.
// The die as a whole still moves on every flux commit it is built at, because
// data.fleet.flux_source names that commit even when no fact changed.
func Stage[T any](rows map[string]T) string {
	if rows == nil {
		rows = map[string]T{}
	}
	// The rows are decoded JSON objects (Declared) or FluxFacts: neither holds
	// a value encoding/json cannot marshal, so there is no error to carry.
	b, _ := json.MarshalIndent(rows, "", "  ")
	// encoding/json sorts the map keys, and a document decoded and re-encoded
	// keeps none of its file's own layout.
	return string(b) + "\n"
}

// Collides answers the injected keys fleet/data.json already carries. OPA
// refuses a bundle whose data.json and fleet/<key>/data.json both set
// data.fleet.<key>, so the lane names the collision before it builds. A tier
// that is not JSON collides with nothing here: Orphans has already refused it.
func Collides(dataJSON string) []string {
	var d map[string]json.RawMessage
	if json.Unmarshal([]byte(dataJSON), &d) != nil {
		return nil
	}
	var hit []string
	for _, k := range []string{DeclaredKey, FluxKey, FluxSourceKey, ReposKey} {
		if _, ok := d[k]; ok {
			hit = append(hit, k)
		}
	}
	return hit
}

// Inputs is what the lane read for the injection: fleet/data.json and the
// path -> body maps — the catalog entries, flux's star manifests, flux's
// data/ manifests and the Service manifests.
type Inputs struct {
	// FluxSHA is the flux commit the manifests were read at.
	FluxSHA         string
	Tier            string
	Entries         map[string]string
	StarManifests   map[string]string
	ClusterManifest map[string]string
	// ServiceManifests is path -> body over ServiceGlobs.
	ServiceManifests map[string]string
}

// Injection is the two staged data.json bodies and the rows each holds.
type Injection struct {
	Declared, Flux, Source string
	DeclaredRows, FluxRows int
	Known, Clusters        int
}

// Inject decides the whole injection from what the lane read. The stars flux
// is read for are the ones the tree knows: the catalog entries. A
// flux tree that answers no star manifest is an error, not an empty key: an
// empty data.fleet.flux would derive every address away.
func Inject(in Inputs) (Injection, error) {
	if !fullSHA.MatchString(in.FluxSHA) {
		return Injection{}, fmt.Errorf("the flux commit %q is not a full sha — the die would not say which flux it read", in.FluxSHA)
	}
	if hit := Collides(in.Tier); len(hit) > 0 {
		return Injection{}, fmt.Errorf("fleet/data.json already sets data.fleet.%s, which the lane injects — OPA would refuse the bundle", strings.Join(hit, ", data.fleet."))
	}
	docs, err := Declared(in.Entries)
	if err != nil {
		return Injection{}, err
	}
	if len(in.StarManifests) == 0 {
		return Injection{}, errors.New("foundry/flux answered no " + StarManifestGlob + " — an empty data.fleet.flux would derive every star's address away")
	}
	stars := map[string]bool{}
	for name := range docs {
		stars[name] = true
	}
	clusters, err := Clusters(in.ClusterManifest)
	if err != nil {
		return Injection{}, fmt.Errorf("foundry/flux %w", err)
	}
	facts, err := FluxFacts(in.StarManifests, stars, clusters)
	if err != nil {
		return Injection{}, fmt.Errorf("foundry/flux %w", err)
	}
	svcFacts, err := ServiceFacts(in.ServiceManifests, stars, facts)
	if err != nil {
		return Injection{}, fmt.Errorf("foundry/flux %w", err)
	}
	for name, f := range svcFacts {
		facts[name] = f
	}
	workFacts, err := WorkloadFacts(in.ServiceManifests, stars, facts, clusters)
	if err != nil {
		return Injection{}, fmt.Errorf("foundry/flux %w", err)
	}
	for name, f := range workFacts {
		facts[name] = f
	}
	source := Stage(map[string]string{"repo": FluxRepo, "sha": in.FluxSHA})
	return Injection{Declared: Stage(docs), Flux: Stage(facts), Source: source, DeclaredRows: len(docs), FluxRows: len(facts), Known: len(stars), Clusters: len(clusters)}, nil
}

// Injected grades the built roster's data.json: it carries data.fleet.declared
// and data.fleet.flux with exactly the rows the lane staged, and
// data.fleet.flux_source names the flux commit they were read at. problem is ""
// when it does.
func Injected(dataJSON string, declared, flux int, fluxSHA string) string {
	var d struct {
		Fleet map[string]json.RawMessage `json:"fleet"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &d); err != nil {
		return "the built roster's data.json is not JSON: " + err.Error()
	}
	for _, k := range []struct {
		key  string
		want int
	}{{DeclaredKey, declared}, {FluxKey, flux}} {
		raw, ok := d.Fleet[k.key]
		if !ok {
			return fmt.Sprintf("the built roster carries no data.fleet.%s — the staged %s did not reach the die", k.key, "fleet/"+k.key+"/data.json")
		}
		if got := size(raw); got != k.want {
			return fmt.Sprintf("the built roster's data.fleet.%s has %d rows, the lane staged %d", k.key, got, k.want)
		}
	}
	var source struct {
		SHA string `json:"sha"`
	}
	if json.Unmarshal(d.Fleet[FluxSourceKey], &source) != nil || source.SHA != fluxSHA {
		return fmt.Sprintf("the built roster's data.fleet.%s does not name flux %s, the commit the facts were read at", FluxSourceKey, fluxSHA)
	}
	return ""
}

// manifest is the part of a Kubernetes object these readers need: its head
// for every kind, and its spec only for the kinds whose spec they read.
type manifest struct {
	APIVersion string
	Kind       string
	Name       string
	Namespace  string
	// Path and TargetNamespace are a Flux Kustomization's spec.path and
	// spec.targetNamespace.
	Path, TargetNamespace string
	// Type and ExternalName are a Service's spec.type and spec.externalName.
	Type, ExternalName string
	// Ports is a Service's spec.ports.
	Ports []servicePort
	// NoGateway is a Service's NoGatewayAnnotation.
	NoGateway string
	// Pods is a workload's pod templates.
	Pods []podTemplate
	// Star is a workload's pistis.star label, from the object or its pod
	// template; "" when it carries none.
	Star string
}

type head struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name        string            `yaml:"name"`
		Namespace   string            `yaml:"namespace"`
		Labels      map[string]string `yaml:"labels"`
		Annotations map[string]string `yaml:"annotations"`
	} `yaml:"metadata"`
}

type servicePort struct {
	Name string `yaml:"name"`
	Port int    `yaml:"port"`
}

type kustomizationDoc struct {
	Spec struct {
		Path            string `yaml:"path"`
		TargetNamespace string `yaml:"targetNamespace"`
	} `yaml:"spec"`
}

type serviceDoc struct {
	Spec struct {
		Type         string        `yaml:"type"`
		ExternalName string        `yaml:"externalName"`
		Ports        []servicePort `yaml:"ports"`
	} `yaml:"spec"`
}

// workloadDoc is a workload's pod template, and a CronJob's under its
// jobTemplate.
type workloadDoc struct {
	Spec struct {
		Template    podTemplate `yaml:"template"`
		JobTemplate struct {
			Spec struct {
				Template podTemplate `yaml:"template"`
			} `yaml:"spec"`
		} `yaml:"jobTemplate"`
	} `yaml:"spec"`
}

type podTemplate struct {
	Metadata struct {
		Labels map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
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

// workloads are the kinds whose pod templates carry a star's DSNs. World is
// the Forge's own CR that renders a star's Deployment (narcissus since flux
// 6a559a4); only its spec.template is read — its spec.ports is a map of
// peers, not a Service's list.
var workloads = map[string]bool{
	"Deployment": true, "StatefulSet": true, "DaemonSet": true,
	"Job": true, "CronJob": true, "World": true,
}

// containers is every container a workload object runs, init ones included.
func (m manifest) containers() []container {
	var out []container
	for _, t := range m.Pods {
		out = append(out, t.Spec.Containers...)
		out = append(out, t.Spec.InitContainers...)
	}
	return out
}

// documents decodes every YAML document in one file, KIND FIRST: a spec is
// decoded only for a Service or a workload, so another kind's spec — a
// World's map-shaped ports, any CR's — can never fail the read. An empty
// document is skipped.
func documents(body string) ([]manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader([]byte(body)))
	var out []manifest
	for {
		var node yaml.Node
		err := dec.Decode(&node)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		var h head
		if err := node.Decode(&h); err != nil {
			return nil, err
		}
		if h.Kind == "" {
			continue
		}
		m := manifest{APIVersion: h.APIVersion, Kind: h.Kind, Name: h.Metadata.Name, Namespace: h.Metadata.Namespace}
		switch {
		case h.Kind == "Service":
			var svc serviceDoc
			if err := node.Decode(&svc); err != nil {
				return nil, fmt.Errorf("the Service %s: %w", h.Metadata.Name, err)
			}
			m.Ports = svc.Spec.Ports
			m.Type, m.ExternalName = svc.Spec.Type, svc.Spec.ExternalName
			m.NoGateway = strings.TrimSpace(h.Metadata.Annotations[NoGatewayAnnotation])
		case h.Kind == "Kustomization":
			var k kustomizationDoc
			_ = node.Decode(&k) // a Kustomization whose spec does not read places nothing
			m.Path, m.TargetNamespace = k.Spec.Path, k.Spec.TargetNamespace
		case workloads[h.Kind]:
			var w workloadDoc
			if err := node.Decode(&w); err != nil {
				return nil, fmt.Errorf("the %s %s: %w", h.Kind, h.Metadata.Name, err)
			}
			m.Pods = []podTemplate{w.Spec.Template, w.Spec.JobTemplate.Spec.Template}
			if m.Star = h.Metadata.Labels[starLabel]; m.Star == "" {
				m.Star = w.Spec.Template.Metadata.Labels[starLabel]
			}
		}
		out = append(out, m)
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
