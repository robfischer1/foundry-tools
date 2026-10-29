package atoms

import (
	"context"
	"net/http"
	"time"
)

// Witness is how fleet:witness reaches narcissus. The zero value is the
// production door and the real clock.
type Witness struct {
	// Sleep is the pause between attempts; nil is checks.SleepContext.
	Sleep func(context.Context, time.Duration) error
	// Do sends one request to narcissus's REST door (GET /openapi.json for
	// readiness, POST /v1/witness for each ask); nil is checks.WitnessClient's
	// Do, plaintext from the lane pod.
	Do func(*http.Request) (*http.Response, error)
}
