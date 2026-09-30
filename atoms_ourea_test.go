// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// THE ATOM HAS TWO WAYS TO BE WRONG AND THEY COST DIFFERENT AMOUNTS.
//
// A FALSE RED costs a flux author a confused minute, and this atom has a live
// source of them: flux's ConfigMap NAMES 7 of the 11 retired keys in its own
// comments, every one inside prose explaining why that key went. A text scan
// would red a clean file seven times. Most of this file is about that.
//
// A FALSE GREEN costs the thing the atom exists for. ourea warns at boot and
// carries on; that warning is the only other reader, and the reason a boot
// REFUSAL was refused is that the code lands ahead of the ConfigMap edit by
// minutes to days, so a refusal makes each of those windows a fleet-wide
// outage. This check is the replacement guarantee — so it must never report a
// pass over a ConfigMap it could not read, or grade one against a list it could
// not fetch.

// oureaRetiredList is what ourea publishes, trimmed to the keys these tests
// exercise. The real file carries eleven.
const oureaRetiredList = `{
  "gate_module_pin": "the door resolves gate_module_repo's main at dispatch; set gate_module / gate_module_repo instead and delete this key",
  "record_volume_size": "there is no record volume to size — delete this key",
  "pin_hook_url": "the Forgejo ` + "`issues`" + ` webhook is gone — delete this key"
}`

const oureaListPath = "/ourea/internal/config/retired-keys.json"

// configMap wraps a TOML body the way flux's file does: a ConfigMap whose
// data["config.toml"] is a block scalar.
func configMap(body string) string {
	var b strings.Builder
	b.WriteString("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: ourea-config\n  namespace: prime\ndata:\n  config.toml: |\n")
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		b.WriteString("    " + line + "\n")
	}
	return b.String()
}

// fluxTree is a tree that looks like the fleet's flux checkout to this atom,
// with ourea's published list seeded beside it.
func fluxTree(toml string) map[string]string {
	return fleetTree(map[string]string{
		"infrastructure/ourea-config.yaml": configMap(toml),
		oureaListPath:                      oureaRetiredList,
	})
}

const cleanConfig = `forgejo_url = "http://forgejo:30142"
gate_job_namespace = "prime"
gate_job_env = "ca-gate-env"
`

// ---- absence: this atom is one repository's business ----

func TestOureaConfigIsAbsentWithNoInfrastructureDirectory(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(nil))
	wantState(t, runAtom(t, "fleet:ourea-config-retired-keys", ""), 0,
		"ABSENT", "not the fleet's flux tree")
	fleetNoContainer(t, "absence decided from the Directory")
}

// A tree with an infrastructure/ directory that is not flux's — every repo may
// have one — is still not this atom's business.
func TestOureaConfigIsAbsentWhenInfrastructureHoldsNoOureaConfig(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{"infrastructure/something-else.yaml": "kind: Service\n"}))
	wantState(t, runAtom(t, "fleet:ourea-config-retired-keys", ""), 0,
		"ABSENT", "infrastructure/ourea-config.yaml")
	fleetNoContainer(t, "absence decided from the Directory")
}

// ---- the pass, and what it is allowed to forgive ----

func TestOureaConfigHoldsWhenItNamesNoRetiredKey(t *testing.T) {
	engine.reset()
	engine.withTree(fluxTree(cleanConfig))
	v := runAtom(t, "fleet:ourea-config-retired-keys", "")
	wantState(t, v, 0)
	if v.Result != "pass" {
		t.Errorf("a read, clean ConfigMap should PASS, not %q: %s", v.Result, v.Reason)
	}
}

// THE FALSE RED THIS ATOM WAS BUILT AROUND. flux's real ConfigMap is 28 set
// keys, none retired, and 7 of the 11 retired names appear in its comments
// anyway — each inside prose explaining why that key went. Every line below is
// lifted from the shape of that file. A text scan reds all of them.
func TestARetiredKeyNamedOnlyInACommentIsNotAFinding(t *testing.T) {
	engine.reset()
	engine.withTree(fluxTree(`# There is no pin to declare here — gate_module_pin was retired with the
# declared-pin apparatus, and a copy of that key is a WARN at boot.
forgejo_url = "http://forgejo:30142"
# record_volume_class, record_volume_size and record_collect_image went with
# the per-run volume.
gate_job_namespace = "prime"
#pin_hook_url = "http://casper:9000/pin"
`))
	v := runAtom(t, "fleet:ourea-config-retired-keys", "")
	wantState(t, v, 0)
	if v.Result != "pass" {
		t.Errorf("three retired names in comments red a clean file: %s", v.Reason)
	}
}

// A key of the same name under a table is a DIFFERENT key, and ourea's retired
// ones are all top-level. Claiming this one would be a finding nobody can fix
// by deleting what the finding names.
func TestARetiredNameInsideATableIsNotTheRetiredKey(t *testing.T) {
	engine.reset()
	engine.withTree(fluxTree("forgejo_url = \"http://forgejo:30142\"\n\n[hooks]\npin_hook_url = \"http://casper:9000/pin\"\n"))
	wantState(t, runAtom(t, "fleet:ourea-config-retired-keys", ""), 0)
}

// ---- the finding ----

func TestOureaConfigRefusesASetRetiredKey(t *testing.T) {
	engine.reset()
	engine.withTree(fluxTree(cleanConfig + "pin_hook_url = \"http://casper:9000/pin\"\n"))
	wantState(t, runAtom(t, "fleet:ourea-config-retired-keys", ""), 1,
		"pin_hook_url", "1 key(s) the door no longer reads")
}

// The finding carries ourea's OWN sentence about the key, because "this key is
// retired" alone leaves the author guessing whether something replaced it. It
// also has to say the line is inert — an author who reads "retired" as
// "broken" reaches for a rollback instead of a delete.
func TestTheFindingCarriesOureasReasonAndTheFix(t *testing.T) {
	engine.reset()
	engine.withTree(fluxTree(cleanConfig + "gate_module_pin = \"foundry-tools@f0566e9b\"\n"))
	out := runAtom(t, "fleet:ourea-config-retired-keys", "").Reason
	for _, want := range []string{
		"gate_module_pin",                    // the key
		"set gate_module / gate_module_repo", // ourea's own guidance, carried verbatim
		"INERT",                              // what it is not
		"FIX: delete the named key",          // what to do
		"internal/config/retired-keys.json",  // where the list came from
	} {
		if !strings.Contains(out, want) {
			t.Errorf("finding does not mention %q:\n%s", want, out)
		}
	}
}

// Several keys are reported together and in a stable order — an author fixing
// a ConfigMap wants the whole list in one pass, and a set's iteration order is
// not a list.
func TestSeveralRetiredKeysAreReportedTogetherAndSorted(t *testing.T) {
	engine.reset()
	engine.withTree(fluxTree(cleanConfig +
		"record_volume_size = \"1Gi\"\npin_hook_url = \"http://casper:9000/pin\"\ngate_module_pin = \"x\"\n"))
	out := runAtom(t, "fleet:ourea-config-retired-keys", "").Reason
	if !strings.Contains(out, "3 key(s)") {
		t.Errorf("the count is wrong:\n%s", out)
	}
	i, j, k := strings.Index(out, "gate_module_pin"), strings.Index(out, "pin_hook_url"), strings.Index(out, "record_volume_size")
	if !(i < j && j < k) {
		t.Errorf("the keys are not sorted (%d, %d, %d):\n%s", i, j, k, out)
	}
}

// ---- the CANNOT RUN paths ----
//
// EVERY ONE OF THESE WOULD OTHERWISE BE A PASS, which is the only reason they
// are worth this many tests. The atom's whole job is to be the reader that
// ourea's boot-time WARN is not, so an atom that cannot read has not done it.

func TestOureaConfigRefusesATreeItCannotEnumerate(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail("{directory{entries}}", "the tree went away")
	wantState(t, runAtom(t, "fleet:ourea-config-retired-keys", ""), 2,
		"CANNOT RUN", "would not enumerate", "the tree went away")
}

func TestOureaConfigRefusesAnInfrastructureDirectoryItCannotList(t *testing.T) {
	engine.reset()
	engine.withTree(fluxTree(cleanConfig))
	engine.fail(`directory(path:"infrastructure")`, "i/o error")
	wantState(t, runAtom(t, "fleet:ourea-config-retired-keys", ""), 2,
		"CANNOT RUN", "would not enumerate", "i/o error")
}

func TestOureaConfigRefusesAConfigMapItCannotRead(t *testing.T) {
	engine.reset()
	engine.withTree(fluxTree(cleanConfig))
	engine.fail(`file(path:"infrastructure/ourea-config.yaml")`, "i/o error")
	wantState(t, runAtom(t, "fleet:ourea-config-retired-keys", ""), 2,
		"CANNOT RUN", "would not read", "i/o error")
}

// A ConfigMap that is not the shape this atom reads is a check that did not
// run, never a ConfigMap with no retired keys in it.
func TestOureaConfigRefusesAFileItCannotParse(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"not yaml", "\tthis: [is\n  not: yaml\n", "not YAML"},
		{"no config.toml", "apiVersion: v1\nkind: ConfigMap\ndata:\n  other.toml: |\n    x = 1\n", `no data["config.toml"]`},
		{"not toml", configMap("this is not = = toml\n"), "not TOML"},
	} {
		t.Run(c.name, func(t *testing.T) {
			engine.reset()
			engine.withTree(fleetTree(map[string]string{
				"infrastructure/ourea-config.yaml": c.body,
				oureaListPath:                      oureaRetiredList,
			}))
			wantState(t, runAtom(t, "fleet:ourea-config-retired-keys", ""), 2,
				"CANNOT RUN", "would not parse", c.want)
		})
	}
}

// THE LIST IS THE CHECK. Without it the atom knows nothing about which keys are
// retired, and a pass here would be a green over an ungraded ConfigMap — the
// zero-file scan wearing a different hat.
func TestOureaConfigRefusesWhenOureasListWillNotRead(t *testing.T) {
	engine.reset()
	engine.withTree(fluxTree(cleanConfig))
	engine.fail(`file(path:"internal/config/retired-keys.json")`, "ourea is unreachable")
	wantState(t, runAtom(t, "fleet:ourea-config-retired-keys", ""), 2,
		"CANNOT RUN", "nothing says which keys are retired", "ourea is unreachable")
}

func TestOureaConfigRefusesAListItCannotUse(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"not an object of strings", `["gate_module_pin"]`, "did not decode"},
		{"empty", `{}`, "grading a ConfigMap against no keys is not a check"},
	} {
		t.Run(c.name, func(t *testing.T) {
			engine.reset()
			engine.withTree(fleetTree(map[string]string{
				"infrastructure/ourea-config.yaml": configMap(cleanConfig),
				oureaListPath:                      c.body,
			}))
			wantState(t, runAtom(t, "fleet:ourea-config-retired-keys", ""), 2,
				"CANNOT RUN", c.want)
		})
	}
}

// The list is read at a MOVING ref, for the reason DiesRepo gives: this grades
// a pull against what the door ignores TODAY, and a key retired after a pinned
// sha would be graded by nothing.
func TestTheListIsReadFromOureasMain(t *testing.T) {
	engine.reset()
	engine.withTree(fluxTree(cleanConfig))
	runAtom(t, "fleet:ourea-config-retired-keys", "")
	var saw bool
	for _, q := range engine.chains() {
		if strings.Contains(q, "retired-keys.json") {
			saw = true
			if !hasCall(q, "git", `"`+OureaRepo+`"`) {
				t.Errorf("the list is not read from ourea:\n%s", q)
			}
			if !hasCall(q, "ref", `"`+OureaRef+`"`) {
				t.Errorf("the list is not read at %s:\n%s", OureaRef, q)
			}
		}
	}
	if !saw {
		t.Error("the atom never read ourea's list at all")
	}
}
