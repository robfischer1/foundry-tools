// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

const renovateConfigBody = "module.exports = {\n  platform: 'forgejo',\n};\n"

var renovateConfigTree = map[string]string{"config.js": renovateConfigBody}

func renovateVersionProbe() {
	engine.stdout(`"renovate","--version"`, checks.RenovateTag+"\n")
}

// THE VALIDATOR IS THE DEPLOYED IMAGE'S OWN: the pinned renovate image, strict, on config.js.
func TestRenovateConfigRunsTheDeployedImagesValidatorStrict(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(renovateConfigTree))
	renovateVersionProbe()
	wantState(t, runAtom(t, "fleet:renovate-config", ""), 0)

	c := engine.chain(`renovate-config-validator`)
	wantCalls(t, c,
		[]string{"from", checks.ImageRenovate},
		[]string{"withExec", "expect:ANY", `"renovate-config-validator","--strict","config.js"`},
	)
	if p := engine.chain(`"renovate","--version"`); !strings.Contains(p, checks.ImageRenovate) {
		t.Errorf("the version probe must run in the pinned image:\n%s", p)
	}
	if strings.Contains(c, "npm") || strings.Contains(c, "npx") {
		t.Errorf("the validator must come from the pinned image, not an install:\n%s", c)
	}
}

// 0 valid, 1 invalid: the validator's exit code is the verdict.
func TestRenovateConfigPassesTheValidatorsExitCodeThrough(t *testing.T) {
	for code, want := range map[int]int{0: 0, 1: 1} {
		engine.reset()
		engine.withTree(fleetTree(renovateConfigTree))
		renovateVersionProbe()
		engine.exitCode(`"renovate-config-validator"`, code)
		wantState(t, runAtom(t, "fleet:renovate-config", ""), want)
	}
}

// An image that answers another version is not the deployed validator.
func TestRenovateConfigCannotRunOnAnImageOfAnotherVersion(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(renovateConfigTree))
	engine.stdout(`"renovate","--version"`, "42.99.0\n")
	wantState(t, runAtom(t, "fleet:renovate-config", ""), 2, "CANNOT RUN", "42.99.0", checks.RenovateTag)
}

func TestRenovateConfigIsAbsentWithoutAConfigJS(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(nil))
	wantState(t, runAtom(t, "fleet:renovate-config", ""), 0, "ABSENT", "config.js")
	fleetNoContainer(t, "absence decided from the Directory")
}

// A config.js that is not a Renovate global config is some other repo's file.
func TestRenovateConfigIsAbsentOnAConfigJSThatIsNotRenovates(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{"config.js": "export default {};\n"}))
	wantState(t, runAtom(t, "fleet:renovate-config", ""), 0, "ABSENT", "not a Renovate")
	fleetNoContainer(t, "absence decided from the file")
}

func TestRenovateConfigRefusesATreeItCannotEnumerate(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail("{directory{entries}}", "the tree went away")
	wantState(t, runAtom(t, "fleet:renovate-config", ""), 2, "CANNOT RUN", "would not enumerate", "the tree went away")
}
