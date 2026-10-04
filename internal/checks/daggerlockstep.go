package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// fleet:dagger-lockstep's judgement. Rob, 2026-10-04: "Bump CLI and engine in
// lockstep for dagger."
//
// THE PINS LIVE IN TWO REPOSITORIES. foundry/flux pins the engine four times
// (the dagger-helm chart, the engine DaemonSet's image.ref, and the gate Jobs'
// BUILD_JOB_IMAGE and LANE_CALL_IMAGE — a gate's CLI IS the engine image), and
// foundry/base-images pins the CLI a Chairman dials the engine with. Renovate
// cannot open one pull across two repositories, so the ENGINE LEADS: flux is
// the one place dagger is taken from upstream (renovate-config's fence), and
// the CLI follows what flux's main runs (its custom.dagger-engine datasource).
//
// This atom is the guard on both sides, and it can see both because the door
// answers flux's engine pin to any gate:
//
//   - in a tree that holds the engine source (flux), every engine pin must
//     name the same version and digest, and the chart must be that version —
//     a pull that moves one of the four alone is a finding;
//   - in any other tree, a CLI pin must EQUAL the engine flux's main runs, and
//     a dagger MODULE's engineVersion (a dagger.json with an sdk) must not be
//     newer than it — an engine refuses a module that asks for a later engine.
//
// A consumer's dagger.json (no sdk — the toolchain declaration every star
// carries) is not read: its engineVersion is the compatibility floor the
// engine serves, and lagging it is what upstream intends.

// DaggerEngineRepo and DaggerEngineSource are where the engine the fleet runs
// is pinned: the image.ref of the dagger-engine HelmRelease.
const (
	DaggerEngineRepo   = "foundry/flux"
	DaggerEngineSource = "forge/dagger-engine-helm.yaml"
)

// daggerPinRole says what a pinned file holds.
type daggerPinRole int

const (
	daggerEngine daggerPinRole = iota
	daggerCLI
)

// DaggerPinFile is one file the lockstep reads.
type DaggerPinFile struct {
	Path string
	role daggerPinRole
}

// DaggerPinFiles is every file outside a dagger.json that pins a dagger
// version. A pin file present in a tree that names no version is a FINDING:
// it means the spelling moved and this atom would otherwise compare nothing.
var DaggerPinFiles = []DaggerPinFile{
	{DaggerEngineSource, daggerEngine},
	{"prime/daedalus-jobs.yaml", daggerEngine},
	{"bases/layer-dagger-cli/Dockerfile", daggerCLI},
}

// DaggerModuleManifest is the file a dagger module (or consumer) declares.
const DaggerModuleManifest = "dagger.json"

var (
	daggerEngineRef  = regexp.MustCompile(`registry\.dagger\.io/engine:(v\d+\.\d+\.\d+)@(sha256:[0-9a-f]{64})`)
	daggerHelmChart  = regexp.MustCompile(`chart:\s*dagger-helm\s*\n\s*version:\s*"?(\d+\.\d+\.\d+)"?`)
	daggerCLIVersion = regexp.MustCompile(`(?m)^\s*ARG\s+DAGGER_VERSION=["']?(v?\d+\.\d+\.\d+)`)
)

// daggerPin is one version a file names.
type daggerPin struct {
	path, what, version, digest string
}

// DaggerLockstep judges the pins in files (path -> contents, only the paths
// the tree holds) against the engine, reading flux's main through door when
// the tree does not hold the engine source itself.
func DaggerLockstep(ctx context.Context, files map[string]string, door Door) (int, string) {
	const id = "fleet:dagger-lockstep"
	var engines, clis, modules []daggerPin
	var findings []string
	pinned := false

	for _, f := range DaggerPinFiles {
		body, ok := files[f.Path]
		if !ok {
			continue
		}
		pinned = true
		var found []daggerPin
		switch f.role {
		case daggerEngine:
			for _, m := range daggerEngineRef.FindAllStringSubmatch(body, -1) {
				found = append(found, daggerPin{f.Path, "engine image", m[1], m[2]})
			}
			for _, m := range daggerHelmChart.FindAllStringSubmatch(body, -1) {
				found = append(found, daggerPin{f.Path, "dagger-helm chart", "v" + m[1], ""})
			}
			engines = append(engines, found...)
		case daggerCLI:
			for _, m := range daggerCLIVersion.FindAllStringSubmatch(body, -1) {
				found = append(found, daggerPin{f.Path, "CLI (ARG DAGGER_VERSION)", vPrefixed(m[1]), ""})
			}
			clis = append(clis, found...)
		}
		if len(found) == 0 {
			findings = append(findings, fmt.Sprintf("%s: names no dagger version this atom can read, so the lockstep compared nothing there. The pin's spelling moved; move checks.DaggerPinFiles' patterns with it.", f.Path))
		}
	}

	if body, ok := files[DaggerModuleManifest]; ok {
		var manifest map[string]any
		if err := json.Unmarshal([]byte(body), &manifest); err != nil {
			return 2, fmt.Sprintf("%s: CANNOT RUN - %s did not parse: %v", id, DaggerModuleManifest, err)
		}
		if _, isModule := manifest["sdk"]; isModule {
			pinned = true
			v, _ := manifest["engineVersion"].(string)
			if !daggerSemver(v) {
				findings = append(findings, fmt.Sprintf("%s: a module with no readable engineVersion (%q); an engine cannot tell which API it was written against.", DaggerModuleManifest, v))
			} else {
				modules = append(modules, daggerPin{DaggerModuleManifest, "module engineVersion", v, ""})
			}
		}
	}

	if !pinned {
		return 0, id + ": ABSENT - this tree pins no dagger CLI, engine or module, so there is nothing to hold in lockstep"
	}

	// THE ENGINE. The tree's own source when it holds one (a pull to flux is
	// judged on what it proposes), otherwise flux's main through the door.
	// The FIRST engine image in the source is the engine; any other pin,
	// including a second image in the same file, is held to it.
	engine, digest, from := "", "", ""
	for _, p := range engines {
		if engine == "" && p.path == DaggerEngineSource && p.what == "engine image" {
			engine, digest, from = p.version, p.digest, p.path+" (this tree)"
		}
	}
	switch {
	case len(engines) > 0 && engine == "":
		findings = append(findings, fmt.Sprintf("this tree pins the dagger engine but %s names no engine image, so there is no engine to hold the other pins to.", DaggerEngineSource))
	case len(engines) == 0:
		at := door.URL(DaggerEngineRepo, DaggerEngineSource)
		status, body, err := door.Get(ctx, DaggerEngineRepo, DaggerEngineSource)
		switch {
		case err != nil:
			return 2, fmt.Sprintf("%s: CANNOT RUN - the door is unreachable (%s): %v. An engine version that could not be read is not one that agrees.", id, at, err)
		case status != http.StatusOK:
			return 2, fmt.Sprintf("%s: CANNOT RUN - the door answered HTTP %d for %s. An engine version that could not be read is not one that agrees.", id, status, at)
		}
		m := daggerEngineRef.FindStringSubmatch(string(body))
		if m == nil {
			return 2, fmt.Sprintf("%s: CANNOT RUN - %s answered no registry.dagger.io/engine:<version>@<digest> pin, so the engine's version is unknown.", id, at)
		}
		engine, from = m[1], DaggerEngineRepo+" main "+DaggerEngineSource
	}

	var agree int
	if engine != "" {
		for _, p := range engines {
			switch {
			case p.version != engine:
				findings = append(findings, fmt.Sprintf("%s: %s %s, but the engine is %s (%s). The four engine pins move in one pull.", p.path, p.what, p.version, engine, from))
			case p.digest != "" && p.digest != digest:
				findings = append(findings, fmt.Sprintf("%s: %s %s@%s, but %s pins the same tag at %s. One version, one digest.", p.path, p.what, p.version, p.digest, DaggerEngineSource, digest))
			default:
				agree++
			}
		}
		for _, p := range clis {
			if p.version != engine {
				findings = append(findings, fmt.Sprintf("%s: %s %s, but the engine is %s (%s). The CLI follows the engine: Renovate's custom.dagger-engine datasource offers this bump once the engine has landed.", p.path, p.what, p.version, engine, from))
			} else {
				agree++
			}
		}
		for _, p := range modules {
			if daggerNewer(p.version, engine) {
				findings = append(findings, fmt.Sprintf("%s: %s %s is newer than the engine %s (%s); the engine refuses a module that asks for a later engine. Land the engine first.", p.path, p.what, p.version, engine, from))
			} else {
				agree++
			}
		}
	}

	if len(findings) > 0 {
		sort.Strings(findings)
		return 1, strings.Join(append([]string{fmt.Sprintf("%s: %d pin(s) out of lockstep", id, len(findings))}, findings...), "\n")
	}
	return 0, fmt.Sprintf("%s: %d pin(s) agree with the engine %s (%s)", id, agree, engine, from)
}

// vPrefixed spells a version the way the engine's tag does.
func vPrefixed(v string) string {
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// daggerSemver reports whether v is a vMAJOR.MINOR.PATCH.
func daggerSemver(v string) bool {
	return daggerParts(v) != nil
}

// daggerParts answers v's three numbers, or nil when v is not daggerSemver.
func daggerParts(v string) []int {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if !strings.HasPrefix(v, "v") || len(parts) != 3 {
		return nil
	}
	var out []int
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil
		}
		out = append(out, n)
	}
	return out
}

// daggerNewer reports whether a is a later version than b. Both are
// daggerSemver; an unreadable one is never newer.
func daggerNewer(a, b string) bool {
	// An unreadable version is nil, and nil is never the length of a readable
	// one; two unreadable ones compare no parts at all.
	pa, pb := daggerParts(a), daggerParts(b)
	if len(pa) != len(pb) {
		return false
	}
	for i := range pa {
		if pa[i] > pb[i] {
			return true
		}
		if pa[i] < pb[i] {
			return false
		}
	}
	return false
}
