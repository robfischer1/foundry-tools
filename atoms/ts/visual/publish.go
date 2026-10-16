package tsvisual

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/svidcred"
	"dagger/foundry-tools/internal/visuallane"
)

// registry is the artifact's registry, and the audience of the SVID it is
// pushed with (flux foundry/zot-config.json bearer.oidc.audiences).
const registry = "registry.notusmi.com"

// registryBase is where the registry is reached; a test points it at a double.
var registryBase = "https://" + registry

// The seams the push runs through, so a test pushes nowhere.
var (
	getenv = os.Getenv
	// fetchSVID asks the SPIRE agent at socket for a JWT-SVID for audience.
	fetchSVID = func(ctx context.Context, socket, audience string) (string, error) {
		_, tok, err := svidcred.Fetch(ctx, socket, audience)
		return tok, err
	}
	// push stores the layers at ref.
	push = func(ctx context.Context, cred visuallane.Credential, ref string, layers []visuallane.Layer) error {
		return visuallane.Registry{
			Base: registryBase, HTTP: &http.Client{Timeout: 5 * time.Minute}, Cred: cred,
		}.PushRef(ctx, ref, layers)
	}
)

// publish pushes the run's artifact and answers the line that says where it
// is, or why it is not. It never changes the verdict: the findings are the
// finding, and the artifact is how a person reads them and accepts a change.
func (e env) publish(ctx context.Context, runs []*suiteRun) string {
	ref, err := visuallane.ArtifactRef(registry, e.in.Origin, getenv(envSha))
	if err != nil {
		return "artifact: not pushed — " + err.Error()
	}
	if getenv(envAuth) == "" && getenv(envSocket) == "" {
		return "artifact: not pushed — the lane was given no --artifact-auth"
	}
	// NOTHING TO CARRY IS NOT A PUSH. A suite that never ran, because what it
	// imports did not build or it has no package.json, writes no results. An
	// image with no layers is a manifest the registry refuses: "PUT
	// …/manifests/<sha>: 400 Bad Request, unknown: manifest invalid" (gijmo-ui
	// 4ed2dae, 2026-10-05). That failed push put six ERROR spans into a
	// transcript whose real finding was the build. Baselines are only
	// regenerated for a suite that ran and wrote results, so results alone say
	// whether anything is carried.
	carried := false
	for _, r := range runs {
		carried = carried || r.ran
	}
	if !carried {
		return "artifact: not pushed — no suite ran far enough to write results or baselines"
	}
	cred, err := e.login(ctx)
	if err != nil {
		return "artifact: not pushed — " + err.Error()
	}
	layers, err := e.layers(runs)
	if err != nil {
		return "artifact: not pushed — " + err.Error()
	}
	if err := push(ctx, cred, ref, layers); err != nil {
		return "artifact: not pushed — " + err.Error()
	}
	return visuallane.FetchHint(ref)
}

// layers are the artifact's: per suite, the check run's results under
// results/<dir>, and every snapshot the update run left in the suite under
// baselines/<dir>, at its path in the tree.
func (e env) layers(runs []*suiteRun) ([]visuallane.Layer, error) {
	var out []visuallane.Layer
	for _, r := range runs {
		if r.updateErr != "" {
			return nil, errors.New(r.updateErr)
		}
		if r.ran {
			results := filepath.Join(r.out, "results")
			l, err := layerOf(path.Join("results", r.dir), results, nil)
			if err != nil {
				return nil, err
			}
			out = append(out, l)
		}
		if r.updated {
			l, err := layerOf(path.Join("baselines", r.dir), e.suite(r.dir), visuallane.Snapshots)
			if err != nil {
				return nil, err
			}
			out = append(out, l)
		}
	}
	return out, nil
}

// layerOf is the files under root (less what keep drops, when given) as a layer
// kept at prefix.
func layerOf(prefix, root string, keep func([]string) []string) (visuallane.Layer, error) {
	files, err := visuallane.Files(root)
	if err != nil {
		return visuallane.Layer{}, err
	}
	if keep != nil {
		files = keep(files)
	}
	return visuallane.TarLayer(prefix, root, files)
}

// login is the push's credential: the Dagger runner's static login when it
// handed one, else the pod's JWT-SVID, fetched now, right before the push (it
// lives 15 minutes).
func (e env) login(ctx context.Context) (visuallane.Credential, error) {
	if auth := getenv(envAuth); auth != "" {
		user, password, err := buildlane.RegistryLogin(auth, registry)
		return visuallane.Credential{User: user, Password: password}, err
	}
	tok, err := fetchSVID(ctx, getenv(envSocket), registry)
	if err != nil {
		return visuallane.Credential{}, fmt.Errorf("the registry JWT-SVID did not fetch: %w", err)
	}
	return visuallane.Credential{Bearer: tok}, nil
}
