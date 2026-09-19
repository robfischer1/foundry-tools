package checks

import (
	"reflect"
	"strings"
	"testing"
)

// THE PREDICATE IS THE ONE DEFINITION OF "A DOCKERFILE", so this table is
// where a spelling is admitted or refused. The rows are the fleet's own: the
// census of 2026-09-13 over every image star, the six bases, infra,
// fleet-crew and wrecksys.
func TestDockerfilePopulationAdmitsTheDocumentedSpellingsAndNothingElse(t *testing.T) {
	files := []string{
		"Dockerfile",
		"bases/go-ci/Dockerfile",
		"host-support/tf-runner/Dockerfile",
		"Dockerfile.arm64",
		"docker/serving/serving.Dockerfile",
		"build.dockerfile",
		"Containerfile",
		"Containerfile.dev",
		"ci.Containerfile",
		"./Dockerfile", // a Dagger glob may spell the root with a dot
		// Not Dockerfiles.
		"Dockerfile-old", // hyphenated: nothing documents it; wrecksys keeps two in a directory called trash
		"src/trash/Dockerfile-base",
		"dockerfile",  // the bare lowercase word is not a Docker default
		".dockerfile", // a hidden file with no stem in front of the dot
		"x/.Containerfile",
		"DockerfileREADME.md", // a prefix match is not a name match
		"docs/dockerfiles.rst",
		".dockerignore",
		"compose.yaml",
		"Makefile",
		"bases/", // a glob names a directory with a trailing slash
	}
	want := []string{
		"Dockerfile",
		"bases/go-ci/Dockerfile",
		"host-support/tf-runner/Dockerfile",
		"Dockerfile.arm64",
		"docker/serving/serving.Dockerfile",
		"build.dockerfile",
		"Containerfile",
		"Containerfile.dev",
		"ci.Containerfile",
		"./Dockerfile",
	}
	if got := DockerfilePopulation(files); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
	if got := DockerfilePopulation(nil); len(got) != 0 {
		t.Errorf("an empty tree is an empty surface, got %v", got)
	}
}

// A dotted suffix on the stem is a VARIANT — `Dockerfile.` + anything — and
// the clause is deliberately that wide. This pins the decision so a refactor
// that narrows it to an allowlist of variants (arm64, dev, …) is caught.
func TestIsDockerfileTreatsADottedSuffixAsAVariant(t *testing.T) {
	// A variant IS admitted: Dockerfile.arm64, Dockerfile.dev.
	for _, p := range []string{"Dockerfile.arm64", "Containerfile.dev", "x/Dockerfile.gpu"} {
		if !IsDockerfile(p) {
			t.Errorf("%s is a Dockerfile variant and must be linted", p)
		}
	}
	// The one measured collision — a Markdown document named after one — is
	// admitted too, and DELIBERATELY: hadolint reading a .md as a Dockerfile
	// reports a parse finding on line 1, which is the honest answer to a file
	// that claims the name. Nothing in the fleet tracks such a file (census
	// 2026-09-13); if one appears, the finding names it and the author
	// renames it.
	if !IsDockerfile("docs/Dockerfile.md") {
		t.Errorf("Dockerfile.md claims the name and is read as one — see the comment")
	}
}

// THE VENDORED DOCKERFILES ARE NOT THIS REPOSITORY'S. The predicate itself
// admits them (they ARE Dockerfiles); it is the gate population that drops
// them first, and this holds the two in the order the atom applies them.
func TestVendoredDockerfilesLeaveWithTheGatePopulation(t *testing.T) {
	files := []string{
		"Dockerfile",
		"vendor/go.opentelemetry.io/otel/dependencies.Dockerfile",
		"vendor/github.com/pjbgf/sha1cd/Dockerfile.arm64",
		"node_modules/x/Dockerfile",
	}
	got := DockerfilePopulation(GatePopulation(files))
	if want := []string{"Dockerfile"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v — vendor/ and node_modules/ are the fleet exclude's", got, want)
	}
	// And without the exclude they would all be read, which is the point of
	// applying it first.
	if got := DockerfilePopulation(files); len(got) != 4 {
		t.Errorf("the predicate alone admits every one of them (%d of 4) — the exclude is what drops vendor/", len(got))
	}
}

// THE FLEET'S RULESET, DECISION BY DECISION. Each row is one of the two
// measured departures from hadolint's defaults, and a change to any of them
// is a change to what every pull in the fleet is graded by — so each is held
// here by name, with the config parsed the coarse way (line by line) rather
// than through a YAML dependency this module does not carry.
func TestHadolintConfigCarriesTheFleetsTwoDecisions(t *testing.T) {
	lines := map[string]bool{}
	for _, l := range strings.Split(HadolintConfig, "\n") {
		lines[strings.TrimSpace(l)] = true
	}
	for _, want := range []string{
		"failure-threshold: info", // hadolint's own default, written out
		"- DL3008",                // apt pins: the digest pins the output
		"- docker.notusmi.com",    // the mirror
		"- registry.notusmi.com",  // the forge's own images
	} {
		if !lines[want] {
			t.Errorf("the fleet ruleset lacks %q:\n%s", want, HadolintConfig)
		}
	}
	// What is deliberately NOT relaxed. DL3025 (JSON notation) and DL3026
	// (untrusted registry) are the two rules with something to say about the
	// fleet today, and neither may be ignored or demoted by this file. DL3018
	// (unpinned apk) left the ignore list when its only population, infra's
	// tf-runner, retired; it may not quietly return. Nor may DL3064 (a
	// secret-looking ARG/ENV name): it left override.info once no Go star
	// spelled its module hosts as GOPRIVATE.
	for _, keep := range []string{"DL3018", "DL3064", "DL3025", "DL3026", "DL4006", "DL3002", "DL3003"} {
		if strings.Contains(HadolintConfig, keep) {
			t.Errorf("%s is a rule the fleet keeps at its own severity; it must not appear in the ruleset", keep)
		}
	}
	// No line may switch the exit code off or silence pragmas' guard — either
	// would be the one-line off switch stop-justifications exists to refuse.
	for _, forbidden := range []string{"no-fail", "disable-ignore-pragma"} {
		if strings.Contains(HadolintConfig, forbidden) {
			t.Errorf("the fleet ruleset must not carry %q", forbidden)
		}
	}
}

// between answers the text of s after the first from up to the first to
// after it, or "" when either is absent.
func between(s, from, to string) string {
	i := strings.Index(s, from)
	if i < 0 {
		return ""
	}
	rest := s[i+len(from):]
	j := strings.Index(rest, to)
	if j < 0 {
		return rest
	}
	return rest[:j]
}

// The YAML and the Go list of trusted registries are two spellings of one
// decision; a host added to one and not the other is the drift this holds
// against.
func TestHadolintTrustedRegistriesAgreeWithTheConfig(t *testing.T) {
	block := between(HadolintConfig, "trustedRegistries:", "\n\n")
	var inYAML []string
	for _, l := range strings.Split(block, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "- ") {
			inYAML = append(inYAML, strings.TrimPrefix(l, "- "))
		}
	}
	if !reflect.DeepEqual(inYAML, HadolintTrustedRegistries) {
		t.Errorf("the YAML trusts %v, the Go list %v", inYAML, HadolintTrustedRegistries)
	}
	if len(HadolintTrustedRegistries) == 0 {
		t.Fatal("an empty trust list makes DL3026 never fire, which is the invariant images.go states going unenforced")
	}
	for _, h := range HadolintTrustedRegistries {
		if !strings.HasSuffix(h, ".notusmi.com") {
			t.Errorf("%q is not a fleet host — the allowlist is the mirror and the forge, nothing upstream", h)
		}
	}
}

// The config lives outside /src, or the mount would be asked to carry the
// fleet's file and hadolint's cwd lookup would be the thing deciding.
func TestHadolintConfigPathIsOutsideTheTree(t *testing.T) {
	if strings.HasPrefix(HadolintConfigPath, "/src") || !strings.HasPrefix(HadolintConfigPath, "/") {
		t.Errorf("HadolintConfigPath %q must be an absolute path outside /src", HadolintConfigPath)
	}
	if strings.HasSuffix(HadolintConfigPath, "/.hadolint.yaml") {
		t.Errorf("HadolintConfigPath %q must not be spelled as the repository's own file", HadolintConfigPath)
	}
}

// THE PIN IS PART OF THE QUESTION, so the probe must accept exactly the pin.
func TestHadolintVersionOKMatchesTheWholeLine(t *testing.T) {
	cases := []struct {
		out  string
		want bool
	}{
		{"Haskell Dockerfile Linter 2.15.1\n", true},
		{"  Haskell Dockerfile Linter 2.15.1  ", true},
		{"Haskell Dockerfile Linter 2.15.1-rc1\n", false},
		{"Haskell Dockerfile Linter 2.15.10\n", false},
		{"Haskell Dockerfile Linter 2.12.0\n", false},
		{"hadolint 2.15.1\n", false},
		{"", false},
		{"hadolint: command not found", false},
	}
	for _, c := range cases {
		if got := HadolintVersionOK(c.out, HadolintVersion); got != c.want {
			t.Errorf("HadolintVersionOK(%q) = %v, want %v", c.out, got, c.want)
		}
	}
}

// The two pins are literals (OpengrepURL's coverage reason) and this is what
// keeps them one decision: the version in the URL is HadolintVersion, the
// asset is the lowercase name the release actually publishes, and the URL is
// GitHub's own — no fleet address.
func TestHadolintPinsAgree(t *testing.T) {
	const asset = "hadolint-linux-x86_64"
	tail := "/hadolint/hadolint/releases/download/v" + HadolintVersion + "/" + asset
	if !strings.HasSuffix(HadolintURL, tail) {
		t.Errorf("HadolintURL %q does not end in %q", HadolintURL, tail)
	}
	if !strings.HasPrefix(HadolintURL, "https://github.com/") {
		t.Errorf("HadolintURL %q is not upstream", HadolintURL)
	}
	if strings.Contains(HadolintURL, "Linux") {
		t.Errorf("the asset is published lowercase; the capitalised spelling works by redirect and on somebody else's schedule")
	}
}
