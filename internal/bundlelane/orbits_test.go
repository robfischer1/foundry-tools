package bundlelane

import (
	"slices"
	"testing"
)

func TestOrbitsPublishOnAContractChangeOrWhenItCannotTell(t *testing.T) {
	for name, tc := range map[string]struct {
		changed []string
		want    bool
	}{
		"cannot tell":     {nil, true},
		"a contract":      {[]string{"policy/x.rego", "orbits/chaos-nyx.toml"}, true},
		"the readme":      {[]string{"orbits/README.md"}, true},
		"elsewhere":       {[]string{"policy/x.rego", "fleet/data.json"}, false},
		"orbits mid-path": {[]string{"docs/orbits/x.toml"}, false},
	} {
		if got := OrbitsPublish(tc.changed); got != tc.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestTreePushArgsTypesEveryFileAndAnnotatesTheSource(t *testing.T) {
	files := []string{"b.toml", "a.toml"}
	got := TreePushArgs(OrbitsDie+":g1234567", "abc", files)
	want := []string{"oras", "push", "--registry-config", "/run/docker/config.json", "foundry.notusmi.com/data/orbits:g1234567",
		"--artifact-type", "application/vnd.hephaestus.die.v1",
		"--annotation", "org.notusmi.die.source-sha=abc",
		"--annotation", "org.opencontainers.image.revision=abc",
		"a.toml:application/octet-stream", "b.toml:application/octet-stream"}
	if !slices.Equal(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if !slices.Equal(files, []string{"b.toml", "a.toml"}) {
		t.Error("the caller's slice was sorted in place")
	}
	if ContractsDie != "foundry.notusmi.com/data/contracts" {
		t.Error(ContractsDie)
	}
}
