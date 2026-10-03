// Package fluxpin reads which image a star is pinned to live in the fleet's
// flux tree. Flux image automation writes each star's newTag and digest into
// prime/images/kustomization.yaml; that file is what the cluster runs, and so
// what a lane that wants "the star as it is live" must run. The moving :stable
// tag of a star image is frozen and is not that.
package fluxpin

import (
	"fmt"
	"regexp"

	"go.yaml.in/yaml/v3"
)

// File is the one file Flux image automation writes, in the flux repository.
const File = "prime/images/kustomization.yaml"

var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Ref answers `<image>:<newTag>@<digest>` for the entry named image in the
// kustomization body. Every failure names File and the reason; none falls
// back to a moving tag.
func Ref(body []byte, image string) (string, error) {
	var doc struct {
		Images []struct {
			Name   string `yaml:"name"`
			NewTag string `yaml:"newTag"`
			Digest string `yaml:"digest"`
		} `yaml:"images"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("%s does not parse: %v", File, err)
	}
	for _, e := range doc.Images {
		if e.Name != image {
			continue
		}
		if e.NewTag == "" {
			return "", fmt.Errorf("%s pins %s with no newTag", File, image)
		}
		if !digestRE.MatchString(e.Digest) {
			return "", fmt.Errorf("%s pins %s with a malformed digest %q", File, image, e.Digest)
		}
		return image + ":" + e.NewTag + "@" + e.Digest, nil
	}
	return "", fmt.Errorf("%s has no entry for %s", File, image)
}
