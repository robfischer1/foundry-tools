package buildlane

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// THE BOOT SMOKE: START THE IMAGE THE LANE JUST BUILT AND ASK IT TO ANSWER.
//
// Every other phase grades the tree or the image's bytes; none of them runs
// the image. calliope proved the gap on 2026-10-04: @forge/stellar-core-ts
// 0.17.0 read its identity core's wasm off disk relative to its module, the
// star ships as one bundled file, and the pod crash-looped in the cluster on
// ENOENT while every lane on the bump was green. Starting the image once,
// before anything is published, turns that into a red pull.
//
// OPT-IN, BY A LABEL THE STAR'S DOCKERFILE CARRIES. A star image needs its
// cluster to boot — a broker, a database, a SPIFFE socket — and only the star
// knows which of those it can run without and how. So the lane asks nothing
// of an image that does not declare SmokeLabel, and a star that does says
// exactly how to start it alone:
//
//	LABEL org.notusmi.smoke="port=8204 path=/mcp wait=8 env=CALLIOPE_MCP_BACKEND=fixture"
//
// port is required; path defaults to /, wait (seconds the image must stay up
// before it is asked) to DefaultSmokeWait, and env may repeat. ANY HTTP answer
// passes, a 404 or 405 included: the question is whether the process is up
// and serving, not whether that path is a health endpoint.

// SmokeLabel is the image label a star sets to opt into the boot smoke.
const SmokeLabel = "org.notusmi.smoke"

// DefaultSmokeWait is how long, in seconds, the image must stay up before the
// smoke asks it to answer, when the label names no wait.
const DefaultSmokeWait = 8

// MaxSmokeWait bounds the wait a label may ask for: the smoke is a boot check,
// not a soak, and it holds the build lane while it waits.
const MaxSmokeWait = 60

// Smoke is a star's declared boot smoke.
type Smoke struct {
	Port int
	Path string
	Wait int
	// Env is KEY=VALUE pairs, in the label's order.
	Env [][2]string
}

// ParseSmoke reads SmokeLabel's value. An empty value is no smoke (ok false,
// no error); a value that names one but cannot be read is an error, because a
// star that asked for a smoke and got none would read green for nothing.
func ParseSmoke(label string) (Smoke, bool, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return Smoke{}, false, nil
	}
	s := Smoke{Path: "/", Wait: DefaultSmokeWait}
	for _, field := range strings.Fields(label) {
		key, value, found := strings.Cut(field, "=")
		if !found || value == "" {
			return Smoke{}, false, fmt.Errorf("%s: %q is not key=value", SmokeLabel, field)
		}
		switch key {
		case "port":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 65535 {
				return Smoke{}, false, fmt.Errorf("%s: port %q is not a port", SmokeLabel, value)
			}
			s.Port = n
		case "path":
			if !strings.HasPrefix(value, "/") {
				return Smoke{}, false, fmt.Errorf("%s: path %q does not start with /", SmokeLabel, value)
			}
			s.Path = value
		case "wait":
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || n > MaxSmokeWait {
				return Smoke{}, false, fmt.Errorf("%s: wait %q is not 0-%d seconds", SmokeLabel, value, MaxSmokeWait)
			}
			s.Wait = n
		case "env":
			name, v, ok := strings.Cut(value, "=")
			if !ok || name == "" {
				return Smoke{}, false, fmt.Errorf("%s: env %q is not NAME=value", SmokeLabel, value)
			}
			s.Env = append(s.Env, [2]string{name, v})
		default:
			return Smoke{}, false, fmt.Errorf("%s: unknown key %q (port, path, wait, env)", SmokeLabel, key)
		}
	}
	if s.Port == 0 {
		return Smoke{}, false, fmt.Errorf("%s: no port to ask", SmokeLabel)
	}
	return s, true, nil
}

// serviceDied is the engine saying the image's own process ended before or
// while it served. Its text can carry "connection refused" (the health check
// that found nothing listening), which Failed would read as a network fault
// and file could-not-run — re-asked forever over an image that will never
// boot. An image that exited is the finding the smoke exists for.
var serviceDied = regexp.MustCompile(`(?i)exited|exit code|health ?check`)

// SmokeFailed is the verdict on a boot smoke whose service the engine could not
// keep up: findings when the image's process died, else what Failed says.
func SmokeFailed(output string) (int, string) {
	if serviceDied.MatchString(output) {
		return Findings, "findings in the boot smoke — the image exited instead of serving"
	}
	return Failed("the boot smoke", output)
}

// SmokeHost is the alias the image is bound under for the probe.
const SmokeHost = "smoke"

// ProbeScript is the python the probe container runs: wait, then ask once.
// Exit 0 on any HTTP answer, 1 on none — a refused or reset connection is the
// image having died (or never served) inside its wait.
func (s Smoke) ProbeScript() string {
	url := fmt.Sprintf("http://%s:%d%s", SmokeHost, s.Port, s.Path)
	return strings.Join([]string{
		"import sys, time, urllib.error, urllib.request",
		fmt.Sprintf("time.sleep(%d)", s.Wait),
		"try:",
		fmt.Sprintf("    r = urllib.request.urlopen(%q, timeout=10)", url),
		fmt.Sprintf("    print('smoke: %s answered %%d after %ds up' %% r.status)", url, s.Wait),
		"except urllib.error.HTTPError as e:",
		fmt.Sprintf("    print('smoke: %s answered %%d after %ds up' %% e.code)", url, s.Wait),
		"except Exception as e:",
		fmt.Sprintf("    print('smoke: %s did not answer after %ds: %%s' %% e)", url, s.Wait),
		"    sys.exit(1)",
	}, "\n") + "\n"
}
