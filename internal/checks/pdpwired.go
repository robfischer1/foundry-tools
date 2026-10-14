package checks

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// fleet:pdp-wired's judgement (foundry-tools#17788).
//
// THE GAP IT CLOSES WAS DEPLOYMENT CONFIG, NOT CODE. stellar-core-go's endpoint
// PEP (mcpserve.Door arms it from STELLAR_PEP_MODE) asks the policy plane for a
// decision only when STELLAR_PDP_URL is set. Unset, it records
// would_consult{pdp_not_consulted} and in witness mode never refuses, so a star
// armed with the PEP and no URL looks healthy while consulting nothing. themis
// was one of ten such stars (dispatch 410, flux 267f8522) and nothing caught it
// for days.
//
// THE RULE, READ OFF THE FLUX TREE ALONE. A container in prime/star-*.yaml that
// sets STELLAR_PEP_MODE to anything but "off" has armed the PEP, and must also
// set STELLAR_PDP_URL in the SAME container (env is per container). The tree
// has no go.mod to ask "does this star vendor the PEP", but the manifest is the
// side that decides whether the PEP runs at all: mode unset means off.
//
// IT DECODES THE YAML RATHER THAN GREPPING. These manifests carry long comments
// that name both variables, and a text scan would count the prose as wiring.
// A valueFrom entry counts as set (a ConfigMap or Secret key supplies it); an
// empty literal value does not.

// PDPWiredDir and PDPWiredPrefix select the manifests this atom reads.
const (
	PDPWiredDir    = "prime"
	PDPWiredPrefix = "star-"
	PDPWiredSuffix = ".yaml"

	envPEPMode = "STELLAR_PEP_MODE"
	envPDPURL  = "STELLAR_PDP_URL"
)

// IsPDPWiredFile reports whether path is one of the manifests this atom grades.
func IsPDPWiredFile(path string) bool {
	rest, ok := strings.CutPrefix(path, PDPWiredDir+"/"+PDPWiredPrefix)
	return ok && strings.HasSuffix(rest, PDPWiredSuffix) && !strings.Contains(rest, "/")
}

// PDPWired judges files (path -> contents of every prime/star-*.yaml). State 0
// when every armed container is wired, 1 naming each that is not, 2 when a
// manifest would not parse (a file never read is a file never judged).
func PDPWired(files map[string]string) (int, string) {
	const id = "fleet:pdp-wired"
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var armed int
	var missing []string
	for _, p := range paths {
		containers, err := envContainers(files[p])
		if err != nil {
			return int(StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %s would not parse: %v", id, p, err)
		}
		for _, c := range containers {
			mode, has := c.env[envPEPMode]
			if !has || mode.literal && strings.TrimSpace(mode.value) == "off" {
				continue
			}
			armed++
			if url, ok := c.env[envPDPURL]; !ok || (url.literal && strings.TrimSpace(url.value) == "") {
				missing = append(missing, fmt.Sprintf("%s (container %s)", p, c.name))
			}
		}
	}
	if len(missing) == 0 {
		return int(StatePass), fmt.Sprintf("%s: all %d container(s) that arm the PEP (%s) set %s", id, armed, envPEPMode, envPDPURL)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d of %d container(s) arm the PEP (%s) with no %s - the PEP records would_consult{pdp_not_consulted} and consults nothing:\n",
		id, len(missing), armed, envPEPMode, envPDPURL)
	for _, m := range missing {
		fmt.Fprintf(&b, "  %s\n", m)
	}
	fmt.Fprintf(&b, "set %s (the policy plane's decision URL, as the wired stars carry it) in each, or set %s off.", envPDPURL, envPEPMode)
	return int(StateFindings), b.String()
}

type envVar struct {
	value   string
	literal bool // false when the value comes from valueFrom
}

type envContainer struct {
	name string
	env  map[string]envVar
}

// envContainers decodes every document and returns each container (in any
// workload, found by walking, so a CronJob or StatefulSet is read as a
// Deployment is) that declares an env list.
func envContainers(body string) ([]envContainer, error) {
	dec := yaml.NewDecoder(strings.NewReader(body))
	var out []envContainer
	for {
		var doc any
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return nil, err
		}
		walkContainers(doc, &out)
	}
}

func walkContainers(n any, out *[]envContainer) {
	switch v := n.(type) {
	case map[string]any:
		if env, ok := v["env"].([]any); ok {
			c := envContainer{name: fmt.Sprint(v["name"]), env: map[string]envVar{}}
			for _, e := range env {
				m, ok := e.(map[string]any)
				if !ok {
					continue
				}
				name, _ := m["name"].(string)
				if name == "" {
					continue
				}
				if _, from := m["valueFrom"]; from {
					c.env[name] = envVar{}
					continue
				}
				val := ""
				if s, ok := m["value"]; ok && s != nil {
					val = fmt.Sprint(s)
				}
				c.env[name] = envVar{value: val, literal: true}
			}
			*out = append(*out, c)
		}
		for _, child := range v {
			walkContainers(child, out)
		}
	case []any:
		for _, child := range v {
			walkContainers(child, out)
		}
	}
}
