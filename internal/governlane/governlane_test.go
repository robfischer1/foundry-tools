package governlane

import (
	"reflect"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/buildlane"
)

const stocks = "http://ourea.prime.svc.cluster.local:8215/foundry/foundry-stocks.git"

// diesToml is foundry-stocks' dies.toml in the part this lane reads: the three
// governance path dies, a kit die and a blades die beside them.
const diesToml = `
[dies."governance/forge-root"]
source  = "https://git.notusmi.com/foundry/foundry-stocks.git@main"
path    = "renders/forge-root/claude"

[dies."governance/vault"]
source  = "https://git.notusmi.com/foundry/foundry-stocks.git@main"
path    = "renders/vault/claude"

[dies."governance/home"]
source  = "https://git.notusmi.com/foundry/foundry-stocks.git@main"
path    = "renders/home/claude"

[dies."repo-gov/code-repo-sdd"]
kit = "code-repo-sdd"

[dies."blades/reference"]
source  = "https://git.notusmi.com/foundry/foundry-stocks.git@main"
path    = "blades/reference"
exclude = ["**/README.md"]
`

// with replaces the vault stanza, which every refusal test bends.
func with(vault string) string {
	return strings.Replace(diesToml, `[dies."governance/vault"]
source  = "https://git.notusmi.com/foundry/foundry-stocks.git@main"
path    = "renders/vault/claude"`, vault, 1)
}

func TestThePlanIsTheAskedConsumersInTheOrderAsked(t *testing.T) {
	got, err := Plan(diesToml, []string{"vault", "forge-root"}, stocks)
	if err != nil {
		t.Fatal(err)
	}
	want := []Die{
		{Consumer: "vault", Path: "renders/vault/claude"},
		{Consumer: "forge-root", Path: "renders/forge-root/claude"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestTheDefaultConsumersAreForgeRootAndVault(t *testing.T) {
	got, err := Plan(diesToml, DefaultConsumers, stocks)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Consumer != "forge-root" || got[1].Consumer != "vault" {
		t.Fatalf("got %+v", got)
	}
}

func TestAConsumerAskedTwiceIsCastOnce(t *testing.T) {
	got, err := Plan(diesToml, []string{"vault", "vault"}, stocks)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestADieNamesItsChannel(t *testing.T) {
	d := Die{Consumer: "forge-root", Path: "renders/forge-root/claude"}
	if d.Name() != "governance/forge-root" {
		t.Errorf("name %q", d.Name())
	}
	c := d.Cast()
	if c.Artifact() != "runtime-gov/governance.forge-root:stable" {
		t.Errorf("artifact %q", c.Artifact())
	}
}

// The hold is in the code: home is refused by name, whether or not dies.toml
// holds a perfectly good stanza for it, and whatever else is asked beside it.
func TestHomeIsHeldWhateverTheRequestSays(t *testing.T) {
	for _, ask := range [][]string{{"home"}, {"vault", "home"}, {"home", "vault"}} {
		got, err := Plan(diesToml, ask, stocks)
		if err == nil || got != nil {
			t.Fatalf("%v: planned %+v", ask, got)
		}
		if !strings.Contains(err.Error(), "governance/home is held") || !strings.Contains(err.Error(), "personal data") {
			t.Errorf("%v: %v", ask, err)
		}
	}
}

func TestAPlanRefuses(t *testing.T) {
	cases := []struct {
		name      string
		toml      string
		consumers []string
		self      string
		want      string
	}{
		{"no repository", diesToml, []string{"vault"}, "", "constructed on no repository"},
		{"an unparseable repository", diesToml, []string{"vault"}, "http://[::1", "constructed on no repository"},
		{"no consumer", diesToml, nil, stocks, "no consumer was asked for"},
		{"bad toml", "[dies", []string{"vault"}, stocks, "dies.toml does not parse"},
		{"an uppercase name", diesToml, []string{"Vault"}, stocks, `"Vault" is not a consumer name`},
		{"a slash", diesToml, []string{"a/b"}, stocks, `"a/b" is not a consumer name`},
		{"a dot", diesToml, []string{"a.b"}, stocks, `"a.b" is not a consumer name`},
		{"a leading dash", diesToml, []string{"-a"}, stocks, `"-a" is not a consumer name`},
		{"an empty name", diesToml, []string{""}, stocks, `"" is not a consumer name`},
		{"no stanza", diesToml, []string{"nomos"}, stocks, `no [dies."governance/nomos"] stanza`},
		{"a kit die", with("[dies.\"governance/vault\"]\nkit = \"vault\""), []string{"vault"}, stocks, "is not a path die"},
		{"a kit beside a path", with("[dies.\"governance/vault\"]\nkit = \"vault\"\nsource = \"https://git.notusmi.com/foundry/foundry-stocks.git@main\"\npath = \"renders/vault/claude\""), []string{"vault"}, stocks, "is not a path die"},
		{"no source", with("[dies.\"governance/vault\"]\npath = \"renders/vault/claude\""), []string{"vault"}, stocks, "is not a path die"},
		{"no path", with("[dies.\"governance/vault\"]\nsource = \"https://git.notusmi.com/foundry/foundry-stocks.git@main\""), []string{"vault"}, stocks, "is not a path die"},
		{"excludes", with("[dies.\"governance/vault\"]\nsource = \"https://git.notusmi.com/foundry/foundry-stocks.git@main\"\npath = \"renders/vault/claude\"\nexclude = [\"x\"]"), []string{"vault"}, stocks, "carries excludes"},
		{"another source", with("[dies.\"governance/vault\"]\nsource = \"https://git.notusmi.com/rob/other.git@main\"\npath = \"renders/vault/claude\""), []string{"vault"}, stocks, "is sourced from rob/other, not from this repository (foundry/foundry-stocks)"},
		{"another consumer's render", with("[dies.\"governance/vault\"]\nsource = \"https://git.notusmi.com/foundry/foundry-stocks.git@main\"\npath = \"renders/forge-root/claude\""), []string{"vault"}, stocks, "not a subtree of renders/vault/"},
		{"the consumer's directory itself", with("[dies.\"governance/vault\"]\nsource = \"https://git.notusmi.com/foundry/foundry-stocks.git@main\"\npath = \"renders/vault\""), []string{"vault"}, stocks, "not a subtree of renders/vault/"},
		{"a path that climbs", with("[dies.\"governance/vault\"]\nsource = \"https://git.notusmi.com/foundry/foundry-stocks.git@main\"\npath = \"renders/vault/../forge-root/claude\""), []string{"vault"}, stocks, "not a subtree of renders/vault/"},
		{"an absolute path", with("[dies.\"governance/vault\"]\nsource = \"https://git.notusmi.com/foundry/foundry-stocks.git@main\"\npath = \"/renders/vault/claude\""), []string{"vault"}, stocks, "not a subtree of renders/vault/"},
		{"an unclean path", with("[dies.\"governance/vault\"]\nsource = \"https://git.notusmi.com/foundry/foundry-stocks.git@main\"\npath = \"renders/vault/claude/\""), []string{"vault"}, stocks, "not a subtree of renders/vault/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Plan(c.toml, c.consumers, c.self)
			if err == nil || got != nil {
				t.Fatalf("planned %+v", got)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %q, want it to name %q", err, c.want)
			}
		})
	}
}

// A source may pin any ref, and the lane's URL may be the door's or the public
// one: the repository is what matches.
func TestASourceMatchesTheTreeInHandByRepository(t *testing.T) {
	pinned := with("[dies.\"governance/vault\"]\nsource = \"https://git.notusmi.com/foundry/foundry-stocks.git@v0.208.0\"\npath = \"renders/vault/claude\"")
	for _, self := range []string{stocks, "https://git.notusmi.com/foundry/foundry-stocks.git", "https://git.notusmi.com/foundry/foundry-stocks"} {
		if _, err := Plan(pinned, []string{"vault"}, self); err != nil {
			t.Errorf("%s: %v", self, err)
		}
	}
}

func TestRepoOf(t *testing.T) {
	for raw, want := range map[string]string{
		"https://git.notusmi.com/foundry/foundry-stocks.git@main": "foundry/foundry-stocks",
		"https://git.notusmi.com/foundry/foundry-stocks.git":      "foundry/foundry-stocks",
		"https://git.notusmi.com/foundry/foundry-stocks":          "foundry/foundry-stocks",
		"http://door:8215/nomos.git":                              "nomos",
		"https://user@git.notusmi.com/foundry/x.git@main":         "foundry/x",
		"https://git.notusmi.com/":                                "",
		"":                                                        "",
		"http://[::1":                                             "",
	} {
		if got := RepoOf(raw); got != want {
			t.Errorf("RepoOf(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestTheLaneSettlesOnTheWorstDie(t *testing.T) {
	clean := Outcome{Die: "governance/forge-root", Code: buildlane.Clean, Reason: "clean: cast a"}
	found := Outcome{Die: "governance/vault", Code: buildlane.Findings, Reason: "findings: b"}
	stuck := Outcome{Die: "governance/other", Code: buildlane.CouldNotRun, Reason: "could not run: c"}

	code, why := Fold([]Outcome{clean})
	if code != buildlane.Clean || why != "governance/forge-root: clean: cast a" {
		t.Errorf("one clean die: %d %q", code, why)
	}
	code, why = Fold([]Outcome{clean, found})
	if code != buildlane.Findings || why != "governance/forge-root: clean: cast a; governance/vault: findings: b" {
		t.Errorf("clean then findings: %d %q", code, why)
	}
	// The order a die came in does not move the verdict.
	if code, _ = Fold([]Outcome{found, clean}); code != buildlane.Findings {
		t.Errorf("findings then clean: %d", code)
	}
	if code, _ = Fold([]Outcome{found, stuck, clean}); code != buildlane.CouldNotRun {
		t.Errorf("could-not-run outranks findings: %d", code)
	}
	if code, _ = Fold([]Outcome{stuck, found}); code != buildlane.CouldNotRun {
		t.Errorf("could-not-run first: %d", code)
	}
}

func TestACastOfNothingIsNotClean(t *testing.T) {
	code, why := Fold(nil)
	if code != buildlane.CouldNotRun || !strings.Contains(why, "no governance die was cast") {
		t.Errorf("%d %q", code, why)
	}
}
