package checks

import (
	"reflect"
	"testing"
)

func TestComposeSpecsMatchesTheFourSpellingsAndNothingElse(t *testing.T) {
	files := []string{
		"compose.yaml",
		"compose.yml",
		"docker-compose.yaml",
		"docker-compose.yml",
		"stacks/immich/compose.yaml",
		"stacks/plex/docker-compose.yml",
		// Not specs.
		"compose.yaml.bak",
		"mycompose.yaml",
		"compose/README.md",
		"k8s/deployment.yaml",
		"docker-compose.override.yaml",
		"stacks/", // a Dagger glob names a directory with a trailing slash
	}
	want := []string{
		"compose.yaml",
		"compose.yml",
		"docker-compose.yaml",
		"docker-compose.yml",
		"stacks/immich/compose.yaml",
		"stacks/plex/docker-compose.yml",
	}
	if got := ComposeSpecs(files); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := ComposeSpecs(nil); len(got) != 0 {
		t.Errorf("an empty tree is an empty surface, got %v", got)
	}
}

func TestComposeSpecsIsNotFooledByASuffixMatch(t *testing.T) {
	// `(^|/)` is the anchor that makes this a FILENAME rule rather than a
	// substring one; without it "notcompose.yaml" reads as a spec.
	for _, f := range []string{"notcompose.yaml", "xcompose.yml", "adocker-compose.yml"} {
		if got := ComposeSpecs([]string{f}); len(got) != 0 {
			t.Errorf("%q: got %v, want no spec", f, got)
		}
	}
}

func TestComposeYAMLFilesTakesBothExtensionsAndDropsDirectories(t *testing.T) {
	got := ComposeYAMLFiles([]string{"a.yml", "b.yaml", "c.json", "d/", "./e.yaml", "f.YAML"})
	want := []string{"a.yml", "b.yaml", "e.yaml"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestPinScanFilesExcludesForgejoAtAnyDepth(t *testing.T) {
	files := []string{
		"compose.yaml",
		".forgejo/workflows/validate.yml",
		"stacks/.forgejo/workflows/x.yaml",
		"stacks/immich/compose.yaml",
		"notforgejo/x.yaml",
	}
	want := []string{"compose.yaml", "stacks/immich/compose.yaml", "notforgejo/x.yaml"}
	got := PinScanFiles(files)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestEnvFileRefsIsSortedUniqueAndStripsTheDotSlash(t *testing.T) {
	bodies := map[string]string{
		"compose.yaml": "services:\n  a:\n    env_file: ./secrets/a.env\n  b:\n    env_file:\n      - secrets/a.env\n      - secrets/b.env\n",
		"other.yml":    "env_file: runtime.env\n",
	}
	want := []string{"runtime.env", "secrets/a.env", "secrets/b.env"}
	if got := EnvFileRefs(bodies); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestEnvFileRefsFindsNothingInATreeWithNoEnvFile(t *testing.T) {
	got := EnvFileRefs(map[string]string{"compose.yaml": "services:\n  a:\n    image: nginx\n"})
	if len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
	if got := EnvFileRefs(nil); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

func TestCredentialShapedCatchesEveryShapeTheIgnoreRuleNames(t *testing.T) {
	files := []string{
		".env",
		"stacks/.env",
		".env.production",
		"stacks/immich/.env.local",
		"envs/prod.txt",
		"deploy/envs/staging.yaml",
		"certs/site.pem",
		"certs/site.key",
		"keys/id_rsa",
		// Clean.
		"compose.yaml",
		"README.md",
		"environment.md",
		"src/envelope.go",
	}
	want := []string{
		".env", "stacks/.env", ".env.production", "stacks/immich/.env.local",
		"envs/prod.txt", "deploy/envs/staging.yaml",
		"certs/site.pem", "certs/site.key", "keys/id_rsa",
	}
	if got := CredentialShaped(files); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := CredentialShaped([]string{"compose.yaml", "README.md"}); len(got) != 0 {
		t.Errorf("a clean tree is clean, got %v", got)
	}
}

func TestPinInterpolationsReportsFileLineText(t *testing.T) {
	bodies := map[string]string{
		"compose.yaml": "services:\n  a:\n    image: ${PIN_NGINX}\n  b:\n    image: nginx:stable\n",
		"b.yaml":       "  image: registry/x@sha256:abc\n",
	}
	want := []string{"compose.yaml:3:    image: ${PIN_NGINX}"}
	if got := PinInterpolations(bodies); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestPinInterpolationsIsOrderedByFileThenLine(t *testing.T) {
	bodies := map[string]string{
		"z.yaml": "image: ${PIN_A}\n",
		"a.yaml": "x\nimage: ${PIN_B}\ny\nimage: ${PIN_C}\n",
	}
	want := []string{
		"a.yaml:2:image: ${PIN_B}",
		"a.yaml:4:image: ${PIN_C}",
		"z.yaml:1:image: ${PIN_A}",
	}
	if got := PinInterpolations(bodies); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestPinInterpolationsIgnoresAPinThatIsNotAnImage(t *testing.T) {
	// The ratchet is about what the stack DEPLOYS. A ${PIN_ in a comment or an
	// environment value is not an image reference.
	bodies := map[string]string{
		"compose.yaml": "# the ${PIN_ era is closed\nservices:\n  a:\n    environment:\n      OLD: ${PIN_X}\n",
	}
	if got := PinInterpolations(bodies); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
	if got := PinInterpolations(nil); len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}
